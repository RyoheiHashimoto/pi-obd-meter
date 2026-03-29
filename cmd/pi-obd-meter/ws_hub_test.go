package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// wsURL はhttptest.ServerのURLをws://に変換する
func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestWSHub_ConnectAndReceive(t *testing.T) {
	var data atomic.Value
	data.Store(RealtimeData{SpeedKmh: 60, RPM: 3000})

	hub := NewWSHub(WebSocketConfig{
		Enabled:             true,
		BroadcastIntervalMs: 20,
		MaxClients:          3,
	}, func() RealtimeData {
		return data.Load().(RealtimeData)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	srv := httptest.NewServer(http.HandlerFunc(hub.HandleWebSocket))
	defer srv.Close()

	conn, _, err := websocket.Dial(ctx, wsURL(srv), nil)
	if err != nil {
		t.Fatalf("WebSocket接続失敗: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	// 初回データ受信
	_, msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("初回データ受信失敗: %v", err)
	}

	var got RealtimeData
	if err := json.Unmarshal(msg, &got); err != nil {
		t.Fatalf("JSONパース失敗: %v", err)
	}
	if got.SpeedKmh != 60 {
		t.Errorf("SpeedKmh = %v, want 60", got.SpeedKmh)
	}
	if got.RPM != 3000 {
		t.Errorf("RPM = %v, want 3000", got.RPM)
	}
}

func TestWSHub_BroadcastUpdate(t *testing.T) {
	var data atomic.Value
	data.Store(RealtimeData{SpeedKmh: 40})

	hub := NewWSHub(WebSocketConfig{
		Enabled:             true,
		BroadcastIntervalMs: 20,
		MaxClients:          3,
	}, func() RealtimeData {
		return data.Load().(RealtimeData)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	srv := httptest.NewServer(http.HandlerFunc(hub.HandleWebSocket))
	defer srv.Close()

	conn, _, err := websocket.Dial(ctx, wsURL(srv), nil)
	if err != nil {
		t.Fatalf("接続失敗: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	// 初回データを読み飛ばす
	_, _, _ = conn.Read(ctx)

	// データ更新
	data.Store(RealtimeData{SpeedKmh: 80})

	// broadcast されたデータを受信
	readCtx, readCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer readCancel()

	_, msg, err := conn.Read(readCtx)
	if err != nil {
		t.Fatalf("ブロードキャスト受信失敗: %v", err)
	}

	var got RealtimeData
	if err := json.Unmarshal(msg, &got); err != nil {
		t.Fatalf("JSONパース失敗: %v", err)
	}
	if got.SpeedKmh != 80 {
		t.Errorf("更新後 SpeedKmh = %v, want 80", got.SpeedKmh)
	}
}

func TestWSHub_MaxClients(t *testing.T) {
	hub := NewWSHub(WebSocketConfig{
		Enabled:             true,
		BroadcastIntervalMs: 50,
		MaxClients:          1,
	}, func() RealtimeData {
		return RealtimeData{}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	srv := httptest.NewServer(http.HandlerFunc(hub.HandleWebSocket))
	defer srv.Close()

	// 1つ目の接続: 成功
	conn1, _, err := websocket.Dial(ctx, wsURL(srv), nil)
	if err != nil {
		t.Fatalf("1つ目の接続失敗: %v", err)
	}
	defer func() { _ = conn1.CloseNow() }()

	// 初回データを読んでクライアント登録を完了させる
	_, _, _ = conn1.Read(ctx)
	time.Sleep(20 * time.Millisecond)

	// 2つ目の接続: 拒否されるべき
	_, _, err = websocket.Dial(ctx, wsURL(srv), nil)
	if err == nil {
		t.Error("max_clients=1 なのに2つ目の接続が成功した")
	}
}

func TestWSHub_Disconnect(t *testing.T) {
	hub := NewWSHub(WebSocketConfig{
		Enabled:             true,
		BroadcastIntervalMs: 20,
		MaxClients:          3,
	}, func() RealtimeData {
		return RealtimeData{}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	srv := httptest.NewServer(http.HandlerFunc(hub.HandleWebSocket))
	defer srv.Close()

	conn, _, err := websocket.Dial(ctx, wsURL(srv), nil)
	if err != nil {
		t.Fatalf("接続失敗: %v", err)
	}

	// 初回データを読む
	_, _, _ = conn.Read(ctx)
	time.Sleep(20 * time.Millisecond)

	if hub.ClientCount() != 1 {
		t.Errorf("接続後クライアント数 = %d, want 1", hub.ClientCount())
	}

	// 切断
	_ = conn.Close(websocket.StatusNormalClosure, "test")
	time.Sleep(50 * time.Millisecond)

	if hub.ClientCount() != 0 {
		t.Errorf("切断後クライアント数 = %d, want 0", hub.ClientCount())
	}
}

func TestWSHub_GracefulShutdown(t *testing.T) {
	hub := NewWSHub(WebSocketConfig{
		Enabled:             true,
		BroadcastIntervalMs: 20,
		MaxClients:          3,
	}, func() RealtimeData {
		return RealtimeData{}
	})

	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)

	srv := httptest.NewServer(http.HandlerFunc(hub.HandleWebSocket))
	defer srv.Close()

	conn, _, err := websocket.Dial(ctx, wsURL(srv), nil)
	if err != nil {
		t.Fatalf("接続失敗: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	// 初回データを読む
	_, _, _ = conn.Read(ctx)
	time.Sleep(20 * time.Millisecond)

	// サーバー側 shutdown
	cancel()
	time.Sleep(50 * time.Millisecond)

	if hub.ClientCount() != 0 {
		t.Errorf("shutdown後クライアント数 = %d, want 0", hub.ClientCount())
	}
}
