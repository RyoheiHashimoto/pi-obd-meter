package main

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/hashimoto/pi-obd-meter/internal/can"
	"github.com/hashimoto/pi-obd-meter/internal/obd"
)

// canReaderLoop はCAN-BUSパッシブモニタリング + OBD-2クエリによるデータ取得ループ。
//
// パッシブ受信（毎フレーム ~20ms）:
//   - 0x201: RPM, 車速, エンジン負荷
//   - 0x430: 大気圧, 電圧
//   - 0x4B0: 4輪速度
//
// OBD-2クエリ（1秒間隔）:
//   - スロットル開度 (PID 0x11)
//   - 冷却水温 (PID 0x05)
//   - インマニ圧 MAP (PID 0x0B)
func canReaderLoop(ctx context.Context, ifname string, intervalMs int, ch chan<- OBDEvent) {
	defer close(ch)

	if intervalMs <= 0 {
		intervalMs = 200
	}

	const (
		reconnectInterval = 10 * time.Second
		staleTimeout      = 3 * time.Second // CAN無通信でエンジンOFF判定
		obdQueryInterval  = 4               // OBDクエリは N tick ごと（N×intervalMs）
	)

	// OBD-2クエリ対象PID（ラウンドロビンで1 tickに1 PIDずつ送信）
	obdPIDs := []byte{
		obd.PIDThrottlePosition, // 0x11
		obd.PIDMAFAirFlow,       // 0x10
		obd.PIDThrottlePosition, // 0x11
		obd.PIDIntakeMAP,        // 0x0B
		obd.PIDThrottlePosition, // 0x11
		obd.PIDO2SensorB1S1,     // 0x14
		obd.PIDThrottlePosition, // 0x11
		obd.PIDShortFuelTrim,    // 0x06
		obd.PIDThrottlePosition, // 0x11
		obd.PIDTimingAdvance,    // 0x0E
		obd.PIDThrottlePosition, // 0x11
		obd.PIDIntakeAirTemp,    // 0x0F
	}

	// CAN接続を試みる（interface DOWN の場合は UP にし直す）
	connect := func() *can.Socket {
		// interface が DOWN の場合に備えて UP を試みる
		_ = exec.Command("ip", "link", "set", ifname, "down").Run()
		_ = exec.Command("ip", "link", "set", ifname, "type", "can", "bitrate", "500000", "restart-ms", "100").Run()
		_ = exec.Command("ip", "link", "set", ifname, "up").Run()

		sock, err := can.Open(ifname)
		if err != nil {
			slog.Warn("CAN接続失敗", "interface", ifname, "error", err)
			return nil
		}
		slog.Info("CAN接続完了", "interface", ifname)
		return sock
	}

	sock := connect()
	if sock == nil {
		slog.Warn("CAN未接続、メーター表示のみで起動（バックグラウンドでリトライ）")
	}

	// 最新値の保持（CANフレーム受信ごとに更新）
	var (
		mu            sync.Mutex
		rpm           float64
		speedKmh      float64
		engineLoad    float64
		throttlePos   float64
		coolantTemp   float64
		intakeMAP     float64
		baroKPa       float64
		voltage       float64
		fuelLevel     float64
		ambientTemp   float64
		mafAirFlow    float64
		shortFuelTrim float64
		longFuelTrim  float64
		timingAdvance float64
		intakeAirTemp float64
		o2Voltage     float64
		runtimeSec    int
		gear          int
		atRange       can.ATRange
		hold          bool
		tcLocked      bool
		shifting      bool
		kickdown      bool
		hasMAF        bool
		hasMAP        bool
		hasData       bool
		lastFrameTime time.Time
	)

	// CANフレーム読み取りgoroutine
	var frameWg sync.WaitGroup
	readerDead := make(chan struct{}, 1) // リーダー死亡通知

	startReader := func(s *can.Socket) {
		frameWg.Add(1)
		go func() {
			defer frameWg.Done()
			defer func() {
				select {
				case readerDead <- struct{}{}:
				default:
				}
			}()
			for {
				frame, err := s.ReadFrame()
				if err != nil {
					if errors.Is(err, can.ErrTimeout) {
						if ctx.Err() != nil {
							return
						}
						continue
					}
					if ctx.Err() != nil {
						return
					}
					slog.Warn("CANフレーム読み取りエラー", "error", err)
					return
				}

				mu.Lock()
				switch frame.ID {
				case can.IDEngine:
					rpm, speedKmh, engineLoad = can.DecodeEngine(frame.Data)
					hasData = true
					lastFrameTime = time.Now()
				case can.IDATCtrl:
					gear, tcLocked, _ = can.DecodeATCtrl(frame.Data)
					lastFrameTime = time.Now()
				case can.IDATStatus:
					_, atRange, hold, shifting, kickdown = can.DecodeATStatus(frame.Data)
					lastFrameTime = time.Now()
				case can.IDCoolant:
					ct, _ := can.DecodeCoolant(frame.Data)
					coolantTemp = ct
					lastFrameTime = time.Now()
				case can.IDElectric:
					_, voltage, baroKPa = can.DecodeElectric(frame.Data)
					lastFrameTime = time.Now()
				case can.IDWheels:
					lastFrameTime = time.Now()
				case can.IDOBDResponse:
					// OBD-2 レスポンス処理
					if pid, data, ok := can.ParseOBDResponse(frame); ok {
						switch pid {
						case obd.PIDThrottlePosition:
							if len(data) >= 1 {
								throttlePos = float64(data[0]) * 100.0 / 255.0
							}
						case obd.PIDCoolantTemp:
							if len(data) >= 1 {
								coolantTemp = float64(data[0]) - 40.0
							}
						case obd.PIDIntakeMAP:
							if len(data) >= 1 {
								intakeMAP = float64(data[0])
								hasMAP = true
							}
						case obd.PIDMAFAirFlow:
							if len(data) >= 2 {
								mafAirFlow = float64(uint16(data[0])<<8|uint16(data[1])) / 100.0
								hasMAF = true
							}
						case obd.PIDShortFuelTrim:
							if len(data) >= 1 {
								shortFuelTrim = (float64(data[0]) - 128) * 100 / 128
							}
						case obd.PIDLongFuelTrim:
							if len(data) >= 1 {
								longFuelTrim = (float64(data[0]) - 128) * 100 / 128
							}
						case obd.PIDTimingAdvance:
							if len(data) >= 1 {
								timingAdvance = float64(data[0])/2 - 64
							}
						case obd.PIDIntakeAirTemp:
							if len(data) >= 1 {
								intakeAirTemp = float64(data[0]) - 40.0
							}
						case obd.PIDO2SensorB1S1:
							if len(data) >= 1 {
								o2Voltage = float64(data[0]) * 0.005
							}
						case obd.PIDRuntime:
							if len(data) >= 2 {
								runtimeSec = int(uint16(data[0])<<8 | uint16(data[1]))
							}
						case obd.PIDFuelLevel:
							if len(data) >= 1 {
								fuelLevel = float64(data[0]) * 100.0 / 255.0
							}
						case obd.PIDAmbientTemp:
							if len(data) >= 1 {
								ambientTemp = float64(data[0]) - 40.0
							}
						}
					}
				}
				mu.Unlock()
			}
		}()
	}

	if sock != nil {
		startReader(sock)
	}

	ticker := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
	defer ticker.Stop()

	reconnectTicker := time.NewTicker(reconnectInterval)
	defer reconnectTicker.Stop()

	tickCount := 0

	for {
		select {
		case <-ctx.Done():
			if sock != nil {
				_ = sock.Close()
				frameWg.Wait()
			}
			return

		case <-ticker.C:
			tickCount++

			if sock == nil {
				// ソケット未接続でも1秒ごとに切断状態を通知（UI更新用）
				if tickCount%(1000/intervalMs) == 0 {
					select {
					case ch <- OBDEvent{Connected: false, ReadAt: time.Now()}:
					case <-ctx.Done():
						return
					}
				}
				continue
			}

			// OBD-2クエリ送信（1 tick に 1 PID、ラウンドロビン）
			pidIdx := tickCount % len(obdPIDs)
			_ = sock.WriteFrame(can.OBDRequestFrame(obdPIDs[pidIdx]))

			mu.Lock()
			if !hasData {
				mu.Unlock()
				select {
				case ch <- OBDEvent{Connected: false, ReadAt: time.Now()}:
				case <-ctx.Done():
					_ = sock.Close()
					frameWg.Wait()
					return
				}
				continue
			}

			// CAN無通信チェック（エンジンOFF検出）
			stale := time.Since(lastFrameTime) > staleTimeout
			if stale {
				mu.Unlock()
				select {
				case ch <- OBDEvent{Connected: false, ReadAt: time.Now()}:
				case <-ctx.Done():
					_ = sock.Close()
					frameWg.Wait()
					return
				}
				continue
			}

			// CAN直結では全データが常時取得可能なため常にIsFull
			isFull := true
			data := &obd.OBDData{
				RPM:           rpm,
				SpeedKmh:      speedKmh,
				EngineLoad:    engineLoad,
				ThrottlePos:   throttlePos,
				CoolantTemp:   coolantTemp,
				IntakeMAP:     intakeMAP,
				MAFAirFlow:    mafAirFlow,
				Voltage:       voltage,
				FuelLevel:     fuelLevel,
				AmbientTemp:   ambientTemp,
				ShortFuelTrim: shortFuelTrim,
				LongFuelTrim:  longFuelTrim,
				TimingAdvance: timingAdvance,
				IntakeAirTemp: intakeAirTemp,
				O2Voltage:     o2Voltage,
				RuntimeSec:    runtimeSec,
				Gear:          gear,
				ATRange:       int(atRange),
				Hold:          hold,
				TCLocked:      tcLocked,
				Shifting:      shifting,
				Kickdown:      kickdown,
				HasMAF:        hasMAF,
			}
			currentHasMAP := hasMAP
			_ = baroKPa     // 将来使用（燃費補正等）
			_ = longFuelTrim // 将来使用（燃費補正等）
			mu.Unlock()

			select {
			case ch <- OBDEvent{
				Data:      data,
				IsFull:    isFull,
				Connected: true,
				HasMAF:    false,
				HasMAP:    currentHasMAP,
				ReadAt:    time.Now(),
			}:
			case <-ctx.Done():
				_ = sock.Close()
				frameWg.Wait()
				return
			}

		case <-readerDead:
			// リーダーgoroutineが死亡 → ソケットを閉じて再接続を促す
			slog.Warn("CANリーダー停止、再接続待機")
			if sock != nil {
				frameWg.Wait()
				_ = sock.Close()
				sock = nil
			}
			mu.Lock()
			hasData = false
			mu.Unlock()

		case <-reconnectTicker.C:
			if sock != nil {
				continue
			}
			sock = connect()
			if sock != nil {
				startReader(sock)
				select {
				case ch <- OBDEvent{Connected: true, ReadAt: time.Now()}:
				case <-ctx.Done():
					_ = sock.Close()
					frameWg.Wait()
					return
				}
			} else {
				select {
				case ch <- OBDEvent{Connected: false, ReadAt: time.Now()}:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}
