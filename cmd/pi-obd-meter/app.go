package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/hashimoto/pi-obd-meter/internal/maintenance"
	"github.com/hashimoto/pi-obd-meter/internal/sender"
	"github.com/hashimoto/pi-obd-meter/internal/trip"
)

// oilStatusPayload はGASに送信するオイル状態
type oilStatusPayload struct {
	OilCurrentKm    float64   `json:"oil_current_km"`
	OilRemainingKm  float64   `json:"oil_remaining_km"`
	OilAlert        string    `json:"oil_alert"`
	TotalKm         float64   `json:"total_km"`
	TripKm          float64   `json:"trip_km"`
	OdometerApplied bool      `json:"odometer_applied,omitempty"`
	SentAt          time.Time `json:"sent_at"`
}

// gasMaintenanceResponse はGASからのメンテナンスレスポンス
type gasMaintenanceResponse struct {
	PendingResets      []string `json:"pending_resets"`
	OdometerCorrection *float64 `json:"odometer_correction"`
	TripCorrectionKm   *float64 `json:"trip_correction_km"`
	TripReset          bool     `json:"trip_reset"`
}

// App はアプリケーション全体の状態を管理する
type App struct {
	cfg      Config
	client   *sender.Client
	maintMgr *maintenance.Manager
	tracker  *trip.Tracker

	dataMu     sync.RWMutex
	latestData RealtimeData

	notificationMu  sync.RWMutex
	notification    string
	notificationExp time.Time

	totalKmMu    sync.Mutex
	totalKmAccum float64
	odoApplied   bool

	startedAt time.Time
}

// newApp はアプリケーション状態を初期化する
func newApp(cfg Config) *App {
	oilCfg := maintenance.OilConfig{
		IntervalKm: cfg.OilChange.IntervalKm,
		WarningKm:  cfg.OilChange.WarningKm,
		DangerKm:   cfg.OilChange.DangerKm,
	}
	if oilCfg.IntervalKm <= 0 {
		oilCfg = maintenance.DefaultOilConfig()
	}

	app := &App{
		cfg:       cfg,
		client:    sender.NewClient(cfg.WebhookURL),
		maintMgr:  maintenance.NewManager(cfg.MaintenancePath, oilCfg),
		tracker:   trip.NewTracker(trip.TrackerConfig{}),
		startedAt: time.Now(),
	}

	// 累計走行距離の初期化
	app.totalKmAccum = app.maintMgr.TotalKm()
	if app.totalKmAccum == 0 && cfg.InitialOdometerKm > 0 {
		app.totalKmAccum = cfg.InitialOdometerKm
		app.maintMgr.UpdateTotalKm(app.totalKmAccum)
		slog.Info("初期ODO設定", "km", app.totalKmAccum)
	} else {
		slog.Info("累計走行距離復元済み", "km", app.totalKmAccum)
	}

	return app
}

// getNotification は有効期限内の通知を返す（期限切れなら空文字列）
func (app *App) getNotification() string {
	app.notificationMu.RLock()
	defer app.notificationMu.RUnlock()
	if time.Now().After(app.notificationExp) {
		return ""
	}
	return app.notification
}

// addDistance は走行距離を累計に加算する
func (app *App) addDistance(deltaKm float64) {
	app.totalKmMu.Lock()
	app.totalKmAccum += deltaKm
	totalKm := app.totalKmAccum
	app.totalKmMu.Unlock()
	app.maintMgr.UpdateTotalKm(totalKm)
}

// updateRealtimeData はリアルタイムデータをスレッドセーフに更新する
func (app *App) updateRealtimeData(data RealtimeData) {
	app.dataMu.Lock()
	app.latestData = data
	app.dataMu.Unlock()
}

// getRealtimeData はリアルタイムデータのコピーを返す
func (app *App) getRealtimeData() RealtimeData {
	app.dataMu.RLock()
	defer app.dataMu.RUnlock()
	return app.latestData
}

// sendMaintenanceStatus はメンテナンス状態をGASに送信し、レスポンスを処理する。
func (app *App) sendMaintenanceStatus(ctx context.Context) {
	const maxRetries = 3

	for attempt := 0; attempt < maxRetries; attempt++ {
		oil := app.maintMgr.OilStatus()

		app.totalKmMu.Lock()
		odoApplied := app.odoApplied
		app.totalKmMu.Unlock()

		payload := oilStatusPayload{
			OilCurrentKm:    oil.CurrentKm,
			OilRemainingKm:  oil.RemainingKm,
			OilAlert:        string(oil.Alert),
			TotalKm:         app.maintMgr.TotalKm(),
			TripKm:          app.tracker.DistanceKm(),
			OdometerApplied: odoApplied,
			SentAt:          time.Now(),
		}

		respBody, err := app.client.SendWithResponse(ctx, "maintenance", payload)
		if err != nil {
			return
		}
		slog.Info("メンテナンス状態送信完了")

		if len(respBody) == 0 {
			return
		}

		var gasResp gasMaintenanceResponse
		if err := json.Unmarshal(respBody, &gasResp); err != nil {
			slog.Warn("GASレスポンスJSON解析失敗", "error", err)
			return
		}

		// pending_resets 処理（OILリセット）
		for _, id := range gasResp.PendingResets {
			if id == "oil_change" {
				app.maintMgr.ResetOil()
				slog.Info("オイル交換リセット")
			}
		}

		// ODO補正処理
		if gasResp.OdometerCorrection != nil && *gasResp.OdometerCorrection > 0 {
			newOdo := *gasResp.OdometerCorrection
			app.totalKmMu.Lock()
			app.totalKmAccum = newOdo
			app.odoApplied = true
			app.totalKmMu.Unlock()
			app.maintMgr.UpdateTotalKm(newOdo)
			slog.Info("ODO補正適用", "odometer_km", newOdo)
			continue
		}

		app.totalKmMu.Lock()
		if app.odoApplied {
			app.odoApplied = false
		}
		app.totalKmMu.Unlock()

		// トリップ補正処理
		if gasResp.TripCorrectionKm != nil {
			km := *gasResp.TripCorrectionKm
			app.tracker.SetDistance(km)
			slog.Info("トリップ補正", "km", km)
		} else if gasResp.TripReset {
			app.tracker.ManualReset()
			slog.Info("トリップリセット", "reason", "給油記録")
		}

		return
	}
}

// initializeFromGAS はWiFi接続を待機し、GASから状態復元とメンテナンス初回送信を行う
func (app *App) initializeFromGAS(ctx context.Context) {
	for i := 0; i < 30; i++ {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if checkWiFi() {
			app.restoreFromGAS(ctx)
			app.sendMaintenanceStatus(ctx)
			return
		}
		time.Sleep(2 * time.Second)
	}
	slog.Warn("WiFi接続待ちタイムアウト、メンテナンス初回送信スキップ")
}

// restoreFromGAS はGASから累計走行距離とトリップ距離を復元する（起動時用）
func (app *App) restoreFromGAS(ctx context.Context) {
	restored, err := app.client.RestoreState(ctx)
	if err != nil || restored.TotalKm <= 0 {
		return
	}

	app.totalKmMu.Lock()
	if app.totalKmAccum < restored.TotalKm {
		app.totalKmAccum = restored.TotalKm
		app.totalKmMu.Unlock()
		app.maintMgr.UpdateTotalKm(restored.TotalKm)
		slog.Info("GASからODO復元", "total_km", restored.TotalKm)
	} else {
		app.totalKmMu.Unlock()
	}

	// トリップ距離をGASの給油記録と同期
	if restored.LastRefuelKm > 0 && restored.TotalKm > restored.LastRefuelKm {
		tripKm := restored.TotalKm - restored.LastRefuelKm
		localTrip := app.tracker.DistanceKm()
		if localTrip == 0 {
			slog.Info("ローカルトリップ0、GAS復元スキップ", "gas_trip_km", tripKm)
		} else if tripKm > localTrip {
			app.tracker.SetDistance(tripKm)
			slog.Info("GASからトリップ復元", "trip_km", tripKm, "last_refuel_km", restored.LastRefuelKm)
		}
	}
}
