// pi-obd-meter はOBD-2対応車向けの車載メーターアプリケーション。
// ELM327経由で ECU からリアルタイムデータを取得し、5インチLCDに表示する。
// 走行距離・メンテナンス状態は Google Sheets (GAS Webhook) に自動送信する。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hashimoto/pi-obd-meter/internal/display"
	"github.com/hashimoto/pi-obd-meter/internal/obd"
)

var version = "dev"

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

// obdState はOBD接続とメインループの状態を管理する
type obdState struct {
	elm           *obd.ELM327
	reader        *obd.Reader
	connected     bool
	hasMAF        bool
	hasMAP        bool
	errCount      int
	sampleCount   int
	statusCount   int
	filters       obdFilters
	lastCoolant   float64
	lastMAP       float64
	lastFuelEco   float64
	lastFuelRate  float64
	wifiConnected bool
}

// tryConnect はELM327への接続を試みる
func (s *obdState) tryConnect() bool {
	if err := s.elm.Connect(); err != nil {
		slog.Warn("ELM327接続失敗", "error", err)
		return false
	}
	r := obd.NewReader(s.elm)
	if err := r.DetectCapabilities(); err != nil {
		slog.Warn("OBDケイパビリティ検出失敗", "error", err)
		_ = s.elm.Close()
		return false
	}
	s.reader = r
	s.hasMAF = r.HasMAF()
	s.hasMAP = r.HasMAP()
	s.connected = true
	slog.Info("ELM327接続完了")
	return true
}

// resetForReconnect は再接続時に状態をリセットする
func (s *obdState) resetForReconnect() {
	s.sampleCount = 0
	s.errCount = 0
	s.filters.ResetAll()
}

const maxConsecutiveErrors = 10

func main() {
	configPath := flag.String("config", "/etc/pi-obd-meter/config.json", "設定ファイルパス")
	flag.Parse()

	cfg := loadConfig(*configPath)

	fmt.Println("=================================")
	fmt.Printf("  DYデミオ 燃費メーター %s\n", version)
	fmt.Println("=================================")
	slog.Info("設定読み込み完了",
		"serial_port", cfg.SerialPort,
		"engine_displacement_l", cfg.EngineDisplacementL,
		"fuel_tank_l", cfg.FuelTankL,
		"fuel_rate_correction", cfg.FuelRateCorrection,
		"throttle_idle_pct", cfg.ThrottleIdlePct,
		"throttle_max_pct", cfg.ThrottleMaxPct,
		"max_speed_kmh", cfg.MaxSpeedKmh,
		"poll_interval_ms", cfg.PollIntervalMs,
		"local_api_port", cfg.LocalAPIPort,
		"obd_protocol", cfg.OBDProtocol,
	)

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
	slog.Info("ローカルAPI起動", "meter_url", fmt.Sprintf("http://localhost:%d/meter.html", cfg.LocalAPIPort))

	// --- WiFi接続後: GASから状態復元 + メンテナンス初回送信 ---
	go app.initializeFromGAS(ctx)

	// --- ELM327接続（失敗してもクラッシュしない） ---
	obs := &obdState{
		elm:     obd.NewELM327(cfg.SerialPort, cfg.OBDProtocol),
		filters: newOBDFilters(),
	}
	if !obs.tryConnect() {
		slog.Warn("OBD未接続、メーター表示のみで起動（バックグラウンドでリトライ）")
	}

	// --- メインループ ---
	// 2層ポーリング: 高速(デフォルト150ms)で RPM/速度/負荷/スロットル を取得し、
	// その5回に1回で水温・MAF等の全PIDを取得する。
	fastIntervalMs := cfg.PollIntervalMs
	if fastIntervalMs <= 0 {
		fastIntervalMs = 150
	}
	const fullEveryN = 5

	ticker := time.NewTicker(time.Duration(fastIntervalMs) * time.Millisecond)
	defer ticker.Stop()

	retryTicker := time.NewTicker(5 * time.Minute) // 送信失敗キューのリトライ間隔
	defer retryTicker.Stop()

	maintTicker := time.NewTicker(5 * time.Minute) // GASへのメンテナンス状態送信間隔
	defer maintTicker.Stop()

	obdRetryTicker := time.NewTicker(10 * time.Second) // OBD未接続時の再接続間隔
	defer obdRetryTicker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	slog.Info("データ収集開始")

	for {
		select {
		case <-ticker.C:
			if !obs.connected {
				// OBD未接続: 1秒ごとにステータスだけ更新
				obs.statusCount++
				if obs.statusCount%7 == 0 {
					obs.wifiConnected = checkWiFi()
					app.updateRealtimeData(RealtimeData{
						Alerts:        app.maintMgr.GetAlerts(),
						Notification:  app.getNotification(),
						OBDConnected:  false,
						WiFiConnected: obs.wifiConnected,
						PendingCount:  app.client.QueueSize(),
						SendSending:   app.client.IsSending(),
					})
				}
				continue
			}

			obs.sampleCount++
			isFull := obs.sampleCount%fullEveryN == 0

			var data *obd.OBDData
			var err error
			if isFull {
				data, err = obs.reader.ReadAll()
			} else {
				data, err = obs.reader.ReadFast()
			}
			if err != nil {
				obs.errCount++
				if obs.errCount >= maxConsecutiveErrors {
					slog.Warn("OBD接続ロスト、リトライ待ち", "consecutive_errors", obs.errCount)
					obs.connected = false
					_ = obs.elm.Close()
					obs.errCount = 0
				}
				continue
			}
			obs.errCount = 0

			// OBD値フィルタリング（ノイズ・スパイク除去）
			data.SpeedKmh = obs.filters.speed.Update(data.SpeedKmh)
			data.RPM = obs.filters.rpm.Update(data.RPM)
			data.EngineLoad = obs.filters.load.Update(data.EngineLoad)
			data.ThrottlePos = obs.filters.throttle.Update(data.ThrottlePos)
			if isFull {
				data.CoolantTemp = obs.filters.coolant.Update(data.CoolantTemp)
				if obs.hasMAP {
					data.IntakeMAP = obs.filters.mapKPa.Update(data.IntakeMAP)
				}
			}

			// 累計走行距離の積算（トリップとは別にメンテナンス用に独立管理）
			dtSec := float64(fastIntervalMs) / 1000.0

			if isFull {
				obs.lastCoolant = data.CoolantTemp
				obs.lastMAP = data.IntakeMAP
				obs.wifiConnected = checkWiFi()
				obs.lastFuelEco, obs.lastFuelRate = calcFuelEconomy(data.SpeedKmh, data.RPM, data.EngineLoad, data.MAFAirFlow, obs.hasMAF, data.IntakeMAP, obs.hasMAP, cfg.EngineDisplacementL, cfg.FuelRateCorrection)
			}

			// エンブレ(燃料カット)時は燃料消費ゼロとしてトラッカーに渡す
			trackerFuelRate := obs.lastFuelRate
			if obs.lastFuelEco < 0 {
				trackerFuelRate = 0
			}
			app.tracker.Update(data.SpeedKmh, trackerFuelRate)
			app.addDistance((data.SpeedKmh / 3600.0) * dtSec)

			app.updateRealtimeData(RealtimeData{
				SpeedKmh:       data.SpeedKmh,
				RPM:            data.RPM,
				EngineLoad:     data.EngineLoad,
				ThrottlePos:    data.ThrottlePos,
				FuelEconomy:    obs.lastFuelEco,
				FuelRateLH:     obs.lastFuelRate,
				AvgFuelEconomy: app.tracker.AvgFuelEconomy(),
				TripKm:         app.tracker.DistanceKm(),
				CoolantTemp:    obs.lastCoolant,
				IntakeMAP:      obs.lastMAP,
				Alerts:         app.maintMgr.GetAlerts(),
				Notification:   app.getNotification(),
				OBDConnected:   true,
				WiFiConnected:  obs.wifiConnected,
				PendingCount:   app.client.QueueSize(),
			})

			if obs.sampleCount%30 == 0 {
				fmt.Printf("\r🚗 %3.0f km/h | %4.0f rpm",
					data.SpeedKmh, data.RPM)
			}

		case <-obdRetryTicker.C:
			if !obs.connected {
				if obs.tryConnect() {
					obs.resetForReconnect()
				}
			}

		case <-retryTicker.C:
			app.client.RetryPending(ctx)

		case <-maintTicker.C:
			app.sendMaintenanceStatus(ctx)

		case sig := <-sigCh:
			slog.Info("シグナル受信、シャットダウン開始", "signal", sig)
			cancel() // HTTP サーバーと goroutine にキャンセルを通知
			done := make(chan struct{})
			go func() {
				app.tracker.SaveState()
				app.maintMgr.SaveState()
				close(done)
			}()
			select {
			case <-done:
				slog.Info("状態保存完了")
			case <-time.After(5 * time.Second):
				slog.Warn("状態保存タイムアウト（5秒）")
			}
			if obs.connected {
				_ = obs.elm.Close()
			}
			return
		}
	}
}
