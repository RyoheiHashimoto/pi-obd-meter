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
	"time"

	"github.com/hashimoto/pi-obd-meter/web"
)

// configResponse は /api/config のレスポンス
type configResponse struct {
	MaxSpeedKmh     int     `json:"max_speed_kmh"`
	Version         string  `json:"version"`
	EcoLHGreen      float64 `json:"eco_lh_green"`
	EcoLHRed        float64 `json:"eco_lh_red"`
	ThrottleIdlePct float64 `json:"throttle_idle_pct"`
	EcoKmplGreen    float64 `json:"eco_kmpl_green"`
	EcoKmplOrange   float64 `json:"eco_kmpl_orange"`
	TripWarnKm      float64 `json:"trip_warn_km"`
	TripDangerKm    float64 `json:"trip_danger_km"`
}

// healthResponse は /api/health のレスポンス
type healthResponse struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	UptimeSec     int    `json:"uptime_sec"`
	OBDConnected  bool   `json:"obd_connected"`
	WiFiConnected bool   `json:"wifi_connected"`
	PendingCount  int    `json:"pending_count"`
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
		subFS, _ := fs.Sub(web.StaticFS, "static")
		webFS = http.FS(subFS)
		slog.Info("Web UI: 埋め込みファイルから配信")
	}
	mux.Handle("GET /", http.FileServer(webFS))

	// --- 設定API（meter.htmlがmax_speed_kmhを取得する） ---
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		d := app.cfg.EngineDisplacementL
		ecoKmplGreen := math.Round(20/d*10) / 10
		ecoKmplOrange := math.Round(13/d*10) / 10
		estRange := app.cfg.FuelTankL * ecoKmplGreen
		json.NewEncoder(w).Encode(configResponse{
			MaxSpeedKmh:     app.cfg.MaxSpeedKmh,
			Version:         version,
			EcoLHGreen:      1.5 * d,
			EcoLHRed:        3.0 * d,
			ThrottleIdlePct: app.cfg.ThrottleIdlePct,
			EcoKmplGreen:    ecoKmplGreen,
			EcoKmplOrange:   ecoKmplOrange,
			TripWarnKm:      math.Round(estRange * 0.5),
			TripDangerKm:    math.Round(estRange * 0.85),
		})
	})

	// --- リアルタイムAPI（LCD用、200ms間隔でポーリングされる） ---
	mux.HandleFunc("GET /api/realtime", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(app.getRealtimeData())
	})

	// --- ヘルスチェックAPI ---
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		d := app.getRealtimeData()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(healthResponse{
			Status:        "ok",
			Version:       version,
			UptimeSec:     int(time.Since(app.startedAt).Seconds()),
			OBDConnected:  d.OBDConnected,
			WiFiConnected: d.WiFiConnected,
			PendingCount:  d.PendingCount,
		})
	})

	// --- メンテナンスAPI（メーター画面のアラートバー用） ---
	mux.HandleFunc("GET /api/maintenance", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(app.maintMgr.CheckAll())
	})

	// --- キオスク停止API（タッチパネルから Chromium を終了する） ---
	mux.HandleFunc("POST /api/kiosk/stop", func(w http.ResponseWriter, r *http.Request) {
		slog.Info("キオスク停止リクエスト受信")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "closing"})
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
