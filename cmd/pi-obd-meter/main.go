// pi-obd-meter はOBD-2対応車向けの車載メーターアプリケーション。
// ELM327経由で ECU からリアルタイムデータを取得し、5インチLCDに表示する。
// 走行距離・メンテナンス状態は Google Sheets (GAS Webhook) に自動送信する。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	selfupdate "github.com/creativeprojects/go-selfupdate"

	"github.com/hashimoto/pi-obd-meter/internal/display"
	"github.com/hashimoto/pi-obd-meter/internal/maintenance"
	"github.com/hashimoto/pi-obd-meter/internal/obd"
	"github.com/hashimoto/pi-obd-meter/internal/sender"
	"github.com/hashimoto/pi-obd-meter/internal/trip"
	"github.com/hashimoto/pi-obd-meter/web"
)

// Config はアプリケーション設定
type Config struct {
	SerialPort           string                   `json:"serial_port"`
	WebhookURL           string                   `json:"webhook_url"`
	PollIntervalMs       int                      `json:"poll_interval_ms"`
	LocalAPIPort         int                      `json:"local_api_port"`
	MaintenancePath      string                   `json:"maintenance_path"`
	WebStaticDir         string                   `json:"web_static_dir"`
	MaxSpeedKmh          int                      `json:"max_speed_kmh"`
	OBDProtocol          string                   `json:"obd_protocol"`
	EngineDisplacementL  float64                  `json:"engine_displacement_l"`
	InitialOdometerKm    float64                  `json:"initial_odometer_km"`
	MaintenanceReminders []maintenance.Reminder   `json:"maintenance_reminders"`
	Brightness           display.BrightnessConfig `json:"brightness"`
}

// RealtimeData はリアルタイムAPIのレスポンス（LCD用）
type RealtimeData struct {
	SpeedKmh       float64              `json:"speed_kmh"`
	RPM            float64              `json:"rpm"`
	EngineLoad     float64              `json:"engine_load"`
	ThrottlePos    float64              `json:"throttle_pos"`
	FuelEconomy    float64              `json:"fuel_economy"`
	FuelRateLH     float64              `json:"fuel_rate_lh"`
	AvgFuelEconomy float64              `json:"avg_fuel_economy"`
	TripKm         float64              `json:"trip_km"`
	CoolantTemp    float64              `json:"coolant_temp"`
	Alerts         []maintenance.Status `json:"alerts"`
	Notification   string               `json:"notification,omitempty"`
	OBDConnected   bool                 `json:"obd_connected"`
	WiFiConnected  bool                 `json:"wifi_connected"`
	PendingCount   int                  `json:"pending_count"`
	SendSending    bool                 `json:"send_sending"`
}

// configResponse は /api/config のレスポンス
type configResponse struct {
	MaxSpeedKmh int     `json:"max_speed_kmh"`
	Version     string  `json:"version"`
	EcoLHGreen  float64 `json:"eco_lh_green"`
	EcoLHRed    float64 `json:"eco_lh_red"`
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

var version = "dev"

// --- App: アプリケーション状態を集約 ---

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

// --- 燃費計算 ---

// 燃費計算用の物理定数
const (
	stoichiometricAFR = 14.7  // ガソリンの理論空燃比 (空気kg / 燃料kg)
	gasolineDensityGL = 750.0 // ガソリン密度 (g/L)
	airDensityGL      = 1.225 // 標準大気密度 (g/L = kg/m³)
	idleFuelRateLH    = 0.8   // アイドリング時の最低燃料消費量 (L/h)
	maxDisplayKmL     = 99.9  // 燃費表示の上限値 (km/L)
	minDisplaySpeedKm = 10.0  // 燃費表示の最低速度 (km/h)
)

// calcFuelEconomy は瞬間燃費(km/L)を計算する
// MAF対応: 燃料レート = MAF(g/s) × 3600 / (14.7 × 750) L/h
// MAF非対応: 燃料レート ≈ RPM × 負荷% × 排気量 / 定数 L/h
func calcFuelEconomy(speed, rpm, load, maf float64, hasMAF bool, displacementL float64) (kmL, rateLH float64) {
	if speed < 0.5 && rpm < 100 {
		return 0, 0 // エンジン停止
	}

	var fuelRateLH float64
	if hasMAF && maf > 0 {
		// MAFから直接計算: g/s → L/h
		fuelRateLH = maf * 3600.0 / (stoichiometricAFR * gasolineDensityGL)
	} else {
		// 負荷×RPM×排気量から推定
		// 4ストロークなので吸気は2回転に1回
		// 体積効率を負荷%で近似
		if rpm < 100 || load < 0.1 {
			fuelRateLH = idleFuelRateLH
		} else {
			airFlowEstimate := (rpm / 2.0) * (load / 100.0) * displacementL / 60.0 // L/s of air
			airMassGS := airFlowEstimate * airDensityGL                            // g/s
			fuelRateLH = airMassGS * 3600.0 / (stoichiometricAFR * gasolineDensityGL)
		}
	}

	if fuelRateLH < 0.01 {
		return -1, fuelRateLH // エンブレ・燃料カット（-1 = 特別表示）
	}
	if speed < minDisplaySpeedKm {
		return 0, fuelRateLH // 低速域（クリープ等）は燃費表示しない
	}
	kmL = speed / fuelRateLH
	if kmL > maxDisplayKmL {
		kmL = maxDisplayKmL
	}
	return kmL, fuelRateLH
}

// --- ユーティリティ ---

// checkWiFi は wlan0 インタフェースにIPアドレスが割り当てられているかを返す
func checkWiFi() bool {
	iface, err := net.InterfaceByName("wlan0")
	if err != nil {
		return false
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	return len(addrs) > 0
}

// loadConfig はJSONファイルから設定を読み込む。ファイルがなければデフォルト値を返す。
func loadConfig(path string) Config {
	cfg := Config{
		SerialPort:          "/dev/rfcomm0",
		WebhookURL:          "",
		PollIntervalMs:      500,
		LocalAPIPort:        9090,
		MaintenancePath:     "/var/lib/pi-obd-meter/maintenance.json",
		WebStaticDir:        "",
		MaxSpeedKmh:         180,
		OBDProtocol:         "6",
		EngineDisplacementL: 1.3,
		Brightness:          display.DefaultConfig(),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("設定ファイルが見つかりません、デフォルト使用", "path", path, "error", err)
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		slog.Warn("設定ファイルのJSON解析失敗、デフォルト使用", "path", path, "error", err)
	}
	return cfg
}

// --- HTTP サーバー ---

// corsMiddleware はCORSヘッダーを付与する（meter.htmlからのfetchリクエスト許可用）
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
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
		json.NewEncoder(w).Encode(configResponse{
			MaxSpeedKmh: app.cfg.MaxSpeedKmh,
			Version:     version,
			EcoLHGreen:  1.5 * app.cfg.EngineDisplacementL,
			EcoLHRed:    3.0 * app.cfg.EngineDisplacementL,
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

// --- 自動更新 ---

// tryAutoUpdate は起動時にGitHub Releasesから最新版をチェックし、
// 新しいバージョンがあればアトミックに差し替えて再起動する。
func tryAutoUpdate(ctx context.Context) {
	if version == "dev" {
		return
	}

	updateCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	if !waitForInternet(updateCtx, 90*time.Second) {
		slog.Info("自動更新: インターネット未接続、スキップ")
		return
	}

	latest, found, err := selfupdate.DetectLatest(updateCtx,
		selfupdate.ParseSlug("RyoheiHashimoto/pi-obd-meter"))
	if err != nil {
		slog.Warn("自動更新: 最新バージョン検出失敗", "error", err)
		return
	}
	if !found {
		slog.Info("自動更新: リリースが見つかりません")
		return
	}
	if latest.LessOrEqual(version) {
		slog.Info("自動更新: 最新版で稼働中", "version", version)
		return
	}

	slog.Info("自動更新: 新バージョン検出", "current", version, "latest", latest.Version())
	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		slog.Error("自動更新: 実行パス取得失敗", "error", err)
		return
	}
	if err := selfupdate.UpdateTo(updateCtx, latest.AssetURL, latest.AssetName, exe); err != nil {
		slog.Error("自動更新: 更新失敗", "error", err)
		return
	}
	slog.Info("自動更新: 更新完了、再起動します", "version", latest.Version())
	os.Exit(0) // systemd Restart=always で新バイナリが起動
}

// waitForInternet はインターネット接続が利用可能になるまで待つ。
// タイムアウトまでに接続できなければ false を返す。
func waitForInternet(ctx context.Context, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	httpClient := &http.Client{Timeout: 5 * time.Second}

	// 初回即チェック
	if resp, err := httpClient.Get("https://api.github.com/zen"); err == nil {
		resp.Body.Close()
		return true
	}

	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-ticker.C:
			if resp, err := httpClient.Get("https://api.github.com/zen"); err == nil {
				resp.Body.Close()
				return true
			}
		}
	}
}

// --- メインエントリーポイント ---

func main() {
	configPath := flag.String("config", "/etc/pi-obd-meter/config.json", "設定ファイルパス")
	flag.Parse()

	cfg := loadConfig(*configPath)

	fmt.Println("=================================")
	fmt.Printf("  DYデミオ 燃費メーター %s\n", version)
	fmt.Println("=================================")
	fmt.Printf("シリアルポート: %s\n", cfg.SerialPort)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := newApp(cfg)

	// --- 輝度制御 ---
	brightness := display.NewBrightnessController(cfg.Brightness)
	brightness.Start()
	defer brightness.Stop()

	// --- 自動更新（バックグラウンド） ---
	go tryAutoUpdate(ctx)

	// --- ローカルAPI + Web UI（OBD接続前に起動） ---
	go app.startLocalAPI(ctx)
	fmt.Printf("✓ LCD メーター: http://localhost:%d/meter.html\n", cfg.LocalAPIPort)

	// --- WiFi接続後: GASから状態復元 + メンテナンス初回送信 ---
	go func() {
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
	}()

	// --- ELM327接続（失敗してもクラッシュしない） ---
	elm := obd.NewELM327(cfg.SerialPort, cfg.OBDProtocol)
	var reader *obd.Reader
	obdConnected := false
	var hasMAF bool

	tryConnectOBD := func() bool {
		if err := elm.Connect(); err != nil {
			slog.Warn("ELM327接続失敗", "error", err)
			return false
		}
		r := obd.NewReader(elm)
		if err := r.DetectCapabilities(); err != nil {
			slog.Warn("OBDケイパビリティ検出失敗", "error", err)
			elm.Close()
			return false
		}
		reader = r
		hasMAF = reader.HasMAF()
		fmt.Println("✓ ELM327接続完了")
		return true
	}

	obdConnected = tryConnectOBD()
	if !obdConnected {
		fmt.Println("⚠ OBD未接続 → メーター表示のみで起動、バックグラウンドでリトライ")
	}

	// --- メインループ ---
	// 2層ポーリング: 高速(150ms)で RPM/速度/負荷/スロットル を取得し、
	// その5回に1回(750ms)で水温・MAF等の全PIDを取得する。
	const fastIntervalMs = 150
	const fullEveryN = 5

	ticker := time.NewTicker(fastIntervalMs * time.Millisecond)
	defer ticker.Stop()

	retryTicker := time.NewTicker(5 * time.Minute) // 送信失敗キューのリトライ間隔
	defer retryTicker.Stop()

	maintTicker := time.NewTicker(5 * time.Minute) // GASへのメンテナンス状態送信間隔
	defer maintTicker.Stop()

	obdRetryTicker := time.NewTicker(10 * time.Second) // OBD未接続時の再接続間隔
	defer obdRetryTicker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("\n▶ データ収集開始...")
	fmt.Printf("  LCD メーター: http://localhost:%d/meter.html\n", cfg.LocalAPIPort)

	sampleCount := 0
	statusCount := 0
	var lastCoolantTemp float64
	var lastFuelEconomy float64
	var lastFuelRateLH float64
	var errCount int
	var wifiConnected bool
	const maxConsecutiveErrors = 10
	for {
		select {
		case <-ticker.C:
			if !obdConnected {
				// OBD未接続: 1秒ごとにステータスだけ更新
				statusCount++
				if statusCount%7 == 0 {
					wifiConnected = checkWiFi()
					app.updateRealtimeData(RealtimeData{
						Alerts:        app.maintMgr.GetAlerts(),
						Notification:  app.getNotification(),
						OBDConnected:  false,
						WiFiConnected: wifiConnected,
						PendingCount:  app.client.QueueSize(),
						SendSending:   app.client.IsSending(),
					})
				}
				continue
			}

			sampleCount++
			isFull := sampleCount%fullEveryN == 0

			var data *obd.OBDData
			var err error
			if isFull {
				data, err = reader.ReadAll()
			} else {
				data, err = reader.ReadFast()
			}
			if err != nil {
				errCount++
				if errCount >= maxConsecutiveErrors {
					slog.Warn("OBD接続ロスト、リトライ待ち", "consecutive_errors", errCount)
					obdConnected = false
					elm.Close()
					errCount = 0
				}
				continue
			}
			errCount = 0

			// 累計走行距離の積算（トリップとは別にメンテナンス用に独立管理）
			dtSec := float64(fastIntervalMs) / 1000.0

			if isFull {
				lastCoolantTemp = data.CoolantTemp
				wifiConnected = checkWiFi()
				lastFuelEconomy, lastFuelRateLH = calcFuelEconomy(data.SpeedKmh, data.RPM, data.EngineLoad, data.MAFAirFlow, hasMAF, cfg.EngineDisplacementL)
			}

			app.tracker.Update(data.SpeedKmh, lastFuelRateLH)
			app.addDistance((data.SpeedKmh / 3600.0) * dtSec)

			app.updateRealtimeData(RealtimeData{
				SpeedKmh:       data.SpeedKmh,
				RPM:            data.RPM,
				EngineLoad:     data.EngineLoad,
				ThrottlePos:    data.ThrottlePos,
				FuelEconomy:    lastFuelEconomy,
				FuelRateLH:     lastFuelRateLH,
				AvgFuelEconomy: app.tracker.AvgFuelEconomy(),
				TripKm:         app.tracker.DistanceKm(),
				CoolantTemp:    lastCoolantTemp,
				Alerts:         app.maintMgr.GetAlerts(),
				Notification:   app.getNotification(),
				OBDConnected:   true,
				WiFiConnected:  wifiConnected,
				PendingCount:   app.client.QueueSize(),
			})

			if sampleCount%30 == 0 {
				fmt.Printf("\r🚗 %3.0f km/h | %4.0f rpm",
					data.SpeedKmh, data.RPM)
			}

		case <-obdRetryTicker.C:
			if !obdConnected {
				obdConnected = tryConnectOBD()
				if obdConnected {
					sampleCount = 0
					errCount = 0
				}
			}

		case <-retryTicker.C:
			app.client.RetryPending(ctx)

		case <-maintTicker.C:
			app.sendMaintenanceStatus(ctx)

		case sig := <-sigCh:
			fmt.Printf("\n\nシグナル受信 (%v)、シャットダウン...\n", sig)
			cancel() // HTTP サーバーと goroutine にキャンセルを通知
			app.tracker.SaveState()
			app.maintMgr.SaveState()
			slog.Info("状態保存完了")
			if obdConnected {
				elm.Close()
			}
			return
		}
	}
}
