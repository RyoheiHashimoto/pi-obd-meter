package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// WSHub はWebSocketクライアントの管理とブロードキャストを行う
type WSHub struct {
	mu         sync.Mutex
	clients    map[*wsClient]struct{}
	maxClients int
	intervalMs int
	getData    func() RealtimeData
}

// wsClient は接続中のWebSocketクライアント
type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

// NewWSHub はWSHubを作成する
func NewWSHub(cfg WebSocketConfig, getData func() RealtimeData) *WSHub {
	return &WSHub{
		clients:    make(map[*wsClient]struct{}),
		maxClients: cfg.MaxClients,
		intervalMs: cfg.BroadcastIntervalMs,
		getData:    getData,
	}
}

// Run はブロードキャストループを開始する。ctx キャンセルで停止。
func (h *WSHub) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(h.intervalMs) * time.Millisecond)
	defer ticker.Stop()

	var lastJSON []byte
	heartbeatInterval := time.Second
	lastSent := time.Now()

	for {
		select {
		case <-ctx.Done():
			h.closeAll()
			return
		case <-ticker.C:
			data := h.getData()
			buf, err := json.Marshal(data)
			if err != nil {
				slog.Warn("WebSocket JSON エンコードエラー", "error", err)
				continue
			}

			// データ変化なし & ハートビート不要ならスキップ
			now := time.Now()
			if string(buf) == string(lastJSON) && now.Sub(lastSent) < heartbeatInterval {
				continue
			}
			lastJSON = buf
			lastSent = now

			h.broadcast(buf)
		}
	}
}

// broadcast は全クライアントにメッセージを送信する（drop-oldest）
func (h *WSHub) broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
			// drop-oldest: 古いメッセージを捨てて最新を入れる
			select {
			case <-c.send:
			default:
			}
			c.send <- msg
		}
	}
}

// HandleWebSocket はWebSocket接続を受け付けるHTTPハンドラ
func (h *WSHub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	if len(h.clients) >= h.maxClients {
		h.mu.Unlock()
		http.Error(w, "WebSocket クライアント数上限", http.StatusServiceUnavailable)
		slog.Warn("WebSocket 接続拒否: クライアント数上限", "max", h.maxClients)
		return
	}
	h.mu.Unlock()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // LAN専用、CORS origin チェック不要
	})
	if err != nil {
		slog.Warn("WebSocket Accept エラー", "error", err)
		return
	}

	client := &wsClient{
		conn: conn,
		send: make(chan []byte, 1),
	}

	h.mu.Lock()
	h.clients[client] = struct{}{}
	clientCount := len(h.clients)
	h.mu.Unlock()

	slog.Info("WebSocket クライアント接続", "clients", clientCount)

	go h.writePump(client)

	// 初回データ即送信（画面が黒にならないように）
	if initial, err := json.Marshal(h.getData()); err == nil {
		client.send <- initial
	}

	h.readPump(client) // ブロック（切断まで）
}

// writePump はクライアントへの書き込みを担当する goroutine
func (h *WSHub) writePump(c *wsClient) {
	defer func() {
		h.remove(c)
		_ = c.conn.Close(websocket.StatusNormalClosure, "")
	}()

	for msg := range c.send {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := c.conn.Write(ctx, websocket.MessageText, msg)
		cancel()
		if err != nil {
			slog.Debug("WebSocket 書き込みエラー", "error", err)
			return
		}
	}
}

// readPump はクライアントからの close/ping を検出する（ブロッキング）
func (h *WSHub) readPump(c *wsClient) {
	defer func() {
		h.remove(c)
		close(c.send)
	}()

	for {
		// クライアントからのメッセージは期待しないが、切断検出のために読み続ける
		_, _, err := c.conn.Read(context.Background())
		if err != nil {
			return
		}
	}
}

// remove はクライアントをHubから削除する
func (h *WSHub) remove(c *wsClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		slog.Info("WebSocket クライアント切断", "clients", len(h.clients))
	}
}

// closeAll は全クライアントを閉じる（shutdown用）
// CloseNow で即座にコネクションを破棄し、readPump をブロックさせない
func (h *WSHub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		_ = c.conn.CloseNow()
		delete(h.clients, c)
	}
}

// ClientCount は接続中のクライアント数を返す
func (h *WSHub) ClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}
