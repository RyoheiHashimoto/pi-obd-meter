package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
