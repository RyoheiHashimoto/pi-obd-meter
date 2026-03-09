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

// maintenanceStatusItem はGASに送信するメンテナンス項目
type maintenanceStatusItem struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Progress    float64 `json:"progress"`
	NeedsAlert  bool    `json:"needs_alert"`
	IsOverdue   bool    `json:"is_overdue"`
	RemainingKm float64 `json:"remaining_km,omitempty"`
	CurrentKm   float64 `json:"current_km,omitempty"`
	DaysLeft    int     `json:"days_left,omitempty"`
	DaysElapsed int     `json:"days_elapsed,omitempty"`
}

// maintenancePayload はGASに送信するメンテナンスペイロード
type maintenancePayload struct {
	Statuses        []maintenanceStatusItem `json:"statuses"`
	SentAt          time.Time               `json:"sent_at"`
	TotalKm         float64                 `json:"total_km"`
	OdometerApplied bool                    `json:"odometer_applied,omitempty"`
}

// gasMaintenanceResponse はGASからのメンテナンスレスポンス
type gasMaintenanceResponse struct {
	PendingResets      []string `json:"pending_resets"`
	OdometerCorrection *float64 `json:"odometer_correction"`
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
	app := &App{
		cfg:       cfg,
		client:    sender.NewClient(cfg.WebhookURL),
		maintMgr:  maintenance.NewManager(cfg.MaintenancePath),
		tracker:   trip.NewTracker(trip.TrackerConfig{}),
		startedAt: time.Now(),
	}

	app.maintMgr.InitDefaults(cfg.MaintenanceReminders)
	slog.Info("メンテナンスリマインダー初期化", "count", len(app.maintMgr.GetAll()))

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
// ODO補正時は再送信するが、再帰ではなくループで処理する。
func (app *App) sendMaintenanceStatus(ctx context.Context) {
	const maxRetries = 3

	for attempt := 0; attempt < maxRetries; attempt++ {
		statuses := app.maintMgr.CheckAll()
		if len(statuses) == 0 {
			return
		}

		items := make([]maintenanceStatusItem, 0, len(statuses))
		for _, s := range statuses {
			item := maintenanceStatusItem{
				ID:         s.Reminder.ID,
				Name:       s.Reminder.Name,
				Type:       string(s.Reminder.Type),
				Progress:   s.Progress,
				NeedsAlert: s.NeedsAlert,
				IsOverdue:  s.IsOverdue,
			}
			if s.Reminder.Type == "distance" {
				item.RemainingKm = s.RemainingKm
				item.CurrentKm = s.CurrentKm
			} else {
				item.DaysLeft = s.DaysLeft
				item.DaysElapsed = s.DaysElapsed
			}
			items = append(items, item)
		}

		app.totalKmMu.Lock()
		odoApplied := app.odoApplied
		app.totalKmMu.Unlock()

		payload := maintenancePayload{
			Statuses:        items,
			SentAt:          time.Now(),
			TotalKm:         app.maintMgr.TotalKm(),
			OdometerApplied: odoApplied,
		}

		respBody, err := app.client.SendWithResponse(ctx, "maintenance", payload)
		if err != nil {
			return
		}
		slog.Info("メンテナンス状態送信完了", "count", len(items))

		if len(respBody) == 0 {
			return
		}

		var gasResp gasMaintenanceResponse
		if json.Unmarshal(respBody, &gasResp) != nil {
			return
		}

		// pending_resets 処理
		for _, id := range gasResp.PendingResets {
			if app.maintMgr.ResetReminder(id) {
				slog.Info("メンテナンスリセット", "id", id)
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
			// 補正後に再送信（ループで次のイテレーションへ）
			continue
		}

		app.totalKmMu.Lock()
		if app.odoApplied {
			// GASが補正をクリア済み → フラグをリセット
			app.odoApplied = false
		}
		app.totalKmMu.Unlock()

		// トリップリセット処理
		if gasResp.TripReset {
			app.tracker.ManualReset()
			slog.Info("トリップリセット", "reason", "給油記録")
		}

		return // 正常完了
	}
}

// restoreFromGAS はGASから累計走行距離を復元する（起動時用）
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
		return
	}
	app.totalKmMu.Unlock()
}
