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
			if obdConnected {
				elm.Close()
			}
			return
		}
	}
}
