package sender

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSendSuccess(t *testing.T) {
	var received GASPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("unmarshal: %v", err)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	err := c.Send(context.Background(), "trip", map[string]string{"id": "abc"})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if received.Type != "trip" {
		t.Errorf("type: got %q, want trip", received.Type)
	}
}

func TestSendServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	err := c.Send(context.Background(), "trip", map[string]string{"id": "abc"})
	if err == nil {
		t.Fatal("expected error on 500 response")
	}

	// エラー時はリトライキューに入る
	if c.QueueSize() != 1 {
		t.Errorf("queue size: got %d, want 1", c.QueueSize())
	}
}

func TestSendNetworkError(t *testing.T) {
	c := NewClient("http://127.0.0.1:1") // 接続不可
	err := c.Send(context.Background(), "trip", map[string]string{"id": "abc"})
	if err == nil {
		t.Fatal("expected error on unreachable server")
	}
	if c.QueueSize() != 1 {
		t.Errorf("queue size: got %d, want 1", c.QueueSize())
	}
}

func TestRetryPending(t *testing.T) {
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n <= 1 {
			w.WriteHeader(500) // 初回失敗
		} else {
			w.WriteHeader(200) // リトライ成功
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	_ = c.Send(context.Background(), "trip", map[string]string{"id": "abc"}) // 失敗 → キュー
	if c.QueueSize() != 1 {
		t.Fatalf("queue should have 1 item, got %d", c.QueueSize())
	}

	c.RetryPending(context.Background()) // 成功
	if c.QueueSize() != 0 {
		t.Errorf("queue should be empty after retry, got %d", c.QueueSize())
	}
}

func TestQueueMaxSize(t *testing.T) {
	c := NewClient("http://127.0.0.1:1")

	// 100件まで溜まる
	for i := 0; i < 110; i++ {
		c.enqueue(GASPayload{Type: "test"})
	}
	if c.QueueSize() != 100 {
		t.Errorf("queue max: got %d, want 100", c.QueueSize())
	}
}

func TestRetryPendingEmpty(t *testing.T) {
	c := NewClient("http://example.com")
	c.RetryPending(context.Background()) // キュー空でもパニックしない
	if c.QueueSize() != 0 {
		t.Error("queue should remain empty")
	}
}

func TestSendWithResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"ok","pending_resets":["oil_change"]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	body, err := c.SendWithResponse(context.Background(), "maintenance", map[string]string{"id": "test"})
	if err != nil {
		t.Fatalf("SendWithResponse failed: %v", err)
	}
	if body == nil {
		t.Fatal("expected non-nil response body")
	}

	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status: got %v, want ok", resp["status"])
	}
}

func TestSendWithResponseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	body, err := c.SendWithResponse(context.Background(), "trip", map[string]string{"id": "abc"})
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if body != nil {
		t.Error("expected nil body on error")
	}
	if c.QueueSize() != 1 {
		t.Errorf("queue size: got %d, want 1", c.QueueSize())
	}
}

func TestRetryBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500) // 常に失敗
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.enqueue(GASPayload{Type: "test"})

	// 初回リトライ: バックオフなしで実行される（lastRetryAtゼロ）
	c.RetryPending(context.Background())
	if c.consecutiveFails != 1 {
		t.Errorf("consecutiveFails: got %d, want 1", c.consecutiveFails)
	}
	if c.QueueSize() != 1 {
		t.Error("failed item should be re-enqueued")
	}

	// 2回目: lastRetryAtが直前なのでバックオフでスキップされる
	c.RetryPending(context.Background())
	if c.consecutiveFails != 1 {
		t.Errorf("consecutiveFails should stay 1 (skipped), got %d", c.consecutiveFails)
	}
}

func TestRetrySkipsRemainingOnFailure(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			w.WriteHeader(200) // 1件目成功
		} else {
			w.WriteHeader(500) // 2件目以降失敗
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.enqueue(GASPayload{Type: "a"})
	c.enqueue(GASPayload{Type: "b"})
	c.enqueue(GASPayload{Type: "c"})

	c.RetryPending(context.Background())

	// 1件成功、2件目失敗で残り2件がキューに戻る
	if c.QueueSize() != 2 {
		t.Errorf("queue size: got %d, want 2", c.QueueSize())
	}
	if callCount.Load() != 2 {
		t.Errorf("call count: got %d, want 2 (3rd should be skipped)", callCount.Load())
	}
}

func TestIsSending(t *testing.T) {
	c := NewClient("http://example.com")
	if c.IsSending() {
		t.Error("should not be sending initially")
	}
}

// --- RestoreState テスト ---

func TestRestoreState_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p GASPayload
		if err := json.Unmarshal(body, &p); err != nil {
			t.Errorf("unmarshal: %v", err)
		}
		if p.Type != "restore" {
			t.Errorf("type: got %q, want restore", p.Type)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"ok","total_km":12345.6,"last_refuel_km":12000}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	resp, err := c.RestoreState(context.Background())
	if err != nil {
		t.Fatalf("RestoreState failed: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status: got %q, want ok", resp.Status)
	}
	if resp.TotalKm != 12345.6 {
		t.Errorf("total_km: got %.1f, want 12345.6", resp.TotalKm)
	}
	if resp.LastRefuelKm != 12000 {
		t.Errorf("last_refuel_km: got %.1f, want 12000", resp.LastRefuelKm)
	}
}

func TestRestoreState_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	_, err := c.RestoreState(context.Background())
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestRestoreState_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	_, err := c.RestoreState(context.Background())
	if err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}

func TestRestoreState_NetworkError(t *testing.T) {
	c := NewClient("http://127.0.0.1:1")
	_, err := c.RestoreState(context.Background())
	if err == nil {
		t.Fatal("expected error on unreachable server")
	}
}

func TestRestoreState_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	resp, err := c.RestoreState(context.Background())
	if err != nil {
		t.Fatalf("RestoreState failed: %v", err)
	}
	if resp.TotalKm != 0 {
		t.Errorf("total_km should be 0 when missing, got %.1f", resp.TotalKm)
	}
	if resp.LastRefuelKm != 0 {
		t.Errorf("last_refuel_km should be 0 when missing, got %.1f", resp.LastRefuelKm)
	}
}

func TestPayloadJSON(t *testing.T) {
	p := GASPayload{Type: "refuel", Data: map[string]float64{"fuel_economy": 12.5}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded["type"] != "refuel" {
		t.Errorf("type: got %v, want refuel", decoded["type"])
	}
	data := decoded["data"].(map[string]any)
	if data["fuel_economy"] != 12.5 {
		t.Errorf("fuel_economy: got %v, want 12.5", data["fuel_economy"])
	}
}
