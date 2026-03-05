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
	retryQueue []trip.TripData // メモリ上のリトライキュー（overlayFSのためファイル保存しない）
	mu         sync.Mutex
}

// NewClient は新しいクライアントを作成する
func NewClient(webhookURL string) *Client {
	return &Client{
		webhookURL: webhookURL,
		httpClient: &http.Client{
			Timeout:       30 * time.Second, // GASは応答が遅いことがある
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return nil // GASのリダイレクトを許可
			},
		},
	}
}

// GASPayload はGoogle Apps Scriptに送信するペイロード
type GASPayload struct {
	Type string      `json:"type"` // "trip"
	Data interface{} `json:"data"`
}

// SendTrip はトリップデータをGoogle Apps Scriptに送信する
func (c *Client) SendTrip(data trip.TripData) error {
	payload := GASPayload{
		Type: "trip",
		Data: data,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("JSON変換エラー: %w", err)
	}

	resp, err := c.httpClient.Post(c.webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		// WiFi未接続等 → メモリキューに入れてリトライ
		log.Printf("送信失敗（リトライキューに追加）: %v", err)
		c.enqueue(data)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		c.enqueue(data)
		return fmt.Errorf("Webhook エラー: status %d", resp.StatusCode)
	}

	log.Printf("✓ トリップデータ送信完了: %s (%.1f km, %.1f km/L)",
		data.TripID, data.DistanceKm, data.AvgFuelEconKm)

	return nil
}

// RetryPending はキューに溜まったデータの再送を試みる
func (c *Client) RetryPending() {
	c.mu.Lock()
	if len(c.retryQueue) == 0 {
		c.mu.Unlock()
		return
	}

	queue := make([]trip.TripData, len(c.retryQueue))
	copy(queue, c.retryQueue)
	c.retryQueue = nil
	c.mu.Unlock()

	log.Printf("未送信データ %d 件をリトライ中...", len(queue))

	for _, data := range queue {
		if err := c.sendDirect(data); err != nil {
			c.enqueue(data)
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
func (c *Client) sendDirect(data trip.TripData) error {
	payload := GASPayload{
		Type: "trip",
		Data: data,
	}

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

	log.Printf("✓ リトライ送信完了: %s", data.TripID)
	return nil
}

func (c *Client) enqueue(data trip.TripData) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 最大100件に制限（メモリ保護）
	if len(c.retryQueue) < 100 {
		c.retryQueue = append(c.retryQueue, data)
	}
}
