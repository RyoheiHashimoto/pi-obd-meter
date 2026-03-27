package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/hashimoto/pi-obd-meter/web"
)

// configResponse は /api/config のレスポンス
type configResponse struct {
	MaxSpeedKmh        int     `json:"max_speed_kmh"`
	Version            string  `json:"version"`
	EcoGradientMaxKmpl float64 `json:"eco_gradient_max_kmpl"`
	ThrottleIdlePct    float64 `json:"throttle_idle_pct"`
	ThrottleMaxPct     float64 `json:"throttle_max_pct"`
	TripWarnKm         float64 `json:"trip_warn_km"`
	TripDangerKm       float64 `json:"trip_danger_km"`
	MaxPS              float64 `json:"max_ps"`
	MaxTorqueKgfm      float64 `json:"max_torque_kgfm"`
	MaxTorqueRPM       int     `json:"max_torque_rpm"`
	MaxPSRPM           int     `json:"max_ps_rpm"`
	CoolantColdMax     int     `json:"coolant_cold_max"`
	CoolantNormalMax   int     `json:"coolant_normal_max"`
	CoolantWarningMax  int     `json:"coolant_warning_max"`
}

// healthResponse は /api/health のレスポンス
type healthResponse struct {
	Status        string  `json:"status"`
	Version       string  `json:"version"`
	UptimeSec     int     `json:"uptime_sec"`
	OBDConnected  bool    `json:"obd_connected"`
	WiFiConnected bool    `json:"wifi_connected"`
	PendingCount  int     `json:"pending_count"`
	MemAllocMB    float64 `json:"mem_alloc_mb"`
	MemSysMB      float64 `json:"mem_sys_mb"`
	NumGoroutine  int     `json:"num_goroutine"`
}

// writeJSON はJSONレスポンスを書き込む。エンコードエラー時はログに記録する。
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("JSONレスポンス書き込みエラー", "error", err)
	}
}

// corsMiddleware はCORSヘッダーを付与する（meter.htmlからのfetchリクエスト許可用）
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// startLocalAPI はローカルHTTPサーバーを起動する。
// meter.html の配信と、リアルタイムデータ・設定・メンテナンスのJSON APIを提供する。
// ctx がキャンセルされると graceful shutdown する。
func (app *App) startLocalAPI(ctx context.Context) {
	mux := http.NewServeMux()

	// --- Web UI配信 ---
	var webFS http.FileSystem
	if app.cfg.WebStaticDir != "" {
		webFS = http.Dir(app.cfg.WebStaticDir)
		slog.Info("Web UI: ファイルシステムから配信", "dir", app.cfg.WebStaticDir)
	} else {
		subFS, _ := fs.Sub(web.StaticFS, "static") //nolint:errcheck // 埋め込みFSなので失敗しない
		webFS = http.FS(subFS)
		slog.Info("Web UI: 埋め込みファイルから配信")
	}
	mux.Handle("GET /", http.FileServer(webFS))

	// --- 設定API（meter.htmlがmax_speed_kmhを取得する） ---
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		d := app.cfg.EngineDisplacementL
		ecoGradientMax := app.cfg.EcoGradientMaxKmpl
		if ecoGradientMax <= 0 {
			ecoGradientMax = math.Round(20/d*10) / 10
		}
		estRange := app.cfg.FuelTankL * ecoGradientMax
		tripWarnKm := math.Round(estRange * 0.5)
		tripDangerKm := math.Round(estRange * 0.85)
		if app.cfg.TripWarnKm > 0 {
			tripWarnKm = app.cfg.TripWarnKm
		}
		if app.cfg.TripDangerKm > 0 {
			tripDangerKm = app.cfg.TripDangerKm
		}
		coolantColdMax := app.cfg.CoolantTemp.ColdMax
		if coolantColdMax <= 0 {
			coolantColdMax = 60
		}
		coolantNormalMax := app.cfg.CoolantTemp.NormalMax
		if coolantNormalMax <= 0 {
			coolantNormalMax = 100
		}
		coolantWarningMax := app.cfg.CoolantTemp.WarningMax
		if coolantWarningMax <= 0 {
			coolantWarningMax = 104
		}
		writeJSON(w, configResponse{
			MaxSpeedKmh:        app.cfg.MaxSpeedKmh,
			Version:            version,
			EcoGradientMaxKmpl: ecoGradientMax,
			ThrottleIdlePct:    app.cfg.ThrottleIdlePct,
			ThrottleMaxPct:     app.cfg.ThrottleMaxPct,
			TripWarnKm:         tripWarnKm,
			TripDangerKm:       tripDangerKm,
			MaxPS:              app.cfg.MaxPS,
			MaxTorqueKgfm:      app.cfg.MaxTorqueKgfm,
			MaxTorqueRPM:       app.cfg.MaxTorqueRPM,
			MaxPSRPM:           app.cfg.MaxPSRPM,
			CoolantColdMax:     coolantColdMax,
			CoolantNormalMax:   coolantNormalMax,
			CoolantWarningMax:  coolantWarningMax,
		})
	})

	// --- リアルタイムAPI（LCD用、200ms間隔でポーリングされる） ---
	mux.HandleFunc("GET /api/realtime", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, app.getRealtimeData())
	})

	// --- ヘルスチェックAPI ---
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		d := app.getRealtimeData()
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		writeJSON(w, healthResponse{
			Status:        "ok",
			Version:       version,
			UptimeSec:     int(time.Since(app.startedAt).Seconds()),
			OBDConnected:  d.OBDConnected,
			WiFiConnected: d.WiFiConnected,
			PendingCount:  d.PendingCount,
			MemAllocMB:    float64(mem.Alloc) / 1024 / 1024,
			MemSysMB:      float64(mem.Sys) / 1024 / 1024,
			NumGoroutine:  runtime.NumGoroutine(),
		})
	})

	// --- オイル交換状態API ---
	mux.HandleFunc("GET /api/maintenance", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, app.maintMgr.OilStatus())
	})

	// --- キオスク停止API（タッチパネルから Chromium を終了する） ---
	mux.HandleFunc("POST /api/kiosk/stop", func(w http.ResponseWriter, r *http.Request) {
		slog.Info("キオスク停止リクエスト受信")
		writeJSON(w, map[string]string{"status": "closing"})
		go func() {
			time.Sleep(500 * time.Millisecond)
			if err := exec.Command("pkill", "chromium").Run(); err != nil {
				slog.Warn("Chromium停止失敗", "error", err)
			}
		}()
	})

	addr := fmt.Sprintf(":%d", app.cfg.LocalAPIPort)
	srv := &http.Server{
		Addr:    addr,
		Handler: corsMiddleware(mux),
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("HTTPサーバーシャットダウンエラー", "error", err)
		}
	}()

	slog.Info("ローカルAPI起動", "addr", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("HTTPサーバーエラー", "error", err)
	}
}
