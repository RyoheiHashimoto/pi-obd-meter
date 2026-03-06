package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
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
	ResetThreshold       float64                  `json:"reset_threshold_km"`
	LocalAPIPort         int                      `json:"local_api_port"`
	MaintenancePath      string                   `json:"maintenance_path"`
	WebStaticDir         string                   `json:"web_static_dir"`
	RedlineRPM           int                      `json:"redline_rpm"`
	MaxSpeedKmh          int                      `json:"max_speed_kmh"`
	MaxRPM               int                      `json:"max_rpm"`
	OBDProtocol          string                   `json:"obd_protocol"`
	FuelTankCapacityL    float64                  `json:"fuel_tank_capacity_l"`
	RefuelMinIncreasePct float64                  `json:"refuel_min_increase_pct"`
	MaintenanceReminders []maintenance.Reminder   `json:"maintenance_reminders"`
	Brightness           display.BrightnessConfig `json:"brightness"`
}

// RealtimeData はリアルタイムAPIのレスポンス（LCD用）
type RealtimeData struct {
	SpeedKmh    float64              `json:"speed_kmh"`
	RPM         float64              `json:"rpm"`
	EngineLoad  float64              `json:"engine_load"`
	ThrottlePos float64              `json:"throttle_pos"`
	Trip        *trip.TripData       `json:"trip"`
	Alerts      []maintenance.Status `json:"alerts"`
}

var version = "dev"

var (
	latestData RealtimeData
	dataMu     sync.RWMutex
)

func loadConfig(path string) Config {
	cfg := Config{
		SerialPort:           "/dev/rfcomm0",
		WebhookURL:           "",
		PollIntervalMs:       500,
		ResetThreshold:       0.5,
		LocalAPIPort:         9090,
		MaintenancePath:      "/var/lib/pi-obd-meter/maintenance.json",
		WebStaticDir:         "/opt/pi-obd-meter/web/static",
		RedlineRPM:           6500,
		MaxSpeedKmh:          180,
		MaxRPM:               8000,
		OBDProtocol:          "6",
		FuelTankCapacityL:    44,
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

	// --- ELM327接続 ---
	elm := obd.NewELM327(cfg.SerialPort, cfg.OBDProtocol)
	if err := elm.Connect(); err != nil {
		log.Fatalf("ELM327接続失敗: %v", err)
	}
	defer elm.Close()
	fmt.Println("✓ ELM327接続完了")

	// --- PID検出 ---
	reader := obd.NewReader(elm)
	if err := reader.DetectCapabilities(); err != nil {
		log.Fatalf("OBDケイパビリティ検出失敗: %v", err)
	}

	// --- 送信クライアント（Google Sheets） ---
	client := sender.NewClient(cfg.WebhookURL)

	// --- メンテナンスマネージャー ---
	maintMgr := maintenance.NewManager(cfg.MaintenancePath)
	maintMgr.InitDefaults(cfg.MaintenanceReminders)
	fmt.Printf("✓ メンテナンスリマインダー: %d 項目\n", len(maintMgr.GetAll()))

	// --- トリップトラッカー ---
	var totalKmAccum float64
	tracker := trip.NewTracker(trip.TrackerConfig{
		ResetThresholdKm: cfg.ResetThreshold,
		OnTripComplete: func(data trip.TripData) {
			fmt.Printf("\n🏁 トリップ完了! 距離: %.1f km\n", data.DistanceKm)
			client.SendTrip(data)
		},
	})

	// --- 給油検出（エンジン始動時） ---
	checkRefueling(reader, tracker, client, cfg)

	// --- メンテナンス状態をGASに送信 ---
	sendMaintenanceStatus(client, maintMgr)

	// --- ローカルAPI + Web UI ---
	brightness := display.NewBrightnessController(cfg.Brightness)
	brightness.Start()
	defer brightness.Stop()
	go startLocalAPI(cfg, maintMgr)

	// --- メインループ（2層ポーリング） ---
	const fastIntervalMs = 150
	const fullEveryN = 5

	ticker := time.NewTicker(fastIntervalMs * time.Millisecond)
	defer ticker.Stop()

	retryTicker := time.NewTicker(5 * time.Minute)
	defer retryTicker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("\n▶ データ収集開始...")
	fmt.Printf("  高速ポーリング: %dms (RPM+速度+負荷+スロットル), 全PID: %dmsごと\n", fastIntervalMs, fastIntervalMs*fullEveryN)
	fmt.Printf("  LCD メーター: http://localhost:%d/meter.html\n", cfg.LocalAPIPort)

	sampleCount := 0
	var lastFuelTank float64
	for {
		select {
		case <-ticker.C:
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
				log.Printf("OBD読み取りエラー: %v", err)
				continue
			}

			dtSec := float64(fastIntervalMs) / 1000.0

			if isFull {
				lastFuelTank = data.FuelTankLevel
				tracker.UpdateFuelLevel(lastFuelTank)
			}

			tracker.Update(data.SpeedKmh)
			current := tracker.GetCurrent()
			totalKmAccum += (data.SpeedKmh / 3600.0) * dtSec
			maintMgr.UpdateTotalKm(totalKmAccum)

			dataMu.Lock()
			latestData = RealtimeData{
				SpeedKmh:    data.SpeedKmh,
				RPM:         data.RPM,
				EngineLoad:  data.EngineLoad,
				ThrottlePos: data.ThrottlePos,
				Trip:        &current,
				Alerts:      maintMgr.GetAlerts(),
			}
			dataMu.Unlock()

			if sampleCount%30 == 0 {
				fmt.Printf("\r🚗 %3.0f km/h | %4.0f rpm | %.1f km",
					data.SpeedKmh, data.RPM, current.DistanceKm)
			}

		case <-retryTicker.C:
			client.RetryPending()

		case sig := <-sigCh:
			fmt.Printf("\n\nシグナル受信 (%v)、シャットダウン...\n", sig)
			if completed := tracker.ManualReset(); completed != nil {
				client.SendTrip(*completed)
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

			// GASに給油データを送信
			client.Send("refuel", map[string]interface{}{
				"trip_id":         completed.TripID,
				"start_time":      completed.StartTime,
				"end_time":        completed.EndTime,
				"distance_km":     completed.DistanceKm,
				"fuel_used_l":     fuelUsedL,
				"fuel_economy":    fuelEcon,
				"refuel_amount_l": refuelAmountL,
				"old_level_pct":   lastPct,
				"new_level_pct":   currentPct,
				"max_speed_kmh":   completed.MaxSpeedKmh,
				"avg_speed_kmh":   completed.AvgSpeedKmh,
				"driving_time_sec": completed.DrivingTimeSec,
				"idle_time_sec":   completed.IdleTimeSec,
			})
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
func sendMaintenanceStatus(client *sender.Client, maintMgr *maintenance.Manager) {
	statuses := maintMgr.CheckAll()
	if len(statuses) == 0 {
		return
	}

	// 送信用にシンプルな構造に変換
	var items []map[string]interface{}
	for _, s := range statuses {
		item := map[string]interface{}{
			"id":           s.Reminder.ID,
			"name":         s.Reminder.Name,
			"type":         string(s.Reminder.Type),
			"progress":     s.Progress,
			"needs_alert":  s.NeedsAlert,
			"is_overdue":   s.IsOverdue,
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

	client.Send("maintenance", map[string]interface{}{
		"statuses": items,
		"sent_at":  time.Now(),
	})
	fmt.Printf("✓ メンテナンス状態送信: %d 項目\n", len(items))
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
