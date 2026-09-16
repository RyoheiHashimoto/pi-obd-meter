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

	// 機関系の診断値。表示には使わず、記録して不調の兆候を見るためのもの。
	//
	// 【要求しないと取れない】
	// 受信側のデコードは以前から 0x06/0x07/0x0E/0x0F/0x14 に対応していたが、
	// メーターはこれらを要求していなかった。2026-09-02 まで値が入っていたのは、
	// 同定用の巡回ポーリング (poll22) が同じバスに投げていた応答を拾っていた
	// だけで、それを止めた 9/7 以降は全点 0 になった。2026-09-16 の走行で
	// 「トリムも点火時期も 0、つまり狂いがない」と読みかけたが、実際は
	// 取れていなかった。0 は健全性の根拠にならない。
	//
	// 100ms に1つずつ巡回するので 1 周 1 秒。MAF/MAP の 100ms 周期には
	// 触らない。追加の送信は 10Hz で、0x201 が 100Hz で流れるバスに対して
	// 十分小さい。
	auxPIDs := []byte{
		obd.PIDMonitorStatus,    // 0x01 — MIL と記録されている DTC の数
		obd.PIDShortFuelTrim,    // 0x06 — 短期燃料トリム
		obd.PIDLongFuelTrim,     // 0x07 — 長期燃料トリム
		obd.PIDTimingAdvance,    // 0x0E — 点火時期。ノッキングで遅角する
		obd.PIDIntakeAirTemp,    // 0x0F — 吸気温
		obd.PIDFuelSystemStatus, // 0x03 — トリムが効いている状態かの判定に要る
		obd.PIDO2SensorB1S1,     // 0x14 — O2 センサー電圧
		obd.PIDAbsoluteLoad,     // 0x43 — 絶対負荷
		obd.PIDCatalystTempB1S1, // 0x3C — 触媒温度
		obd.PIDRuntime,          // 0x1F — エンジン稼働時間
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
		brakePedal    bool
		radiatorFan   bool
		acCompressor  bool
		gradeRaw      int
		hasGrade      bool
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

		// 機関系の診断値
		fuelSysStatus int
		catalystTempC float64
		hasCatalyst   bool
		absoluteLoad  float64
		mil           bool
		milDTCCount   int
		hasMonitor    bool

		// 故障コード。dtcStage は 0=Mode 03 未送信, 1=Mode 07 未送信, 2=読了。
		// lastDTCCount と記録数が食い違ったら 0 に戻して読み直す。
		lastDTCCount = -1
		dtcStage     int
		dtcCodes     []obd.DTC
		pendingDTCs  []obd.DTC

		// ISO-TP の組み立てと、未同定 Mode 22 PID の生値
		isotp      can.Reassembler
		probe22Idx int
		aux22      map[uint16]uint32
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
					// ISO-TP の組み立て。故障コード (Mode 03/07) は 3 件以上で
					// 複数フレームに分かれ、Flow Control を返さないと続きが来ない。
					// Mode 01/22 の応答も単一フレームとしてここを通るが、
					// 先頭バイトで弾く。
					if payload, needFC := isotp.Push(frame); needFC {
						_ = s.WriteFrame(can.FlowControlFrame())
					} else if len(payload) > 0 && (payload[0] == 0x43 || payload[0] == 0x47) {
						if codes, ok := obd.ParseDTCPayload(payload); ok {
							if payload[0] == 0x43 {
								dtcCodes = codes
							} else {
								pendingDTCs = codes
							}
						}
					}

					// Mode 22 (拡張診断データ) の応答。ATF油温はここから来る。
					if pid22, data, ok := can.ParseOBDResponse22(frame); ok {
						switch pid22 {
						case can.PID22ATFTemp:
							if t, ok := can.DecodeATFTemp(data); ok {
								atfTempC = t
								hasATF = true
							}
						case can.PID22Status:
							if b, f, ok := can.DecodeStatus1101(data); ok {
								brakePedal, radiatorFan = b, f
							}
						case can.PID22ACCompressor:
							if on, ok := can.DecodeACCompressor(data); ok {
								acCompressor = on
							}
						case can.PID22Grade:
							if g, ok := can.DecodeGrade(data); ok {
								gradeRaw = g
								hasGrade = true
							}
						default:
							// 未同定の PID は生値のまま残す。意味が決まって
							// いないうちに単位を付けると、後から見た人が
							// 確定値だと思い込む。
							if can.IsProbe22(pid22) {
								if v, ok := can.DecodeRaw22(data); ok {
									if aux22 == nil {
										aux22 = make(map[uint16]uint32, len(can.PID22Probe))
									}
									aux22[pid22] = v
								}
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
						case obd.PIDMonitorStatus:
							// A: bit7 = MIL 点灯, bit0-6 = 記録されている DTC 数
							if len(data) >= 1 {
								mil = data[0]&0x80 != 0
								milDTCCount = int(data[0] & 0x7F)
								hasMonitor = true
							}
						case obd.PIDFuelSystemStatus:
							if len(data) >= 1 {
								fuelSysStatus = int(data[0])
							}
						case obd.PIDCatalystTempB1S1:
							// ((A*256)+B)/10 − 40 ℃
							if len(data) >= 2 {
								catalystTempC = float64(uint16(data[0])<<8|uint16(data[1]))/10.0 - 40.0
								hasCatalyst = true
							}
						case obd.PIDAbsoluteLoad:
							// ((A*256)+B)×100/255 %
							if len(data) >= 2 {
								absoluteLoad = float64(uint16(data[0])<<8|uint16(data[1])) * 100.0 / 255.0
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

			// 機関系の診断値。100ms に1つずつ巡回する (1周 1秒)。
			if n := max(1, 100/intervalMs); tickCount%n == 0 {
				_ = sock.WriteFrame(can.OBDRequestFrame(auxPIDs[(tickCount/n)%len(auxPIDs)]))
			}

			// 故障コードは始動時に1回だけ読む。Mode 03 と Mode 07 は 1秒
			// あけて送る。複数フレームの応答が途中のうちに次を投げると、
			// 組み立てが混ざって存在しないコードを作りかねない。
			if tickCount%max(1, 1000/intervalMs) == 0 {
				mu.Lock()
				// 走行中に記録数が変わったら読み直す。
				if hasMonitor && milDTCCount != lastDTCCount {
					lastDTCCount = milDTCCount
					dtcStage = 0
				}
				stage := -1
				if hasData && dtcStage < 2 {
					stage = dtcStage
					dtcStage++
				}
				mu.Unlock()
				switch stage {
				case 0:
					_ = sock.WriteFrame(can.OBDRequestFrameMode(can.ModeStoredDTC))
				case 1:
					_ = sock.WriteFrame(can.OBDRequestFrameMode(can.ModePendingDTC))
				}
			}

			// ATF油温 (Mode 22)。油は熱容量が大きく分解能も1℃しかないため、
			// 2秒に1回で十分。実測では停車4分間まったく動かなかった。
			if tickCount%max(1, 2000/intervalMs) == 0 {
				_ = sock.WriteFrame(can.OBDRequestFrame22(can.PID22ATFTemp))
			}

			// ブレーキ・ファン・エアコン・勾配 (Mode 22)。
			//
			// ブレーキは踏んだ瞬間を捉えたいので速く回す。1つの tick に1つずつ
			// 送り、4つを順に巡る。intervalMs=50 なら各 PID は 200ms 周期になる。
			// ATF (2秒周期) と合わせても Mode 22 の送信は 25Hz 未満で、
			// 0x201 が 100Hz で流れているバスに対して十分小さい。
			switch tickCount % 4 {
			case 0:
				_ = sock.WriteFrame(can.OBDRequestFrame22(can.PID22Status))
			case 1:
				_ = sock.WriteFrame(can.OBDRequestFrame22(can.PID22Grade))
			case 2:
				_ = sock.WriteFrame(can.OBDRequestFrame22(can.PID22ACCompressor))
			case 3:
				// 未同定 PID の生値集め。6個を順に巡って 1.2秒周期。
				// 表示には使わないので、これ以上速くする理由がない。
				_ = sock.WriteFrame(can.OBDRequestFrame22(can.PID22Probe[probe22Idx%len(can.PID22Probe)]))
				probe22Idx++
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

			// 未同定 PID の生値は毎回コピーして渡す。map をそのまま
			// 渡すと、受け取った側が読んでいる最中に受信側が書き換える。
			var aux22Copy map[uint16]uint32
			if len(aux22) > 0 {
				aux22Copy = make(map[uint16]uint32, len(aux22))
				for k, v := range aux22 {
					aux22Copy[k] = v
				}
			}

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
				BrakePedal:      brakePedal,
				RadiatorFan:     radiatorFan,
				ACCompressor:    acCompressor,
				GradeRaw:        gradeRaw,
				HasGrade:        hasGrade,
				OdometerCANKm:   odometerCANKm,
				ElecB0Pct:       elecB0Pct,
				ElecB1Raw:       elecB1Raw,

				FuelSystemStatus: fuelSysStatus,
				CatalystTempC:    catalystTempC,
				HasCatalyst:      hasCatalyst,
				AbsoluteLoad:     absoluteLoad,
				MIL:              mil,
				DTCCount:         milDTCCount,
				HasMonitor:       hasMonitor,
				DTCCodes:         dtcCodes,
				PendingDTCs:      pendingDTCs,
				Aux22:            aux22Copy,
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
			// 組み立て途中の応答は捨てる。再接続後の続きと繋がると
			// 前後が混ざったコードになる。故障コードも読み直す。
			isotp.Reset()
			dtcStage = 0
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
