package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// 給油の検出から、GAS の応答でトリップがリセットされるまでを通して確かめる。
//
// この経路は1回の送信で最後まで進む（検出 → 記録 → 応答の
// trip_correction_km=0 → リセット）。途中で止まってもメーターからは
// 分からなかったのでダイアログを足した (#120, #178)。段階が実際に
// 進むことを、GAS の代わりに httptest で確かめる。
func TestRefuelFlow_DetectToTripReset(t *testing.T) {
	var gotPayload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("リクエストの JSON を読めない: %v", err)
		}
		if body.Type == "maintenance" {
			gotPayload = body.Data
		}
		// GAS は給油を記録すると trip_correction_km=0 を返す (webhook.gs:260)
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"trip_correction_km":0}`)); err != nil {
			t.Errorf("応答を書けない: %v", err)
		}
	}))
	defer srv.Close()

	stateDir := t.TempDir()
	// 前回の停車時は 20pt。ここから満タンまで跳ねれば給油と判定される。
	if err := os.WriteFile(filepath.Join(stateDir, "fuel_state.json"),
		[]byte(`{"last_settled_pt":20}`), 0o600); err != nil {
		t.Fatalf("状態ファイルを置けない: %v", err)
	}

	app := newApp(Config{
		MaintenancePath: filepath.Join(stateDir, "m.json"),
		WebhookURL:      srv.URL,
	})
	app.tracker.SetDistance(432.1) // 前のタンクで走った距離

	// 停車中のサンプルが溜まると検出が働く。
	for range 40 {
		app.refuel.Update(95.3, true)
	}
	ev := app.refuel.Event()
	if ev == nil {
		t.Fatal("給油を検出していない")
	}
	if !app.noteRefuelDetected(ev) {
		t.Fatal("ダイアログが検出を受け取っていない")
	}
	if got := app.refuelUISnapshot(0); got == nil || got.Stage != refuelStageDetected {
		t.Fatalf("送信前の stage が違う: %+v", got)
	}

	app.sendMaintenanceStatus(context.Background())

	if gotPayload["refuel_detected"] != true {
		t.Errorf("GAS への payload に給油が乗っていない: %v", gotPayload)
	}
	if id, _ := gotPayload["refuel_event_id"].(string); id == "" || id != ev.ID() {
		t.Errorf("payload の refuel_event_id = %v, want %q", gotPayload["refuel_event_id"], ev.ID())
	}
	got := app.refuelUISnapshot(0)
	if got == nil || got.Stage != refuelStageTripReset {
		t.Fatalf("トリップリセットまで進んでいない: %+v", got)
	}
	if got.PrevTripKm < 432.0 || got.PrevTripKm > 432.2 {
		t.Errorf("前のタンクの距離 = %.1f, want 432.1（リセット前の値を控えていない）", got.PrevTripKm)
	}
	if km := app.tracker.DistanceKm(); km != 0 {
		t.Errorf("トリップがリセットされていない: %.1f km", km)
	}
	if app.refuel.Event() != nil {
		t.Error("送信済みの給油イベントが消えていない（次回起動で二重記録になる）")
	}
}

// 検出時にトリップを畳んであれば、GAS の応答に trip_correction_km=0 が無くても
// 記録が通った時点でダイアログが完了まで進む。
//
// 2026-11-18 の給油で、送信中に自動更新でメーターが再起動し、新しいプロセスの
// 応答では最後まで進まなかった。ダイアログは「記録」で止まり、停車のたびに
// 15 分出続けた。
func TestRefuelFlow_FoldedTripCompletesWithoutGASReset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`)) // trip_correction_km を返さない
	}))
	defer srv.Close()

	app := newRefuelTestApp(t, srv.URL)
	app.tracker.SetDistance(477.6)
	detectRefuel(t, app)
	app.resetTripOnRefuel() // main.go と同じく、検出の時点で畳む
	if !app.refuel.Event().TripFolded {
		t.Fatal("検出時にトリップを畳んでいない")
	}

	app.sendMaintenanceStatus(context.Background())

	got := app.refuelUISnapshot(0)
	if got == nil || got.Stage != refuelStageTripReset {
		t.Fatalf("記録が通ったのに完了まで進んでいない: %+v", got)
	}
	if got.PrevTripKm < 477.5 || got.PrevTripKm > 477.7 {
		t.Errorf("前のタンクの距離 = %.1f, want 477.6", got.PrevTripKm)
	}
}

// 起動時の送信と、検出した瞬間の送信が重なっても、給油は GAS に 1 回しか送らない。
//
// 2026-11-18 の給油で、起動 45 秒と 48 秒に同じ給油を 2 回送っていた
// (webhook.gs は送られた分だけ給油の行を作る)。
func TestRefuelFlow_ConcurrentSendsRecordOnce(t *testing.T) {
	var mu sync.Mutex
	refuels := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Type == "maintenance" && body.Data["refuel_detected"] == true {
			mu.Lock()
			refuels++
			mu.Unlock()
		}
		time.Sleep(200 * time.Millisecond) // GAS の応答の遅さ。2 本を確実に重ねる
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"trip_correction_km":0}`))
	}))
	defer srv.Close()

	app := newRefuelTestApp(t, srv.URL)
	app.tracker.SetDistance(300)
	detectRefuel(t, app)
	app.resetTripOnRefuel()

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			app.sendMaintenanceStatus(context.Background())
		}()
	}
	wg.Wait()

	if refuels != 1 {
		t.Errorf("給油を GAS に %d 回送った, want 1", refuels)
	}
}

func newRefuelTestApp(t *testing.T, url string) *App {
	t.Helper()
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "fuel_state.json"),
		[]byte(`{"last_settled_pt":12}`), 0o600); err != nil {
		t.Fatalf("状態ファイルを置けない: %v", err)
	}
	return newApp(Config{
		MaintenancePath: filepath.Join(stateDir, "m.json"),
		WebhookURL:      url,
	})
}

func detectRefuel(t *testing.T, app *App) {
	t.Helper()
	for range 40 {
		app.refuel.Update(95.3, true)
	}
	ev := app.refuel.Event()
	if ev == nil {
		t.Fatal("給油を検出していない")
	}
	if !app.noteRefuelDetected(ev) {
		t.Fatal("ダイアログが検出を受け取っていない")
	}
}

// 給油を載せた送信が失敗しても再送キューには入れず、次の送信で 1 回だけ記録する。
//
// キューに入れると、次の送信で記録された後にキューからも再送され、
// 同じ給油が GAS に 2 行できる (2026-11-18 のクロスレビューの指摘)。
func TestRefuelFlow_FailedSendIsNotQueued(t *testing.T) {
	var mu sync.Mutex
	fail, refuels := true, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		var body struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Data["refuel_detected"] == true {
			refuels++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"trip_correction_km":0}`))
	}))
	defer srv.Close()

	app := newRefuelTestApp(t, srv.URL)
	app.tracker.SetDistance(300)
	detectRefuel(t, app)
	app.resetTripOnRefuel()

	app.sendMaintenanceStatus(context.Background()) // 失敗する
	if n := app.client.QueueSize(); n != 0 {
		t.Errorf("給油を載せた送信が再送キューに %d 件入った, want 0", n)
	}
	if app.refuel.Event() == nil {
		t.Fatal("送れていない給油イベントが消えた")
	}

	mu.Lock()
	fail = false
	mu.Unlock()
	app.sendMaintenanceStatus(context.Background()) // 通る
	app.client.RetryPending(context.Background())   // キューが空なら何も送らない

	if refuels != 1 {
		t.Errorf("給油を GAS に %d 回記録した, want 1", refuels)
	}
	if got := app.refuelUISnapshot(0); got == nil || got.Stage != refuelStageTripReset {
		t.Errorf("記録が通った後に完了まで進んでいない: %+v", got)
	}
}

// GAS が記録を見送った (refuel_deferred) ときは給油イベントを残し、次の送信で送り直す。
// ロックが取れずに見送ったものを「済み」にすると、給油が記録されずに失われる。
func TestRefuelFlow_DeferredByGASIsResent(t *testing.T) {
	var mu sync.Mutex
	deferred, refuels := true, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var body struct {
			Data map[string]any `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.Data["refuel_detected"] == true {
			if deferred {
				_, _ = w.Write([]byte(`{"refuel_deferred":true}`))
				return
			}
			refuels++
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	app := newRefuelTestApp(t, srv.URL)
	app.tracker.SetDistance(300)
	detectRefuel(t, app)
	app.resetTripOnRefuel()

	app.sendMaintenanceStatus(context.Background()) // 見送られる
	if app.refuel.Event() == nil {
		t.Fatal("見送られた給油イベントを消した (給油が失われる)")
	}
	if got := app.refuelUISnapshot(0); got == nil || got.Stage != refuelStageDetected {
		t.Errorf("見送られたのに記録済みに進んだ: %+v", got)
	}

	mu.Lock()
	deferred = false
	mu.Unlock()
	app.sendMaintenanceStatus(context.Background()) // 送り直して記録される

	if refuels != 1 {
		t.Errorf("給油を %d 回記録した, want 1", refuels)
	}
	if app.refuel.Event() != nil {
		t.Error("記録された給油イベントが残っている")
	}
	if got := app.refuelUISnapshot(0); got == nil || got.Stage != refuelStageTripReset {
		t.Errorf("記録の後に完了まで進んでいない: %+v", got)
	}
}
