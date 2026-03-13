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

	// --- OBD読み取り goroutine 起動 ---
	fastIntervalMs := cfg.PollIntervalMs
	if fastIntervalMs <= 0 {
		fastIntervalMs = 150
	}
	elm := obd.NewELM327(cfg.SerialPort, cfg.OBDProtocol)
	obdCh := make(chan OBDEvent, 1)
	go obdReaderLoop(ctx, elm, fastIntervalMs, obdCh)

	// --- メインループ ---
	retryTicker := time.NewTicker(5 * time.Minute) // 送信失敗キューのリトライ間隔
	defer retryTicker.Stop()

	maintTicker := time.NewTicker(5 * time.Minute) // GASへのメンテナンス状態送信間隔
	defer maintTicker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	slog.Info("データ収集開始")

	// OBDメインループ状態（フィルタリング・燃費計算はメイン側で管理）
	var (
		filters        = newOBDFilters()
		lastCoolant    float64
		lastMAP        float64
		lastFuelEco    float64
		lastFuelRate   float64
		displayFuelEco float64 // 表示用瞬間燃費（ReadFastで毎サイクル更新）
		wifiConnected  bool
		wasConnected   bool
		lastReadAt     time.Time
		sampleCount    int
	)

	for {
		select {
		case ev, ok := <-obdCh:
			if !ok {
				// goroutine が終了した（ctx キャンセル時）
				return
			}

			// 再接続検出: フィルターリセット
			if ev.Connected && !wasConnected {
				filters.ResetAll()
				lastCoolant = 0
				lastMAP = 0
				lastFuelEco = 0
				lastFuelRate = 0
				displayFuelEco = 0
			}
			wasConnected = ev.Connected

			if !ev.Connected || ev.Data == nil {
				// OBD未接続: ステータスだけ更新
				wifiConnected = checkWiFi()
				app.updateRealtimeData(RealtimeData{
					Alerts:        app.maintMgr.GetAlerts(),
					Notification:  app.getNotification(),
					OBDConnected:  false,
					WiFiConnected: wifiConnected,
					PendingCount:  app.client.QueueSize(),
					SendSending:   app.client.IsSending(),
				})
				continue
			}

			data := ev.Data
			sampleCount++

			// OBD値フィルタリング（ノイズ・スパイク除去）
			// ReadFast は速度+スロットルのみ返すため、RPM/load は IsFull 時のみフィルタリング
			data.SpeedKmh = filters.speed.Update(data.SpeedKmh)
			data.ThrottlePos = filters.throttle.Update(data.ThrottlePos)
			if ev.IsFull {
				data.RPM = filters.rpm.Update(data.RPM)
				data.EngineLoad = filters.load.Update(data.EngineLoad)
				data.CoolantTemp = filters.coolant.Update(data.CoolantTemp)
				if ev.HasMAP {
					data.IntakeMAP = filters.mapKPa.Update(data.IntakeMAP)
				}
			}

			// 実測 dtSec（前回読み取りからの経過時間）
			dtSec := float64(fastIntervalMs) / 1000.0
			if !lastReadAt.IsZero() {
				dtSec = ev.ReadAt.Sub(lastReadAt).Seconds()
			}
			lastReadAt = ev.ReadAt

			if ev.IsFull {
				lastCoolant = data.CoolantTemp
				lastMAP = data.IntakeMAP
				wifiConnected = checkWiFi()
				lastFuelEco, lastFuelRate = calcFuelEconomy(data.SpeedKmh, data.RPM, data.EngineLoad, data.MAFAirFlow, ev.HasMAF, data.IntakeMAP, ev.HasMAP, cfg.EngineDisplacementL, cfg.FuelRateCorrection)
				displayFuelEco = lastFuelEco
			} else {
				// ReadFast: 表示用瞬間燃費を速度+スロットル+直近燃料レートから更新
				displayFuelEco = calcDisplayFuelEco(data.SpeedKmh, data.ThrottlePos, lastFuelRate, cfg.ThrottleIdlePct)
			}

			// エンブレ(燃料カット)時は燃料消費ゼロとしてトラッカーに渡す
			trackerFuelRate := lastFuelRate
			if lastFuelEco < 0 {
				trackerFuelRate = 0
			}
			app.tracker.Update(data.SpeedKmh, trackerFuelRate)
			app.addDistance((data.SpeedKmh / 3600.0) * dtSec)

			app.updateRealtimeData(RealtimeData{
				SpeedKmh:       data.SpeedKmh,
				RPM:            data.RPM,
				EngineLoad:     data.EngineLoad,
				ThrottlePos:    data.ThrottlePos,
				FuelEconomy:    displayFuelEco,
				FuelRateLH:     lastFuelRate,
				AvgFuelEconomy: app.tracker.AvgFuelEconomy(),
				TripKm:         app.tracker.DistanceKm(),
				CoolantTemp:    lastCoolant,
				IntakeMAP:      lastMAP,
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

		case <-retryTicker.C:
			app.client.RetryPending(ctx)

		case <-maintTicker.C:
			app.sendMaintenanceStatus(ctx)

		case sig := <-sigCh:
			slog.Info("シグナル受信、シャットダウン開始", "signal", sig)
			cancel() // obdReaderLoop + HTTP サーバーにキャンセルを通知
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
			return
		}
	}
}
