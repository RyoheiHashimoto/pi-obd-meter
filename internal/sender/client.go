// Package sender はGoogle Apps Script Webhookへのデータ送信を提供する。
// 送信失敗時はメモリ内リトライキュー（最大100件）に保持し、定期的に再送する。
// overlayFS環境を考慮し、ディスクへの永続化は行わない。
package sender

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Client はGoogle Apps Script Webhookへデータを送信するクライアント
type Client struct {
	webhookURL     string
	httpClient     *http.Client
	retryQueue     []GASPayload // メモリ上のリトライキュー（overlayFSのためファイル保存しない）
	mu             sync.Mutex
	sending        bool // 送信中フラグ
	consecutiveFails int  // 連続失敗回数（指数バックオフ用）
	lastRetryAt    time.Time // 最後にリトライした時刻
}

// NewClient は新しいクライアントを作成する
func NewClient(webhookURL string) *Client {
	return &Client{
		webhookURL: webhookURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return nil // GASのリダイレクトを許可
			},
		},
	}
}

// GASPayload はGoogle Apps Scriptに送信するペイロード
type GASPayload struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// Send は汎用データをGASに送信する
func (c *Client) Send(payloadType string, data interface{}) error {
	_, err := c.SendWithResponse(payloadType, data)
	return err
}

// SendWithResponse はデータをGASに送信し、レスポンスボディを返す
func (c *Client) SendWithResponse(payloadType string, data interface{}) ([]byte, error) {
	payload := GASPayload{Type: payloadType, Data: data}
	respBody, err := c.doPost(payload)
	if err != nil {
		c.enqueue(payload)
		return nil, err
	}
	slog.Info("データ送信完了", "type", payloadType)
	return respBody, nil
}

// doPost はHTTP POSTを実行し、レスポンスボディを返す。リトライキューには追加しない。
func (c *Client) doPost(payload GASPayload) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("JSON変換エラー: %w", err)
	}

	c.mu.Lock()
	c.sending = true
	c.mu.Unlock()

	resp, err := c.httpClient.Post(c.webhookURL, "application/json", bytes.NewReader(body))

	c.mu.Lock()
	c.sending = false
	c.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("送信失敗 [%s]: %w", payload.Type, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Webhook エラー [%s]: status %d", payload.Type, resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Warn("レスポンス読み取りエラー", "type", payload.Type, "error", err)
		return nil, nil // 送信自体は成功
	}

	return respBody, nil
}

// retryBackoff は連続失敗回数に応じたバックオフ間隔を返す（最大30分）
func (c *Client) retryBackoff() time.Duration {
	if c.consecutiveFails <= 0 {
		return 0
	}
	// 5分 × 2^(fails-1): 5m, 10m, 20m, 30m(cap)
	d := 5 * time.Minute
	for i := 1; i < c.consecutiveFails; i++ {
		d *= 2
	}
	const maxBackoff = 30 * time.Minute
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

// RetryPending はキューに溜まったデータの再送を試みる。
// 連続失敗時は指数バックオフで間隔を広げ、1件でも失敗したら残りをスキップする。
func (c *Client) RetryPending() {
	c.mu.Lock()
	if len(c.retryQueue) == 0 {
		c.mu.Unlock()
		return
	}

	// バックオフ中ならスキップ
	if backoff := c.retryBackoff(); backoff > 0 {
		if time.Since(c.lastRetryAt) < backoff {
			c.mu.Unlock()
			return
		}
	}
	c.lastRetryAt = time.Now()

	queue := make([]GASPayload, len(c.retryQueue))
	copy(queue, c.retryQueue)
	c.retryQueue = nil
	c.mu.Unlock()

	slog.Info("未送信データリトライ開始", "count", len(queue))

	for i, payload := range queue {
		if _, err := c.doPost(payload); err != nil {
			// 失敗: 残りを全部キューに戻す
			c.mu.Lock()
			c.consecutiveFails++
			c.mu.Unlock()
			for j := i; j < len(queue); j++ {
				c.enqueue(queue[j])
			}
			slog.Warn("リトライ失敗、残りスキップ",
				"failed_type", payload.Type,
				"remaining", len(queue)-i,
				"next_backoff", c.retryBackoff().String())
			return
		}
		slog.Info("リトライ送信完了", "type", payload.Type)
	}

	// 全件成功
	c.mu.Lock()
	c.consecutiveFails = 0
	c.mu.Unlock()
}

// QueueSize はリトライキューのサイズを返す
func (c *Client) QueueSize() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.retryQueue)
}

// IsSending は送信中かどうかを返す
func (c *Client) IsSending() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sending
}

// RestoreResponse はGASから返される状態復元レスポンス
type RestoreResponse struct {
	Status      string  `json:"status"`
	TotalKm     float64 `json:"total_km"`
	LastRefuelKm float64 `json:"last_refuel_km"`
}

// RestoreState はGASから保存済みの状態を復元する（起動時に1回呼ぶ）
func (c *Client) RestoreState() (*RestoreResponse, error) {
	payload := GASPayload{Type: "restore", Data: nil}
	respBody, err := c.doPost(payload)
	if err != nil {
		return nil, fmt.Errorf("状態復元失敗: %w", err)
	}

	var resp RestoreResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("状態復元レスポンスパース失敗: %w", err)
	}

	return &resp, nil
}

// enqueue は送信失敗したペイロードをリトライキューに追加する（上限100件）
func (c *Client) enqueue(payload GASPayload) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.retryQueue) < 100 {
		c.retryQueue = append(c.retryQueue, payload)
	}
}
