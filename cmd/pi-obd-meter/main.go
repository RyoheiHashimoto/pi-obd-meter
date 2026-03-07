package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/hashimoto/pi-obd-meter/internal/display"
	"github.com/hashimoto/pi-obd-meter/internal/maintenance"
	"github.com/hashimoto/pi-obd-meter/internal/obd"
	"github.com/hashimoto/pi-obd-meter/internal/sender"
	"github.com/hashimoto/pi-obd-meter/internal/trip"
)

// Config はアプリケーション設定
type Config struct {
	SerialPort           string                   `json:"serial_port"`
	WebhookURL           string                   `json:"webhook_url"`
	PollIntervalMs       int                      `json:"poll_interval_ms"`
	LocalAPIPort         int                      `json:"local_api_port"`
	MaintenancePath      string                   `json:"maintenance_path"`
	WebStaticDir         string                   `json:"web_static_dir"`
	RedlineRPM           int                      `json:"redline_rpm"`
	MaxSpeedKmh          int                      `json:"max_speed_kmh"`
	MaxRPM               int                      `json:"max_rpm"`
	OBDProtocol          string                   `json:"obd_protocol"`
	FuelTankCapacityL    float64                  `json:"fuel_tank_capacity_l"`
	EngineDisplacementL  float64                  `json:"engine_displacement_l"`
	RefuelMinIncreasePct float64                  `json:"refuel_min_increase_pct"`
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
	TripKm         float64              `json:"trip_km"`
	CoolantTemp    float64              `json:"coolant_temp"`
	Alerts         []maintenance.Status `json:"alerts"`
	Notification   string               `json:"notification,omitempty"`
	OBDConnected   bool                 `json:"obd_connected"`
	WiFiConnected  bool                 `json:"wifi_connected"`
	PendingCount   int                  `json:"pending_count"`
	SendSending    bool                 `json:"send_sending"`
}

var version = "dev"

// calcFuelEconomy は瞬間燃費(km/L)を計算する
// MAF対応: 燃料レート = MAF(g/s) × 3600 / (14.7 × 750) L/h
// MAF非対応: 燃料レート ≈ RPM × 負荷% × 排気量 / 定数 L/h
func calcFuelEconomy(speed, rpm, load, maf float64, hasMAF bool, displacementL float64) float64 {
	if speed < 0.5 && rpm < 100 {
		return 0 // エンジン停止
	}

	var fuelRateLH float64
	if hasMAF && maf > 0 {
		// MAFから直接計算: g/s → L/h
		// AFR(空燃比)=14.7, ガソリン密度=750 g/L
		fuelRateLH = maf * 3600.0 / (14.7 * 750.0)
	} else {
		// 負荷×RPM×排気量から推定
		// 4ストロークなので吸気は2回転に1回
		// 体積効率を負荷%で近似
		if rpm < 100 || load < 0.1 {
			fuelRateLH = 0.8 // アイドリング最低消費量
		} else {
			airFlowEstimate := (rpm / 2.0) * (load / 100.0) * displacementL / 60.0 // L/s of air
			airMassGS := airFlowEstimate * 1.225                                    // g/s (空気密度1.225 kg/m³ = 1.225 g/L)
			fuelRateLH = airMassGS * 3600.0 / (14.7 * 750.0)
		}
	}

	if fuelRateLH < 0.01 {
		return 99.9 // エンブレ・コースティング
	}
	if speed < 10 {
		return 0 // 低速域（クリープ等）は燃費表示しない
	}
	kmL := speed / fuelRateLH
	if kmL > 99.9 {
		kmL = 99.9
	}
	return kmL
}

var (
	latestData      RealtimeData
	dataMu          sync.RWMutex
	notification    string
	notificationMu  sync.RWMutex
	notificationExp time.Time
)

// setNotification はメーター画面に表示する一時通知をセットする
func setNotification(msg string, duration time.Duration) {
	notificationMu.Lock()
	defer notificationMu.Unlock()
	notification = msg
	notificationExp = time.Now().Add(duration)
}

// getNotification は有効期限内の通知を返す（期限切れなら空文字列）
func getNotification() string {
	notificationMu.RLock()
	defer notificationMu.RUnlock()
	if time.Now().After(notificationExp) {
		return ""
	}
	return notification
}

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

func loadConfig(path string) Config {
	cfg := Config{
		SerialPort:           "/dev/rfcomm0",
		WebhookURL:           "",
		PollIntervalMs:       500,
		LocalAPIPort:         9090,
		MaintenancePath:      "/var/lib/pi-obd-meter/maintenance.json",
		WebStaticDir:         "/opt/pi-obd-meter/web/static",
		RedlineRPM:           6500,
		MaxSpeedKmh:          180,
		MaxRPM:               8000,
		OBDProtocol:          "6",
		FuelTankCapacityL:    44,
		EngineDisplacementL:  1.3,
		RefuelMinIncreasePct: 5.0,
		Brightness:           display.DefaultConfig(),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("設定ファイルが見つかりません、デフォルト設定を使用: %v", err)
		return cfg
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Printf("設定ファイルのJSON解析に失敗、デフォルト設定を使用: %v", err)
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

	// --- ローカルAPI + Web UI（OBD接続前に起動） ---
	go startLocalAPI(cfg, maintMgr)
	fmt.Printf("✓ LCD メーター: http://localhost:%d/meter.html\n", cfg.LocalAPIPort)

	// --- メンテナンス状態をGASに送信（WiFi接続後） ---
	go func() {
		for i := 0; i < 30; i++ {
			if checkWiFi() {
				sendMaintenanceStatus(client, maintMgr, &totalKmAccum, &odoApplied, tracker)
				return
			}
			time.Sleep(2 * time.Second)
		}
		log.Printf("⚠ WiFi接続待ちタイムアウト、メンテナンス初回送信スキップ")
	}()

	// --- ELM327接続（失敗してもクラッシュしない） ---
	elm := obd.NewELM327(cfg.SerialPort, cfg.OBDProtocol)
	var reader *obd.Reader
	obdConnected := false
	var hasMAF bool

	tryConnectOBD := func() bool {
		if err := elm.Connect(); err != nil {
			log.Printf("⚠ ELM327接続失敗: %v", err)
			return false
		}
		r := obd.NewReader(elm)
		if err := r.DetectCapabilities(); err != nil {
			log.Printf("⚠ OBDケイパビリティ検出失敗: %v", err)
			elm.Close()
			return false
		}
		reader = r
		hasMAF = reader.HasMAF()
		fmt.Println("✓ ELM327接続完了")
		return true
	}

	obdConnected = tryConnectOBD()
	if obdConnected {
		if reader.HasFuelTank() {
			checkRefueling(reader, tracker, client, cfg)
		} else {
			fmt.Println("✗ 燃料タンクレベルPID非対応 → 給油検出無効")
		}
	} else {
		fmt.Println("⚠ OBD未接続 → メーター表示のみで起動、バックグラウンドでリトライ")
	}

	// --- メインループ ---
	const fastIntervalMs = 150
	const fullEveryN = 5

	ticker := time.NewTicker(fastIntervalMs * time.Millisecond)
	defer ticker.Stop()

	retryTicker := time.NewTicker(5 * time.Minute)
	defer retryTicker.Stop()

	maintTicker := time.NewTicker(5 * time.Minute)
	defer maintTicker.Stop()

	obdRetryTicker := time.NewTicker(10 * time.Second)
	defer obdRetryTicker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("\n▶ データ収集開始...")
	fmt.Printf("  LCD メーター: http://localhost:%d/meter.html\n", cfg.LocalAPIPort)

	sampleCount := 0
	statusCount := 0
	var lastFuelTank float64
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
					log.Printf("⚠ OBD接続ロスト（連続%dエラー）→ リトライ待ち", errCount)
					obdConnected = false
					elm.Close()
					errCount = 0
				}
				continue
			}
			errCount = 0

			dtSec := float64(fastIntervalMs) / 1000.0

			if isFull {
				lastFuelTank = data.FuelTankLevel
				lastCoolantTemp = data.CoolantTemp
				tracker.UpdateFuelLevel(lastFuelTank)
				wifiConnected = checkWiFi()
				lastFuelEconomy = calcFuelEconomy(data.SpeedKmh, data.RPM, data.EngineLoad, data.MAFAirFlow, hasMAF, cfg.EngineDisplacementL)
			}

			tracker.Update(data.SpeedKmh)
			totalKmAccum += (data.SpeedKmh / 3600.0) * dtSec
			maintMgr.UpdateTotalKm(totalKmAccum)

			dataMu.Lock()
			latestData = RealtimeData{
				SpeedKmh:       data.SpeedKmh,
				RPM:            data.RPM,
				EngineLoad:     data.EngineLoad,
				ThrottlePos:    data.ThrottlePos,
				FuelEconomy:    lastFuelEconomy,
				TripKm:         tracker.DistanceKm(),
				CoolantTemp:    lastCoolantTemp,
				Alerts:         maintMgr.GetAlerts(),
				Notification:   getNotification(),
				OBDConnected:   true,
				WiFiConnected:  wifiConnected,
				PendingCount:   client.QueueSize(),
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
					if reader.HasFuelTank() {
						checkRefueling(reader, tracker, client, cfg)
					}
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
			if obdConnected {
				elm.Close()
			}
			return
		}
	}
}

// checkRefueling はエンジン始動時に給油を検出する
func checkRefueling(reader *obd.Reader, tracker *trip.Tracker, client *sender.Client, cfg Config) {
	// タンク残量を複数回読み取り平均（ノイズ低減）
	var sum float64
	var count int
	for i := 0; i < 3; i++ {
		pct, err := reader.ReadFuelTankLevel()
		if err != nil {
			continue
		}
		sum += pct
		count++
		if i < 2 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	if count == 0 {
		fmt.Println("⚠ 燃料タンクレベル取得失敗、給油検出スキップ")
		return
	}
	currentPct := sum / float64(count)
	fmt.Printf("✓ 燃料タンク: %.1f%%\n", currentPct)

	tripStartPct, lastPct, valid := tracker.GetFuelState()
	if !valid {
		// 初回起動: ベースライン設定のみ
		fmt.Println("  初回起動: 燃料ベースライン設定")
		tracker.ResetFuelBaseline(currentPct)
		return
	}

	delta := currentPct - lastPct
	minIncrease := cfg.RefuelMinIncreasePct
	if minIncrease <= 0 {
		minIncrease = 5.0
	}

	if delta >= minIncrease {
		// 給油検出!
		fuelUsedL := (tripStartPct - lastPct) / 100.0 * cfg.FuelTankCapacityL
		refuelAmountL := delta / 100.0 * cfg.FuelTankCapacityL
		completed := tracker.ManualReset()

		fmt.Printf("⛽ 給油検出! タンク %.1f%% → %.1f%% (+%.1fL)\n", lastPct, currentPct, refuelAmountL)

		if completed != nil && completed.DistanceKm >= 1.0 && fuelUsedL > 0 {
			fuelEcon := completed.DistanceKm / fuelUsedL
			fmt.Printf("   前回区間: %.1f km / %.1f L = %.1f km/L\n",
				completed.DistanceKm, fuelUsedL, fuelEcon)
			setNotification(fmt.Sprintf("⛽ %.1f km/L (%.0fkm)", fuelEcon, completed.DistanceKm), 10*time.Second)

			// GASに給油データを送信
			client.Send("refuel", map[string]interface{}{
				"trip_id":          completed.TripID,
				"start_time":       completed.StartTime,
				"end_time":         completed.EndTime,
				"distance_km":      completed.DistanceKm,
				"fuel_used_l":      fuelUsedL,
				"fuel_economy":     fuelEcon,
				"refuel_amount_l":  refuelAmountL,
				"old_level_pct":    lastPct,
				"new_level_pct":    currentPct,
				"max_speed_kmh":    completed.MaxSpeedKmh,
				"avg_speed_kmh":    completed.AvgSpeedKmh,
				"driving_time_sec": completed.DrivingTimeSec,
				"idle_time_sec":    completed.IdleTimeSec,
			})
		}

		if getNotification() == "" {
			// 燃費算出できなかった場合（距離不足等）
			setNotification(fmt.Sprintf("⛽ 給油 +%.0fL", refuelAmountL), 10*time.Second)
		}
		tracker.ResetFuelBaseline(currentPct)
	} else if delta > 3.0 {
		fmt.Printf("  タンク微増 +%.1f%% (しきい値%.0f%%未満、給油判定せず)\n", delta, minIncrease)
		tracker.UpdateFuelLevel(currentPct)
	} else {
		tracker.UpdateFuelLevel(currentPct)
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
					log.Printf("✓ メンテナンスリセット: %s", id)
				}
			}

			// ODO補正処理
			if gasResp.OdometerCorrection != nil && *gasResp.OdometerCorrection > 0 {
				newOdo := *gasResp.OdometerCorrection
				if totalKm != nil {
					*totalKm = newOdo
				}
				maintMgr.UpdateTotalKm(newOdo)
				log.Printf("✓ ODO補正適用: %.0f km", newOdo)
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
				log.Printf("✓ トリップリセット（給油記録による）")
			}
		}
	}
}

// corsMiddleware はCORSヘッダーを付与する
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

func startLocalAPI(cfg Config, maintMgr *maintenance.Manager) {
	mux := http.NewServeMux()

	// --- Web UI配信 ---
	mux.Handle("GET /", http.FileServer(http.Dir(cfg.WebStaticDir)))

	// --- 設定API（meter.htmlがredline_rpm等を取得する） ---
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"redline_rpm":   cfg.RedlineRPM,
			"max_speed_kmh": cfg.MaxSpeedKmh,
			"max_rpm":       cfg.MaxRPM,
		})
	})

	// --- リアルタイムAPI（LCD用、200ms間隔でポーリングされる） ---
	mux.HandleFunc("GET /api/realtime", func(w http.ResponseWriter, r *http.Request) {
		dataMu.RLock()
		defer dataMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(latestData)
	})

	// --- メンテナンスAPI（メーター画面のアラートバー用） ---
	mux.HandleFunc("GET /api/maintenance", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(maintMgr.CheckAll())
	})

	addr := fmt.Sprintf(":%d", cfg.LocalAPIPort)
	log.Printf("ローカルAPI起動: %s", addr)
	http.ListenAndServe(addr, corsMiddleware(mux))
}
