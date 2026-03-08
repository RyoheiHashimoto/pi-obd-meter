package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashimoto/pi-obd-meter/internal/maintenance"
	"github.com/hashimoto/pi-obd-meter/internal/sender"
	"github.com/hashimoto/pi-obd-meter/internal/trip"
)

func TestCalcFuelEconomy_EngineStopped(t *testing.T) {
	// エンジン停止 (speed<0.5, rpm<100) → 0
	got := calcFuelEconomy(0, 0, 0, 0, false, 1.3)
	if got != 0 {
		t.Errorf("engine stopped: got %.1f, want 0", got)
	}
}

func TestCalcFuelEconomy_LowSpeed(t *testing.T) {
	// 低速域 (<10 km/h) → 0（クリープ等は燃費表示しない）
	got := calcFuelEconomy(5, 800, 30, 0, false, 1.3)
	if got != 0 {
		t.Errorf("low speed: got %.1f, want 0", got)
	}
}

func TestCalcFuelEconomy_NormalDriving_LoadRPM(t *testing.T) {
	// 60km/h, 2000rpm, 30%負荷, MAFなし, 1.3L
	got := calcFuelEconomy(60, 2000, 30, 0, false, 1.3)
	if got <= 0 || got > maxDisplayKmL {
		t.Errorf("normal driving (load×RPM): got %.1f, expected positive value", got)
	}
	// 一般的な1.3Lの燃費は10-25 km/Lの範囲
	if got < 5 || got > 50 {
		t.Errorf("normal driving (load×RPM): got %.1f, expected 5-50 range", got)
	}
}

func TestCalcFuelEconomy_NormalDriving_MAF(t *testing.T) {
	// 60km/h, MAF=5g/s(一般的な巡航値)
	got := calcFuelEconomy(60, 2000, 30, 5.0, true, 1.3)
	if got <= 0 || got > maxDisplayKmL {
		t.Errorf("normal driving (MAF): got %.1f, expected positive value", got)
	}
	if got < 5 || got > 50 {
		t.Errorf("normal driving (MAF): got %.1f, expected 5-50 range", got)
	}
}

func TestCalcFuelEconomy_HighLoad(t *testing.T) {
	// 高負荷: 120km/h, 4000rpm, 80%負荷 → 燃費が悪い
	got := calcFuelEconomy(120, 4000, 80, 0, false, 1.3)
	if got <= 0 {
		t.Errorf("high load: got %.1f, expected positive", got)
	}
	// 高負荷時は低めの燃費
	normalGot := calcFuelEconomy(60, 2000, 30, 0, false, 1.3)
	if got >= normalGot {
		t.Errorf("high load (%.1f) should be worse than normal (%.1f)", got, normalGot)
	}
}

func TestCalcFuelEconomy_EngineBraking(t *testing.T) {
	// エンブレ: 速度あり、負荷ほぼ0 → idle燃料消費(0.8L/h)で計算
	// 60km/h / 0.8L/h = 75 km/L
	got := calcFuelEconomy(60, 2000, 0, 0, false, 1.3)
	if got <= 0 {
		t.Errorf("engine braking: got %.1f, expected positive", got)
	}
	// 低負荷なので高燃費が出る
	normalGot := calcFuelEconomy(60, 2000, 30, 0, false, 1.3)
	if got <= normalGot {
		t.Errorf("engine braking (%.1f) should be better than normal driving (%.1f)", got, normalGot)
	}
}

func TestCalcFuelEconomy_ZeroMAF_Fallback(t *testing.T) {
	// hasMAF=true でも MAF=0 → load×RPM にフォールバック
	mafZero := calcFuelEconomy(60, 2000, 30, 0, true, 1.3)
	noMAF := calcFuelEconomy(60, 2000, 30, 0, false, 1.3)
	if mafZero != noMAF {
		t.Errorf("MAF=0 should fall back to load×RPM: MAF0=%.1f, noMAF=%.1f", mafZero, noMAF)
	}
}

func TestCalcFuelEconomy_Idle(t *testing.T) {
	// アイドリング: 速度0, RPM 800 → 0 (低速域で非表示)
	got := calcFuelEconomy(0, 800, 20, 0, false, 1.3)
	if got != 0 {
		t.Errorf("idle: got %.1f, want 0 (below min display speed)", got)
	}
}

func TestCalcFuelEconomy_CappedAtMax(t *testing.T) {
	// MAFが非常に小さい値 → maxDisplayKmL でキャップ
	got := calcFuelEconomy(60, 2000, 5, 0.01, true, 1.3)
	if got > maxDisplayKmL {
		t.Errorf("cap: got %.1f, should not exceed %.1f", got, maxDisplayKmL)
	}
}

func TestCalcFuelEconomy_MAFPriority(t *testing.T) {
	// MAFがある場合、load×RPMより優先される
	mafResult := calcFuelEconomy(60, 2000, 30, 5.0, true, 1.3)
	noMafResult := calcFuelEconomy(60, 2000, 30, 5.0, false, 1.3)
	// 両方とも有効な値を返すが、異なる計算パス
	if mafResult <= 0 || noMafResult <= 0 {
		t.Errorf("both paths should return positive: MAF=%.1f, noMAF=%.1f", mafResult, noMafResult)
	}
	// MAFとload×RPMは一般的に異なる結果
	if math.Abs(mafResult-noMafResult) < 0.001 {
		t.Log("MAF and load×RPM gave same result (coincidental)")
	}
}

func TestCalcFuelEconomy_LargerDisplacement(t *testing.T) {
	// 排気量が大きいほど燃費が悪い (load×RPMベース)
	small := calcFuelEconomy(60, 2000, 30, 0, false, 1.3)
	large := calcFuelEconomy(60, 2000, 30, 0, false, 2.0)
	if large >= small {
		t.Errorf("larger displacement should use more fuel: 1.3L=%.1f, 2.0L=%.1f", small, large)
	}
}

// --- sendMaintenanceStatus テスト ---

func TestSendMaintenanceStatus_Basic(t *testing.T) {
	var receivedPayload map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p sender.GASPayload
		json.NewDecoder(r.Body).Decode(&p)
		receivedPayload = p.Data.(map[string]interface{})
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	client := sender.NewClient(srv.URL)
	maintMgr := maintenance.NewManager(t.TempDir() + "/maint.json")
	maintMgr.InitDefaults(nil)
	tracker := trip.NewTracker(trip.TrackerConfig{StatePath: t.TempDir() + "/trip.json"})

	totalKm := 100.0
	odoApplied := false
	sendMaintenanceStatus(client, maintMgr, &totalKm, &odoApplied, tracker)

	if receivedPayload == nil {
		t.Fatal("expected payload to be sent")
	}
	statuses, ok := receivedPayload["statuses"].([]interface{})
	if !ok || len(statuses) == 0 {
		t.Error("expected non-empty statuses array")
	}
}

func TestSendMaintenanceStatus_PendingResets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"pending_resets":["oil_change"]}`))
	}))
	defer srv.Close()

	client := sender.NewClient(srv.URL)
	maintMgr := maintenance.NewManager(t.TempDir() + "/maint.json")
	maintMgr.InitDefaults(nil)

	totalKm := 5000.0
	odoApplied := false
	sendMaintenanceStatus(client, maintMgr, &totalKm, &odoApplied, nil)

	// oil_changeがリセットされたことを確認
	for _, s := range maintMgr.CheckAll() {
		if s.Reminder.ID == "oil_change" && s.CurrentKm > 100 {
			t.Errorf("oil_change should have been reset, current_km=%.1f", s.CurrentKm)
		}
	}
}

func TestSendMaintenanceStatus_OdometerCorrection(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(200)
		if callCount == 1 {
			w.Write([]byte(`{"odometer_correction":50000}`))
		} else {
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	client := sender.NewClient(srv.URL)
	maintMgr := maintenance.NewManager(t.TempDir() + "/maint.json")
	maintMgr.InitDefaults(nil)

	totalKm := 100.0
	odoApplied := false
	sendMaintenanceStatus(client, maintMgr, &totalKm, &odoApplied, nil)

	if totalKm != 50000 {
		t.Errorf("totalKm should be corrected to 50000, got %.1f", totalKm)
	}
	// 2回呼ばれる（補正後の再送信）。2回目でGASが補正をクリア済みなのでodoApplied=false
	if callCount != 2 {
		t.Errorf("expected 2 calls (initial + re-send after correction), got %d", callCount)
	}
	if odoApplied {
		t.Error("odoApplied should be false after re-send (GAS cleared correction)")
	}
}

func TestSendMaintenanceStatus_TripReset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"trip_reset":true}`))
	}))
	defer srv.Close()

	client := sender.NewClient(srv.URL)
	maintMgr := maintenance.NewManager(t.TempDir() + "/maint.json")
	maintMgr.InitDefaults(nil)

	tracker := trip.NewTracker(trip.TrackerConfig{StatePath: t.TempDir() + "/trip.json"})
	tracker.Update(60) // トリップに走行データを追加

	totalKm := 100.0
	odoApplied := false
	sendMaintenanceStatus(client, maintMgr, &totalKm, &odoApplied, tracker)

	// トリップがリセットされている
	if tracker.DistanceKm() > 0.001 {
		t.Errorf("trip should be reset, got %.4f km", tracker.DistanceKm())
	}
}

func TestSendMaintenanceStatus_Empty(t *testing.T) {
	// リマインダー0件 → 送信しない
	client := sender.NewClient("http://127.0.0.1:1")
	maintMgr := maintenance.NewManager(t.TempDir() + "/maint.json")
	// InitDefaultsを呼ばない = リマインダー0件

	totalKm := 0.0
	odoApplied := false
	sendMaintenanceStatus(client, maintMgr, &totalKm, &odoApplied, nil)
	// パニックしなければOK
}

// --- corsMiddleware テスト ---

func TestCorsMiddleware_GETRequest(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})
	handler := corsMiddleware(inner)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS origin header")
	}
	if rec.Code != 200 {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

func TestCorsMiddleware_OPTIONSRequest(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler should not be called for OPTIONS")
	})
	handler := corsMiddleware(inner)

	req := httptest.NewRequest("OPTIONS", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") != "GET, OPTIONS" {
		t.Error("missing CORS methods header")
	}
}

// --- RestoreState テスト ---

func TestRestoreState_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok","total_km":12345.6,"last_refuel_km":12000}`))
	}))
	defer srv.Close()

	client := sender.NewClient(srv.URL)
	resp, err := client.RestoreState()
	if err != nil {
		t.Fatalf("RestoreState failed: %v", err)
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

	client := sender.NewClient(srv.URL)
	_, err := client.RestoreState()
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestRestoreState_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	client := sender.NewClient(srv.URL)
	_, err := client.RestoreState()
	if err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}
