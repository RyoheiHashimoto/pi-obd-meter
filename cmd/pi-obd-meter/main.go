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

// GearRatio はギア比定義
type GearRatio struct {
	Gear  int     `json:"gear"`
	Ratio float64 `json:"ratio"`
}

// Config はアプリケーション設定
type Config struct {
	SerialPort           string                   `json:"serial_port"`
	WebhookURL           string                   `json:"webhook_url"`
	DiscordWebhook       string                   `json:"discord_webhook"`
	PollIntervalMs       int                      `json:"poll_interval_ms"`
	ResetThreshold       float64                  `json:"reset_threshold_km"`
	LocalAPIPort         int                      `json:"local_api_port"`
	MaintenancePath      string                   `json:"maintenance_path"`
	WebStaticDir         string                   `json:"web_static_dir"`
	EngineDisplacementCC float64                  `json:"engine_displacement_cc"`
	VECoefficient        float64                  `json:"ve_coefficient"`
	ThermalEfficiency    float64                  `json:"thermal_efficiency"`
	RedlineRPM           int                      `json:"redline_rpm"`
	MaxSpeedKmh          int                      `json:"max_speed_kmh"`
	MaxRPM               int                      `json:"max_rpm"`
	PowerMaxPS           int                      `json:"power_max_ps"`
	TorqueMaxKgfm        float64                  `json:"torque_max_kgfm"`
	OBDProtocol          string                   `json:"obd_protocol"`
	GearRatios           []GearRatio              `json:"gear_ratios"`
	MaintenanceReminders []maintenance.Reminder   `json:"maintenance_reminders"`
	Brightness           display.BrightnessConfig `json:"brightness"`
}

// RealtimeData はリアルタイムAPIのレスポンス（LCD用）
type RealtimeData struct {
	SpeedKmh    float64              `json:"speed_kmh"`
	RPM         float64              `json:"rpm"`
	InstantEcon float64              `json:"instant_econ"`
	FuelRateLph float64              `json:"fuel_rate_lph"`
	CoolantTemp float64              `json:"coolant_temp"`
	FuelTank    float64              `json:"fuel_tank_pct"`
	EstPowerKW  float64              `json:"est_power_kw"`
	EstPowerPS  float64              `json:"est_power_ps"`
	EstTorqueNm float64              `json:"est_torque_nm"`
	EngineLoad  float64              `json:"engine_load"`
	Trip        *trip.TripData       `json:"trip"`
	Alerts      []maintenance.Status `json:"alerts"`
	DTCs        *obd.DTCResult       `json:"dtcs,omitempty"`
}

var version = "dev"

var (
	latestData RealtimeData
	latestDTCs *obd.DTCResult
	dataMu     sync.RWMutex
)

func loadConfig(path string) Config {
	cfg := Config{
		SerialPort:           "/dev/rfcomm0",
		WebhookURL:           "", // Google Apps Script Webhook URL
		DiscordWebhook:       "",
		PollIntervalMs:       500,
		ResetThreshold:       0.5,
		LocalAPIPort:         9090,
		MaintenancePath:      "/var/lib/pi-obd-meter/maintenance.json",
		WebStaticDir:         "/opt/pi-obd-meter/web/static",
		EngineDisplacementCC: 1348, // ZJ-VE 1.3L
		VECoefficient:        0.85, // 体積効率、満タン法で校正
		ThermalEfficiency:    0.28, // 初期値、全開加速で校正
		RedlineRPM:           6500, // ZJ-VE レッドゾーン開始
		MaxSpeedKmh:          180,
		MaxRPM:               8000,
		OBDProtocol:          "6", // CAN 11bit 500kbaud (ISO 15765-4)
		PowerMaxPS:           100,
		TorqueMaxKgfm:        15,
		GearRatios: []GearRatio{
			{Gear: 1, Ratio: 105.2},
			{Gear: 2, Ratio: 57.1},
			{Gear: 3, Ratio: 37.4},
			{Gear: 4, Ratio: 26.4},
		},
		Brightness: display.DefaultConfig(),
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
	fmt.Printf("送信先: Google Sheets (GAS Webhook)\n")

	// --- ELM327接続 ---
	elm := obd.NewELM327(cfg.SerialPort, cfg.OBDProtocol)
	if err := elm.Connect(); err != nil {
		log.Fatalf("ELM327接続失敗: %v", err)
	}
	defer elm.Close()
	fmt.Println("✓ ELM327接続完了")

	// --- PID検出 ---
	reader := obd.NewReader(elm, obd.EngineConfig{
		DisplacementL:        cfg.EngineDisplacementCC / 1000.0,
		ThermalEfficiency:    cfg.ThermalEfficiency,
		VolumetricEfficiency: cfg.VECoefficient,
	})
	if err := reader.DetectCapabilities(); err != nil {
		log.Fatalf("OBDケイパビリティ検出失敗: %v", err)
	}

	// --- 送信クライアント（Google Sheets） ---
	client := sender.NewClient(cfg.WebhookURL)
	// リトライはメモリキュー（overlayFSのためファイル保存しない）

	// --- メンテナンスマネージャー ---
	maintMgr := maintenance.NewManager(cfg.MaintenancePath)
	maintMgr.InitDefaults(cfg.MaintenanceReminders)
	fmt.Printf("✓ メンテナンスリマインダー: %d 項目\n", len(maintMgr.GetAll()))

	// --- トリップトラッカー ---
	var totalKmAccum float64
	tracker := trip.NewTracker(trip.TrackerConfig{
		ResetThresholdKm: cfg.ResetThreshold,
		OnTripComplete: func(data trip.TripData) {
			fmt.Printf("\n🏁 トリップ完了!\n")
			fmt.Printf("   距離: %.1f km | 燃費: %.1f km/L | 燃料: %.2f L\n",
				data.DistanceKm, data.AvgFuelEconKm, data.FuelUsedL)

			// Google Sheetsに送信（GAS側でDiscord通知も行う）
			client.SendTrip(data)
		},
	})

	// --- ローカルAPI + Web UI ---
	brightness := display.NewBrightnessController(cfg.Brightness)
	brightness.Start()
	defer brightness.Stop()
	go startLocalAPI(cfg, tracker, maintMgr, brightness)

	// --- メインループ ---
	pollInterval := time.Duration(cfg.PollIntervalMs) * time.Millisecond
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	retryTicker := time.NewTicker(5 * time.Minute)
	defer retryTicker.Stop()

	// DTC（故障コード）チェック: 起動時 + 1分ごと
	dtcTicker := time.NewTicker(1 * time.Minute)
	defer dtcTicker.Stop()
	go func() {
		// 起動時チェック
		if result, err := elm.ReadDTCs(); err == nil {
			dataMu.Lock()
			latestDTCs = result
			dataMu.Unlock()
			if result.MIL {
				fmt.Printf("⚠ チェックランプ点灯中: %d件の故障コード\n", len(result.Codes))
				for _, dtc := range result.Codes {
					fmt.Printf("  %s: %s\n", dtc.Code, dtc.Description)
				}
			} else {
				fmt.Println("✓ 故障コードなし")
			}
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("\n▶ データ収集開始...")
	fmt.Printf("  LCD メーター: http://localhost:%d/meter.html\n", cfg.LocalAPIPort)
	fmt.Printf("  スマホ操作:   http://<raspi-ip>:%d/control.html\n", cfg.LocalAPIPort)

	sampleCount := 0
	for {
		select {
		case <-ticker.C:
			data, err := reader.ReadAll()
			if err != nil {
				log.Printf("OBD読み取りエラー: %v", err)
				continue
			}

			fuelRate := data.CalcFuelRateLph()
			instantEcon := data.CalcInstantFuelEconomy()
			tracker.Update(data.SpeedKmh, fuelRate)

			current := tracker.GetCurrent()
			dtSec := float64(cfg.PollIntervalMs) / 1000.0
			totalKmAccum += (data.SpeedKmh / 3600.0) * dtSec
			maintMgr.UpdateTotalKm(totalKmAccum)

			// リアルタイムデータ更新（LCD & スマホ用）
			dataMu.Lock()
			latestData = RealtimeData{
				SpeedKmh:    data.SpeedKmh,
				RPM:         data.RPM,
				InstantEcon: instantEcon,
				FuelRateLph: fuelRate,
				CoolantTemp: data.CoolantTemp,
				FuelTank:    data.FuelTankLevel,
				EstPowerKW:  data.CalcEstimatedPowerKW(),
				EstPowerPS:  data.CalcEstimatedPowerPS(),
				EstTorqueNm: data.CalcEstimatedTorqueNm(),
				EngineLoad:  data.EngineLoad,
				Trip:        &current,
				Alerts:      maintMgr.GetAlerts(),
				DTCs:        latestDTCs,
			}
			dataMu.Unlock()

			sampleCount++
			if sampleCount%10 == 0 {
				fmt.Printf("\r🚗 %3.0f km/h | ⛽ %4.1f km/L (avg %4.1f) | %.1f km | %.2f L",
					data.SpeedKmh, instantEcon, current.AvgFuelEconKm,
					current.DistanceKm, current.FuelUsedL)
			}

		case <-retryTicker.C:
			client.RetryPending()

		case <-dtcTicker.C:
			// 1分ごとのDTCチェック（ポーリングの合間に実行）
			if result, err := elm.ReadDTCs(); err == nil {
				dataMu.Lock()
				latestDTCs = result
				dataMu.Unlock()
			}

		case sig := <-sigCh:
			fmt.Printf("\n\nシグナル受信 (%v)、シャットダウン...\n", sig)
			if completed := tracker.ManualReset(); completed != nil {
				client.SendTrip(*completed)
			}
			return
		}
	}
}

// corsMiddleware はCORSヘッダーを付与する
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

func startLocalAPI(cfg Config, tracker *trip.Tracker, maintMgr *maintenance.Manager, brightness *display.BrightnessController) {
	mux := http.NewServeMux()

	// --- Web UI配信 ---
	// LCD: http://localhost:9090/meter.html
	// スマホ: http://<raspi-ip>:9090/control.html
	mux.Handle("GET /", http.FileServer(http.Dir(cfg.WebStaticDir)))

	// --- 設定API（meter.html等がredline_rpm等を取得する） ---
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"redline_rpm":            cfg.RedlineRPM,
			"engine_displacement_cc": cfg.EngineDisplacementCC,
			"max_speed_kmh":          cfg.MaxSpeedKmh,
			"max_rpm":                cfg.MaxRPM,
			"power_max_ps":           cfg.PowerMaxPS,
			"torque_max_kgfm":        cfg.TorqueMaxKgfm,
			"gear_ratios":            cfg.GearRatios,
		})
	})

	// --- リアルタイムAPI（LCD用、500ms間隔でポーリングされる） ---
	mux.HandleFunc("GET /api/realtime", func(w http.ResponseWriter, r *http.Request) {
		dataMu.RLock()
		defer dataMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(latestData)
	})

	// --- トリップAPI ---
	mux.HandleFunc("GET /api/current", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tracker.GetCurrent())
	})

	mux.HandleFunc("POST /api/reset", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		completed := tracker.ManualReset()
		if completed != nil {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "reset", "completed": completed,
			})
		} else {
			json.NewEncoder(w).Encode(map[string]string{"status": "no_data"})
		}
	})

	// --- メンテナンスAPI ---
	mux.HandleFunc("GET /api/maintenance", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(maintMgr.CheckAll())
	})

	mux.HandleFunc("POST /api/maintenance/{id}/reset", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		w.Header().Set("Content-Type", "application/json")
		if err := maintMgr.ResetReminder(id); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "id": id})
	})

	// --- 故障コード（DTC）API ---
	mux.HandleFunc("GET /api/dtc", func(w http.ResponseWriter, r *http.Request) {
		dataMu.RLock()
		defer dataMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		if latestDTCs != nil {
			json.NewEncoder(w).Encode(latestDTCs)
		} else {
			json.NewEncoder(w).Encode(map[string]string{"status": "未チェック"})
		}
	})

	// --- 輝度API ---
	mux.HandleFunc("GET /api/brightness", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(brightness.Status())
	})

	mux.HandleFunc("POST /api/brightness", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Brightness float64 `json:"brightness"` // 0.0〜1.0
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "不正なリクエスト", http.StatusBadRequest)
			return
		}
		brightness.SetManual(req.Brightness)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(brightness.Status())
	})

	mux.HandleFunc("POST /api/brightness/auto", func(w http.ResponseWriter, r *http.Request) {
		brightness.ClearManual()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(brightness.Status())
	})

	addr := fmt.Sprintf(":%d", cfg.LocalAPIPort)
	log.Printf("ローカルAPI起動: %s", addr)
	http.ListenAndServe(addr, corsMiddleware(mux))
}
