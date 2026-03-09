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
	SpeedKmh      float64              `json:"speed_kmh"`
	RPM           float64              `json:"rpm"`
	EngineLoad    float64              `json:"engine_load"`
	ThrottlePos   float64              `json:"throttle_pos"`
	FuelEconomy   float64              `json:"fuel_economy"`
	TripKm        float64              `json:"trip_km"`
	CoolantTemp   float64              `json:"coolant_temp"`
	Alerts        []maintenance.Status `json:"alerts"`
	Notification  string               `json:"notification,omitempty"`
	OBDConnected  bool                 `json:"obd_connected"`
	WiFiConnected bool                 `json:"wifi_connected"`
	PendingCount  int                  `json:"pending_count"`
	SendSending   bool                 `json:"send_sending"`
}

var version = "dev"

// calcFuelEconomy は瞬間燃費(km/L)を計算する
// MAF対応: 燃料レート = MAF(g/s) × 3600 / (14.7 × 750) L/h
// MAF非対応: 燃料レート ≈ RPM × 負荷% × 排気量 / 定数 L/h
// 燃費計算用の物理定数
const (
	stoichiometricAFR = 14.7  // ガソリンの理論空燃比 (空気kg / 燃料kg)
	gasolineDensityGL = 750.0 // ガソリン密度 (g/L)
	airDensityGL      = 1.225 // 標準大気密度 (g/L = kg/m³)
	idleFuelRateLH    = 0.8   // アイドリング時の最低燃料消費量 (L/h)
	maxDisplayKmL     = 99.9  // 燃費表示の上限値 (km/L)
	minDisplaySpeedKm = 10.0  // 燃費表示の最低速度 (km/h)
)

func calcFuelEconomy(speed, rpm, load, maf float64, hasMAF bool, displacementL float64) float64 {
	if speed < 0.5 && rpm < 100 {
		return 0 // エンジン停止
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
		return maxDisplayKmL // エンブレ・コースティング
	}
	if speed < minDisplaySpeedKm {
		return 0 // 低速域（クリープ等）は燃費表示しない
	}
	kmL := speed / fuelRateLH
	if kmL > maxDisplayKmL {
		kmL = maxDisplayKmL
	}
	return kmL
}

var (
	latestData      RealtimeData
	dataMu          sync.RWMutex
	notification    string
	notificationMu  sync.RWMutex
	notificationExp time.Time
	startedAt       = time.Now()
)

// getNotification は有効期限内の通知を返す（期限切れなら空文字列）
func getNotification() string {
	notificationMu.RLock()
	defer notificationMu.RUnlock()
	if time.Now().After(notificationExp) {
		return ""
	}
	return notification
}

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

func main() {
	configPath := flag.String("config", "/etc/pi-obd-meter/config.json", "設定ファイルパス")
	flag.Parse()

	cfg := loadConfig(*configPath)

	fmt.Println("=================================")
	fmt.Printf("  DYデミオ 燃費メーター %s\n", version)
	fmt.Println("=================================")
	fmt.Printf("シリアルポート: %s\n", cfg.SerialPort)

	// --- 送信クライアント（Google Sheets） ---
	client := sender.NewClient(cfg.WebhookURL)

	// --- メンテナンスマネージャー ---
	maintMgr := maintenance.NewManager(cfg.MaintenancePath)
	maintMgr.InitDefaults(cfg.MaintenanceReminders)
	fmt.Printf("✓ メンテナンスリマインダー: %d 項目\n", len(maintMgr.GetAll()))

	// --- 累計走行距離 ---
	totalKmAccum := maintMgr.TotalKm()
	if totalKmAccum == 0 && cfg.InitialOdometerKm > 0 {
		totalKmAccum = cfg.InitialOdometerKm
		maintMgr.UpdateTotalKm(totalKmAccum)
		fmt.Printf("✓ 初期ODO設定: %.0f km\n", totalKmAccum)
	} else {
		fmt.Printf("✓ 累計走行距離: %.1f km（復元済み）\n", totalKmAccum)
	}

	// --- ODO補正フラグ ---
	odoApplied := false

	// --- トリップトラッカー ---
	tracker := trip.NewTracker(trip.TrackerConfig{})

	// --- 輝度制御 ---
	brightness := display.NewBrightnessController(cfg.Brightness)
	brightness.Start()
	defer brightness.Stop()

	// --- 自動更新（バックグラウンド） ---
	go tryAutoUpdate()

	// --- ローカルAPI + Web UI（OBD接続前に起動） ---
	go startLocalAPI(cfg, maintMgr)
	fmt.Printf("✓ LCD メーター: http://localhost:%d/meter.html\n", cfg.LocalAPIPort)

	// --- WiFi接続後: GASから状態復元 + メンテナンス初回送信 ---
	go func() {
		for i := 0; i < 30; i++ {
			if checkWiFi() {
				// GASから累計走行距離を復元（overlayFSリブート対策）
				if restored, err := client.RestoreState(); err == nil && restored.TotalKm > 0 {
					if totalKmAccum < restored.TotalKm {
						totalKmAccum = restored.TotalKm
						maintMgr.UpdateTotalKm(totalKmAccum)
						slog.Info("GASからODO復元", "total_km", restored.TotalKm)
					}
				}
				sendMaintenanceStatus(client, maintMgr, &totalKmAccum, &odoApplied, tracker)
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
					dataMu.Lock()
					latestData = RealtimeData{
						Alerts:        maintMgr.GetAlerts(),
						Notification:  getNotification(),
						OBDConnected:  false,
						WiFiConnected: wifiConnected,
						PendingCount:  client.QueueSize(),
						SendSending:   client.IsSending(),
					}
					dataMu.Unlock()
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
				lastFuelEconomy = calcFuelEconomy(data.SpeedKmh, data.RPM, data.EngineLoad, data.MAFAirFlow, hasMAF, cfg.EngineDisplacementL)
			}

			tracker.Update(data.SpeedKmh)
			totalKmAccum += (data.SpeedKmh / 3600.0) * dtSec
			maintMgr.UpdateTotalKm(totalKmAccum)

			dataMu.Lock()
			latestData = RealtimeData{
				SpeedKmh:      data.SpeedKmh,
				RPM:           data.RPM,
				EngineLoad:    data.EngineLoad,
				ThrottlePos:   data.ThrottlePos,
				FuelEconomy:   lastFuelEconomy,
				TripKm:        tracker.DistanceKm(),
				CoolantTemp:   lastCoolantTemp,
				Alerts:        maintMgr.GetAlerts(),
				Notification:  getNotification(),
				OBDConnected:  true,
				WiFiConnected: wifiConnected,
				PendingCount:  client.QueueSize(),
			}
			dataMu.Unlock()

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
			client.RetryPending()

		case <-maintTicker.C:
			sendMaintenanceStatus(client, maintMgr, &totalKmAccum, &odoApplied, tracker)

		case sig := <-sigCh:
			fmt.Printf("\n\nシグナル受信 (%v)、シャットダウン...\n", sig)
			tracker.SaveState()
			maintMgr.SaveState()
			slog.Info("状態保存完了")
			if obdConnected {
				elm.Close()
			}
			return
		}
	}
}

// sendMaintenanceStatus はメンテナンス状態をGASに送信する
// totalKm が非nilの場合、ODO補正値を受け取ったら更新する
// odoApplied が非nilの場合、前回の補正適用フラグを送信・管理する
func sendMaintenanceStatus(client *sender.Client, maintMgr *maintenance.Manager, totalKm *float64, odoApplied *bool, tracker *trip.Tracker) {
	statuses := maintMgr.CheckAll()
	if len(statuses) == 0 {
		return
	}

	// 送信用にシンプルな構造に変換
	var items []map[string]interface{}
	for _, s := range statuses {
		item := map[string]interface{}{
			"id":          s.Reminder.ID,
			"name":        s.Reminder.Name,
			"type":        string(s.Reminder.Type),
			"progress":    s.Progress,
			"needs_alert": s.NeedsAlert,
			"is_overdue":  s.IsOverdue,
		}
		if s.Reminder.Type == "distance" {
			item["remaining_km"] = s.RemainingKm
			item["current_km"] = s.CurrentKm
		} else {
			item["days_left"] = s.DaysLeft
			item["days_elapsed"] = s.DaysElapsed
		}
		items = append(items, item)
	}

	payload := map[string]interface{}{
		"statuses": items,
		"sent_at":  time.Now(),
		"total_km": maintMgr.TotalKm(),
	}
	if odoApplied != nil && *odoApplied {
		payload["odometer_applied"] = true
	}

	respBody, err := client.SendWithResponse("maintenance", payload)
	if err != nil {
		return
	}
	fmt.Printf("✓ メンテナンス状態送信: %d 項目\n", len(items))

	// GASレスポンスを処理
	if len(respBody) > 0 {
		var gasResp struct {
			PendingResets      []string `json:"pending_resets"`
			OdometerCorrection *float64 `json:"odometer_correction"`
			TripReset          bool     `json:"trip_reset"`
		}
		if json.Unmarshal(respBody, &gasResp) == nil {
			// pending_resets処理
			for _, id := range gasResp.PendingResets {
				if maintMgr.ResetReminder(id) {
					slog.Info("メンテナンスリセット", "id", id)
				}
			}

			// ODO補正処理
			if gasResp.OdometerCorrection != nil && *gasResp.OdometerCorrection > 0 {
				newOdo := *gasResp.OdometerCorrection
				if totalKm != nil {
					*totalKm = newOdo
				}
				maintMgr.UpdateTotalKm(newOdo)
				slog.Info("ODO補正適用", "odometer_km", newOdo)
				if odoApplied != nil {
					*odoApplied = true
				}
				// 補正後のステータスを即座に再送信（GASシートを更新するため）
				sendMaintenanceStatus(client, maintMgr, totalKm, odoApplied, tracker)
				return
			} else if odoApplied != nil && *odoApplied {
				// GASが補正をクリア済み → フラグをリセット
				*odoApplied = false
			}

			// トリップリセット処理
			if gasResp.TripReset && tracker != nil {
				tracker.ManualReset()
				slog.Info("トリップリセット", "reason", "給油記録")
			}
		}
	}
}

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
func startLocalAPI(cfg Config, maintMgr *maintenance.Manager) {
	mux := http.NewServeMux()

	// --- Web UI配信 ---
	var webFS http.FileSystem
	if cfg.WebStaticDir != "" {
		webFS = http.Dir(cfg.WebStaticDir)
		slog.Info("Web UI: ファイルシステムから配信", "dir", cfg.WebStaticDir)
	} else {
		subFS, _ := fs.Sub(web.StaticFS, "static")
		webFS = http.FS(subFS)
		slog.Info("Web UI: 埋め込みファイルから配信")
	}
	mux.Handle("GET /", http.FileServer(webFS))

	// --- 設定API（meter.htmlがmax_speed_kmhを取得する） ---
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"max_speed_kmh": cfg.MaxSpeedKmh,
			"version":       version,
		})
	})

	// --- リアルタイムAPI（LCD用、200ms間隔でポーリングされる） ---
	mux.HandleFunc("GET /api/realtime", func(w http.ResponseWriter, r *http.Request) {
		dataMu.RLock()
		defer dataMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(latestData)
	})

	// --- ヘルスチェックAPI ---
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		dataMu.RLock()
		d := latestData
		dataMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":         "ok",
			"version":        version,
			"uptime_sec":     int(time.Since(startedAt).Seconds()),
			"obd_connected":  d.OBDConnected,
			"wifi_connected": d.WiFiConnected,
			"pending_count":  d.PendingCount,
		})
	})

	// --- メンテナンスAPI（メーター画面のアラートバー用） ---
	mux.HandleFunc("GET /api/maintenance", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(maintMgr.CheckAll())
	})

	addr := fmt.Sprintf(":%d", cfg.LocalAPIPort)
	slog.Info("ローカルAPI起動", "addr", addr)
	http.ListenAndServe(addr, corsMiddleware(mux))
}

// tryAutoUpdate は起動時にGitHub Releasesから最新版をチェックし、
// 新しいバージョンがあればアトミックに差し替えて再起動する。
func tryAutoUpdate() {
	if version == "dev" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if !waitForInternet(ctx, 90*time.Second) {
		slog.Info("自動更新: インターネット未接続、スキップ")
		return
	}

	latest, found, err := selfupdate.DetectLatest(ctx,
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
	if err := selfupdate.UpdateTo(ctx, latest.AssetURL, latest.AssetName, exe); err != nil {
		slog.Error("自動更新: 更新失敗", "error", err)
		return
	}
	slog.Info("自動更新: 更新完了、再起動します", "version", latest.Version())
	os.Exit(0) // systemd Restart=always で新バイナリが起動
}

// waitForInternet はインターネット接続が利用可能になるまで待つ。
// タイムアウトまでに接続できなければ false を返す。
func waitForInternet(ctx context.Context, timeout time.Duration) bool {
	deadline := time.After(timeout)
	httpClient := &http.Client{Timeout: 5 * time.Second}
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			return false
		default:
			resp, err := httpClient.Get("https://api.github.com/zen")
			if err == nil {
				resp.Body.Close()
				return true
			}
			time.Sleep(5 * time.Second)
		}
	}
}
