package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashimoto/pi-obd-meter/internal/maintenance"
	"github.com/hashimoto/pi-obd-meter/internal/sender"
	"github.com/hashimoto/pi-obd-meter/internal/trip"
)

func TestCalcFuelEconomy_EngineStopped(t *testing.T) {
	// エンジン停止 (speed<0.5, rpm<100) → 0
	got, rateLH := calcFuelEconomy(0, 0, 0, 0, false, 1.3)
	if got != 0 {
		t.Errorf("engine stopped: got %.1f, want 0", got)
	}
	if rateLH != 0 {
		t.Errorf("engine stopped: rateLH got %.2f, want 0", rateLH)
	}
}

func TestCalcFuelEconomy_LowSpeed(t *testing.T) {
	// 低速域 (<10 km/h) → kmL=0 だが fuelRateLH は返る
	got, rateLH := calcFuelEconomy(5, 800, 30, 0, false, 1.3)
	if got != 0 {
		t.Errorf("low speed: got %.1f, want 0", got)
	}
	if rateLH <= 0 {
		t.Errorf("low speed: rateLH should be positive, got %.2f", rateLH)
	}
}

func TestCalcFuelEconomy_NormalDriving_LoadRPM(t *testing.T) {
	// 60km/h, 2000rpm, 30%負荷, MAFなし, 1.3L
	got, rateLH := calcFuelEconomy(60, 2000, 30, 0, false, 1.3)
	if got <= 0 || got > maxDisplayKmL {
		t.Errorf("normal driving (load×RPM): got %.1f, expected positive value", got)
	}
	// 一般的な1.3Lの燃費は10-25 km/Lの範囲
	if got < 5 || got > 50 {
		t.Errorf("normal driving (load×RPM): got %.1f, expected 5-50 range", got)
	}
	if rateLH <= 0 {
		t.Errorf("normal driving: rateLH should be positive, got %.2f", rateLH)
	}
}

func TestCalcFuelEconomy_NormalDriving_MAF(t *testing.T) {
	// 60km/h, MAF=5g/s(一般的な巡航値)
	got, _ := calcFuelEconomy(60, 2000, 30, 5.0, true, 1.3)
	if got <= 0 || got > maxDisplayKmL {
		t.Errorf("normal driving (MAF): got %.1f, expected positive value", got)
	}
	if got < 5 || got > 50 {
		t.Errorf("normal driving (MAF): got %.1f, expected 5-50 range", got)
	}
}

func TestCalcFuelEconomy_HighLoad(t *testing.T) {
	// 高負荷: 120km/h, 4000rpm, 80%負荷 → 燃費が悪い
	got, _ := calcFuelEconomy(120, 4000, 80, 0, false, 1.3)
	if got <= 0 {
		t.Errorf("high load: got %.1f, expected positive", got)
	}
	// 高負荷時は低めの燃費
	normalGot, _ := calcFuelEconomy(60, 2000, 30, 0, false, 1.3)
	if got >= normalGot {
		t.Errorf("high load (%.1f) should be worse than normal (%.1f)", got, normalGot)
	}
}

func TestCalcFuelEconomy_EngineBraking(t *testing.T) {
	// エンブレ: 速度あり、負荷ほぼ0 → idle燃料消費(0.8L/h)で計算
	// 負荷0 → fuelRate = idleFuelRateLH(0.8) → 0.01以上なので通常計算
	got, rateLH := calcFuelEconomy(60, 2000, 0, 0, false, 1.3)
	if got <= 0 {
		t.Errorf("engine braking: got %.1f, expected positive", got)
	}
	if rateLH != idleFuelRateLH {
		t.Errorf("engine braking: rateLH got %.2f, want %.2f", rateLH, idleFuelRateLH)
	}
	// 低負荷なので高燃費が出る
	normalGot, _ := calcFuelEconomy(60, 2000, 30, 0, false, 1.3)
	if got <= normalGot {
		t.Errorf("engine braking (%.1f) should be better than normal driving (%.1f)", got, normalGot)
	}
}

func TestCalcFuelEconomy_FuelCut(t *testing.T) {
	// 燃料カット（MAF=ほぼ0）→ -1 (特別表示)
	got, _ := calcFuelEconomy(60, 2000, 30, 0.001, true, 1.3)
	if got != -1 {
		t.Errorf("fuel cut: got %.1f, want -1", got)
	}
}

func TestCalcFuelEconomy_ZeroMAF_Fallback(t *testing.T) {
	// hasMAF=true でも MAF=0 → load×RPM にフォールバック
	mafZero, _ := calcFuelEconomy(60, 2000, 30, 0, true, 1.3)
	noMAF, _ := calcFuelEconomy(60, 2000, 30, 0, false, 1.3)
	if mafZero != noMAF {
		t.Errorf("MAF=0 should fall back to load×RPM: MAF0=%.1f, noMAF=%.1f", mafZero, noMAF)
	}
}

func TestCalcFuelEconomy_Idle(t *testing.T) {
	// アイドリング: 速度0, RPM 800 → 0 (低速域で非表示)
	got, _ := calcFuelEconomy(0, 800, 20, 0, false, 1.3)
	if got != 0 {
		t.Errorf("idle: got %.1f, want 0 (below min display speed)", got)
	}
}

func TestCalcFuelEconomy_CappedAtMax(t *testing.T) {
	// 高速 + 低燃料消費 → maxDisplayKmL でキャップ
	got, _ := calcFuelEconomy(100, 1000, 5, 0, false, 1.3)
	if got > maxDisplayKmL {
		t.Errorf("cap: got %.1f, should not exceed %.1f", got, maxDisplayKmL)
	}
	if got != maxDisplayKmL {
		t.Logf("cap test: got %.1f (may not hit cap at this condition)", got)
	}
}

func TestCalcFuelEconomy_MAFPriority(t *testing.T) {
	// MAFがある場合、load×RPMより優先される
	mafResult, _ := calcFuelEconomy(60, 2000, 30, 5.0, true, 1.3)
	noMafResult, _ := calcFuelEconomy(60, 2000, 30, 5.0, false, 1.3)
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
	small, smallRate := calcFuelEconomy(60, 2000, 30, 0, false, 1.3)
	large, largeRate := calcFuelEconomy(60, 2000, 30, 0, false, 2.0)
	if large >= small {
		t.Errorf("larger displacement should use more fuel: 1.3L=%.1f, 2.0L=%.1f", small, large)
	}
	if largeRate <= smallRate {
		t.Errorf("larger displacement should have higher fuel rate: 1.3L=%.2f, 2.0L=%.2f L/h", smallRate, largeRate)
	}
}

func TestCalcFuelEconomy_LowSpeedFuelRate(t *testing.T) {
	// 低速域でも fuelRateLH が排気量に比例することを確認
	_, rate13 := calcFuelEconomy(15, 1000, 20, 0, false, 1.3)
	_, rate20 := calcFuelEconomy(15, 1000, 20, 0, false, 2.0)
	if rate13 <= 0 {
		t.Errorf("1.3L at low speed: rateLH should be positive, got %.2f", rate13)
	}
	if rate20 <= rate13 {
		t.Errorf("2.0L should consume more than 1.3L: 1.3L=%.2f, 2.0L=%.2f L/h", rate13, rate20)
	}
	// 1.3L で穏やかな低速走行は ECO 閾値 (1.5×排気量=2.0 L/h) 以下であるべき
	ecoGreenThreshold := 1.5 * 1.3
	if rate13 >= ecoGreenThreshold {
		t.Errorf("gentle low speed (1.3L): rateLH=%.2f should be below eco green threshold %.1f", rate13, ecoGreenThreshold)
	}
}

// --- sendMaintenanceStatus テスト ---

// newTestApp はテスト用の App を作成するヘルパー
func newTestApp(t *testing.T, serverURL string) *App {
	t.Helper()
	return &App{
		cfg:      Config{},
		client:   sender.NewClient(serverURL),
		maintMgr: maintenance.NewManager(filepath.Join(t.TempDir(), "maint.json")),
		tracker:  trip.NewTracker(trip.TrackerConfig{StatePath: filepath.Join(t.TempDir(), "trip.json")}),
	}
}

func TestSendMaintenanceStatus_Basic(t *testing.T) {
	var receivedPayload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p sender.GASPayload
		_ = json.NewDecoder(r.Body).Decode(&p)
		receivedPayload = p.Data.(map[string]any)
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	app := newTestApp(t, srv.URL)
	app.maintMgr.InitDefaults(nil)
	app.totalKmAccum = 100.0

	app.sendMaintenanceStatus(context.Background())

	if receivedPayload == nil {
		t.Fatal("expected payload to be sent")
	}
	statuses, ok := receivedPayload["statuses"].([]any)
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

	app := newTestApp(t, srv.URL)
	app.maintMgr.InitDefaults(nil)
	app.totalKmAccum = 5000.0

	app.sendMaintenanceStatus(context.Background())

	// oil_changeがリセットされたことを確認
	for _, s := range app.maintMgr.CheckAll() {
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

	app := newTestApp(t, srv.URL)
	app.maintMgr.InitDefaults(nil)
	app.totalKmAccum = 100.0

	app.sendMaintenanceStatus(context.Background())

	if app.totalKmAccum != 50000 {
		t.Errorf("totalKm should be corrected to 50000, got %.1f", app.totalKmAccum)
	}
	// 2回呼ばれる（補正後の再送信）。2回目でGASが補正をクリア済みなのでodoApplied=false
	if callCount != 2 {
		t.Errorf("expected 2 calls (initial + re-send after correction), got %d", callCount)
	}
	if app.odoApplied {
		t.Error("odoApplied should be false after re-send (GAS cleared correction)")
	}
}

func TestSendMaintenanceStatus_TripReset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"trip_reset":true}`))
	}))
	defer srv.Close()

	app := newTestApp(t, srv.URL)
	app.maintMgr.InitDefaults(nil)
	app.tracker.Update(60, 0) // トリップに走行データを追加
	app.totalKmAccum = 100.0

	app.sendMaintenanceStatus(context.Background())

	// トリップがリセットされている
	if app.tracker.DistanceKm() > 0.001 {
		t.Errorf("trip should be reset, got %.4f km", app.tracker.DistanceKm())
	}
}

func TestSendMaintenanceStatus_Empty(t *testing.T) {
	// リマインダー0件 → 送信しない
	app := newTestApp(t, "http://127.0.0.1:1")
	// InitDefaultsを呼ばない = リマインダー0件

	app.sendMaintenanceStatus(context.Background())
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
	resp, err := client.RestoreState(context.Background())
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
	_, err := client.RestoreState(context.Background())
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
	_, err := client.RestoreState(context.Background())
	if err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}

// --- loadConfig テスト ---

func TestLoadConfig_FileNotFound(t *testing.T) {
	cfg := loadConfig("/nonexistent/path/config.json")
	// デフォルト値が返る
	if cfg.SerialPort != "/dev/rfcomm0" {
		t.Errorf("SerialPort: got %q, want /dev/rfcomm0", cfg.SerialPort)
	}
	if cfg.MaxSpeedKmh != 180 {
		t.Errorf("MaxSpeedKmh: got %d, want 180", cfg.MaxSpeedKmh)
	}
	if cfg.LocalAPIPort != 9090 {
		t.Errorf("LocalAPIPort: got %d, want 9090", cfg.LocalAPIPort)
	}
	if cfg.EngineDisplacementL != 1.3 {
		t.Errorf("EngineDisplacementL: got %.1f, want 1.3", cfg.EngineDisplacementL)
	}
}

func TestLoadConfig_ValidJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	content := `{
		"serial_port": "/dev/ttyUSB0",
		"max_speed_kmh": 200,
		"local_api_port": 8080,
		"engine_displacement_l": 2.0,
		"webhook_url": "https://example.com/webhook"
	}`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := loadConfig(cfgPath)
	if cfg.SerialPort != "/dev/ttyUSB0" {
		t.Errorf("SerialPort: got %q, want /dev/ttyUSB0", cfg.SerialPort)
	}
	if cfg.MaxSpeedKmh != 200 {
		t.Errorf("MaxSpeedKmh: got %d, want 200", cfg.MaxSpeedKmh)
	}
	if cfg.LocalAPIPort != 8080 {
		t.Errorf("LocalAPIPort: got %d, want 8080", cfg.LocalAPIPort)
	}
	if cfg.EngineDisplacementL != 2.0 {
		t.Errorf("EngineDisplacementL: got %.1f, want 2.0", cfg.EngineDisplacementL)
	}
	if cfg.WebhookURL != "https://example.com/webhook" {
		t.Errorf("WebhookURL: got %q", cfg.WebhookURL)
	}
}

func TestLoadConfig_PartialJSON(t *testing.T) {
	// 一部のフィールドのみ → 残りはデフォルト
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"max_speed_kmh": 260}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := loadConfig(cfgPath)
	if cfg.MaxSpeedKmh != 260 {
		t.Errorf("MaxSpeedKmh: got %d, want 260", cfg.MaxSpeedKmh)
	}
	// デフォルトが維持される
	if cfg.SerialPort != "/dev/rfcomm0" {
		t.Errorf("SerialPort should keep default, got %q", cfg.SerialPort)
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{invalid json}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := loadConfig(cfgPath)
	// パース失敗 → デフォルト値
	if cfg.MaxSpeedKmh != 180 {
		t.Errorf("MaxSpeedKmh: got %d, want 180 (default)", cfg.MaxSpeedKmh)
	}
}

func TestLoadConfig_WithMaintenanceReminders(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	content := `{
		"maintenance_reminders": [
			{"id": "custom_oil", "name": "Custom Oil", "type": "distance", "interval_km": 5000, "warning_pct": 0.9}
		]
	}`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := loadConfig(cfgPath)
	if len(cfg.MaintenanceReminders) != 1 {
		t.Fatalf("MaintenanceReminders: got %d, want 1", len(cfg.MaintenanceReminders))
	}
	if cfg.MaintenanceReminders[0].ID != "custom_oil" {
		t.Errorf("reminder ID: got %q, want custom_oil", cfg.MaintenanceReminders[0].ID)
	}
}

// --- getNotification テスト ---

func TestGetNotification_Empty(t *testing.T) {
	app := &App{}
	got := app.getNotification()
	if got != "" {
		t.Errorf("initial notification: got %q, want empty", got)
	}
}

func TestGetNotification_Active(t *testing.T) {
	app := &App{
		notification:    "テスト通知",
		notificationExp: time.Now().Add(10 * time.Second),
	}
	got := app.getNotification()
	if got != "テスト通知" {
		t.Errorf("active notification: got %q, want テスト通知", got)
	}
}

func TestGetNotification_Expired(t *testing.T) {
	app := &App{
		notification:    "期限切れ",
		notificationExp: time.Now().Add(-1 * time.Second),
	}
	got := app.getNotification()
	if got != "" {
		t.Errorf("expired notification: got %q, want empty", got)
	}
}
