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
		staleTimeout      = 1 * time.Second // エンジン ECU (IDEngine 100Hz) 無通信で OFF 判定
		obdQueryInterval  = 4               // OBDクエリは N tick ごと（N×intervalMs）
	)

	// OBD-2クエリ対象PID（ラウンドロビンで1 tickに1 PIDずつ送信）
	// CAN直結モードでは速度・RPM・負荷・水温はCAN受信、OBDで追加取得するもの:
	// 0x2F (燃料残量)、0x46 (外気温) は DY ZJ-VE 非対応確認済のため削除。
	obdPIDs := []byte{
		obd.PIDMAFAirFlow, // 0x10 — MAF (燃費計算)
		obd.PIDIntakeMAP,  // 0x0B — MAP (バキューム計、燃費計算)
	}

	// 距離パルスの累積カウンタ。CAN再接続のたびに基準値を捨てる。
	var pulseCounter can.PulseCounter
	// トルコン滑りの校正器。ロックアップ中のサンプルから k を学習する。
	slipCal := can.NewSlipCalibrator()
	// 変速中に保持するロック率と滑り比
	var lastLockPct float64
	var lastSlip float64

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
		// 断絶中に進んだパルスは追えないため、基準値を捨てる。
		// 累積値は保持されるので、失われるのは断絶中の距離だけ。
		pulseCounter.Invalidate()
		return sock
	}

	sock := connect()
	if sock == nil {
		slog.Warn("CAN未接続、メーター表示のみで起動（バックグラウンドでリトライ）")
	}

	// 最新値の保持（CANフレーム受信ごとに更新）
	var (
		mu            sync.Mutex
		atfTempC      float64
		hasATF        bool
		rpm           float64
		speedKmh      float64
		engineLoad    float64
		wheelSpeedKmh float64
		coolantTemp   float64
		intakeMAP     float64
		odometerCANKm float64
		elecB0Pct     float64
		elecB1Raw     float64
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
		gearRatio     float64
		atRange       can.ATRange
		hold          bool
		tcLocked      bool
		shifting      bool
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
				// lastFrameTime はエンジン ECU (IDEngine) フレームのみで更新
				// (エンジン OFF 後も他 ECU が 10秒以上信号送り続けるため、エンジン停止判定を遅らせないように)
				switch frame.ID {
				case can.IDEngine:
					rpm, speedKmh, engineLoad = can.DecodeEngine(frame.Data)
					hasData = true
					lastFrameTime = time.Now()
				case can.IDATCtrl:
					gear, gearRatio = can.DecodeATCtrl(frame.Data)
				case can.IDATStatus:
					_, atRange, hold, tcLocked, shifting = can.DecodeATStatus(frame.Data)
				case can.IDCoolant:
					ct, pulse := can.DecodeCoolant(frame.Data)
					coolantTemp = ct
					// 距離パルス (8bit ローリング) を累積する。
					// 車速の積分と違い計数なので誤差が蓄積しない。
					pulseCounter.Add(pulse)
				case can.IDElectric:
					elecB0Pct, elecB1Raw, odometerCANKm = can.DecodeElectric(frame.Data)
				case can.IDWheels:
					wheelSpeedKmh = can.DecodeWheelSpeed(frame.Data)
				case can.IDOBDResponse:
					// Mode 22 (拡張診断データ) の応答。ATF油温はここから来る。
					if pid22, data, ok := can.ParseOBDResponse22(frame); ok {
						if pid22 == can.PID22ATFTemp {
							if t, ok := can.DecodeATFTemp(data); ok {
								atfTempC = t
								hasATF = true
							}
						}
					}
					// OBD-2 レスポンス処理
					if pid, data, ok := can.ParseOBDResponse(frame); ok {
						switch pid {
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
						case obd.PIDControlModuleV:
							// ECU 電源電圧: ((A*256)+B)/1000 V（OBD-2 規格）
							if len(data) >= 2 {
								voltage = float64(uint16(data[0])<<8|uint16(data[1])) / 1000.0
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
				// ソケット未接続でも 100ms ごとに切断状態を通知 (UI 移行 smooth 化)
				if tickCount%max(1, 100/intervalMs) == 0 {
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

			// 電圧は高頻度不要のため 1 秒周期の別枠で問い合わせる。
			// 高速ローテーション (MAF/MAP) の更新周期を落とさないための措置。
			if tickCount%max(1, 1000/intervalMs) == 0 {
				_ = sock.WriteFrame(can.OBDRequestFrame(obd.PIDControlModuleV))
			}

			// ATF油温 (Mode 22)。油は熱容量が大きく分解能も1℃しかないため、
			// 2秒に1回で十分。実測では停車4分間まったく動かなかった。
			if tickCount%max(1, 2000/intervalMs) == 0 {
				_ = sock.WriteFrame(can.OBDRequestFrame22(can.PID22ATFTemp))
			}

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

			// 4輪平均車速を使用（0x4B0 から取得、CAN直読み）
			// 0x201 の speedKmh より正確（従動輪含む4輪平均）
			currentSpeed := wheelSpeedKmh
			if currentSpeed < 0.1 {
				currentSpeed = speedKmh // フォールバック
			}

			// ロック率計算: rpm と車速から実際の滑りを求める。
			// 0x230 B2 のギア比には滑りが含まれないため使えない (#132)。
			//
			// 校正定数はロックアップ係合中のサンプルから自動学習するので、
			// タイヤ周長や最終減速比を定数で持つ必要がない。
			// 滑り比は実際に噛んでいるギアで計算する。
			//
			// gear (0x231) は「これから入れる目標ギア」で、実際のギアとは
			// 限らない。95km/h で S レンジに入れると表示は即 2速 になるが、
			// 実際に落ちるのは 92.8km/h まで減速してから。その間に目標ギアで
			// 計算すると滑り比が 0.647 (実際は 0.970) という異常値になる。
			engagedGear := can.ActualGear(gearRatio)
			mech := can.MechGearRatio(engagedGear)
			if tcLocked && !shifting && currentSpeed > 30 && rpm > 300 && mech > 0 {
				slipCal.Observe(rpm, currentSpeed, mech)
			}

			// 車速の下限を 20km/h とする。それ以下ではトルコンが大きく滑り、
			// ロック率に意味が無い。
			//
			// 変速中は回転が過渡状態にあり計算値が暴れるので、直前の値を保持
			// する。0 に落とすと変速のたびに指針が振り切れて読めなくなる。
			if currentSpeed > 20 && rpm > 300 && mech > 0 {
				if !shifting {
					lastLockPct = slipCal.LockPct(rpm, currentSpeed, mech)
					if s, ok := slipCal.Slip(rpm, currentSpeed, mech); ok {
						lastSlip = s
					}
				}
			} else {
				lastLockPct = 0
				lastSlip = 0
			}
			tccLockPct := lastLockPct
			slipRatio := lastSlip

			// CAN直結では全データが常時取得可能なため常にIsFull
			isFull := true
			data := &obd.OBDData{
				RPM:             rpm,
				SpeedKmh:        currentSpeed,
				EngineLoad:      engineLoad,
				ThrottlePos:     engineLoad, // LOADをスロットル表示に使用（CAN 0x201 B6）
				CoolantTemp:     coolantTemp,
				IntakeMAP:       intakeMAP,
				MAFAirFlow:      mafAirFlow,
				EngagedGear:     engagedGear,
				ATFTempC:        atfTempC,
				HasATF:          hasATF,
				PulseDistanceKm: pulseCounter.DistanceKm(),
				PulseValid:      pulseCounter.Valid(),
				Voltage:         voltage,
				FuelLevel:       fuelLevel,
				AmbientTemp:     ambientTemp,
				ShortFuelTrim:   shortFuelTrim,
				LongFuelTrim:    longFuelTrim,
				TimingAdvance:   timingAdvance,
				IntakeAirTemp:   intakeAirTemp,
				O2Voltage:       o2Voltage,
				RuntimeSec:      runtimeSec,
				Gear:            gear,
				GearRatio:       gearRatio,
				ATRange:         int(atRange),
				Hold:            hold,
				TCLocked:        tcLocked,
				Shifting:        shifting,
				HasMAF:          hasMAF,
				TCCLockPct:      tccLockPct,
				SlipRatio:       slipRatio,
				OdometerCANKm:   odometerCANKm,
				ElecB0Pct:       elecB0Pct,
				ElecB1Raw:       elecB1Raw,
			}
			currentHasMAP := hasMAP
			mu.Unlock()

			select {
			case ch <- OBDEvent{
				Data:      data,
				IsFull:    isFull,
				Connected: true,
				HasMAF:    hasMAF,
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
