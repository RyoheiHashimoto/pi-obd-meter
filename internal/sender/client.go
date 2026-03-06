package sender

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/hashimoto/pi-obd-meter/internal/trip"
)

// Client はGoogle Apps Script Webhookへデータを送信するクライアント
type Client struct {
	webhookURL string
	httpClient *http.Client
	retryQueue []GASPayload // メモリ上のリトライキュー（overlayFSのためファイル保存しない）
	mu         sync.Mutex
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
	payload := GASPayload{Type: payloadType, Data: data}
	return c.sendPayload(payload)
}

// SendTrip はトリップデータをGASに送信する
func (c *Client) SendTrip(data trip.TripData) error {
	return c.Send("trip", data)
}

// sendPayload はペイロードを送信する
func (c *Client) sendPayload(payload GASPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("JSON変換エラー: %w", err)
	}

	resp, err := c.httpClient.Post(c.webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("送信失敗（リトライキューに追加）[%s]: %v", payload.Type, err)
		c.enqueue(payload)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		c.enqueue(payload)
		return fmt.Errorf("Webhook エラー [%s]: status %d", payload.Type, resp.StatusCode)
	}

	log.Printf("✓ %s データ送信完了", payload.Type)
	return nil
}

// RetryPending はキューに溜まったデータの再送を試みる
func (c *Client) RetryPending() {
	c.mu.Lock()
	if len(c.retryQueue) == 0 {
		c.mu.Unlock()
		return
	}

	queue := make([]GASPayload, len(c.retryQueue))
	copy(queue, c.retryQueue)
	c.retryQueue = nil
	c.mu.Unlock()

	log.Printf("未送信データ %d 件をリトライ中...", len(queue))

	for _, payload := range queue {
		if err := c.sendDirect(payload); err != nil {
			c.enqueue(payload)
		}
	}
}

// QueueSize はリトライキューのサイズを返す
func (c *Client) QueueSize() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.retryQueue)
}

// sendDirect はリトライキューに入れずに直接送信する
func (c *Client) sendDirect(payload GASPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Post(c.webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	log.Printf("✓ リトライ送信完了 [%s]", payload.Type)
	return nil
}

func (c *Client) enqueue(payload GASPayload) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.retryQueue) < 100 {
		c.retryQueue = append(c.retryQueue, payload)
	}
}
