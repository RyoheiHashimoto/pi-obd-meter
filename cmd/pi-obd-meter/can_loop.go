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

// OBD 要求の送信スロット。1 tick に1つだけ出す。
const (
	slotMAF   = iota // 0x10 MAF
	slotMAP          // 0x0B MAP
	slotBrake        // Mode22 0x1101 ブレーキ・ファン
	slotGrade        // Mode22 0x3201 勾配
	slotAC           // Mode22 0x1103 エアコン
	slotAux          // 機関系の診断値 (auxPIDs を巡回)
	slotProbe        // 未同定 Mode22 PID (PID22Probe を巡回)
	slotSlow         // 電圧・ATF・故障コード
)

// sendSchedule は要求を出す順番。送信できたときだけ次へ進む (slotIdx)。
//
// tick の番号で引いてはいけない。応答が返らない要求があると、その直後の
// スロットが毎回飛ばされる。1周に1回しかない勾配やエアコンがそこに来ると、
// 走行中まったく取れなくなる。
//
// 【1 tick に 2 つ投げてはいけない】
// この ECU は 7DF (ブロードキャスト) の要求を処理している間に届いた要求を、
// 宛先を問わず捨てる。2026-09-16 の走行ログ (9万フレーム) で確かめた。
//
//	0x42 電圧の要求 4,655 件
//	  前の要求が応答済みのとき  735 件中  735 件 応答 (100.0%)
//	  7DF の応答待ちのとき    3,920 件中    0 件 応答 (  0.0%)
//
//	Mode22 の要求
//	  前の要求が応答済み     15,733 件中 15,733 件 応答 (100.0%)
//	  7E0 の応答待ち          1,786 件中  1,701 件 応答 ( 95.2%)
//	  7DF の応答待ち         54,627 件中  7,225 件 応答 ( 13.2%)
//
// 以前は MAF/MAP を毎 tick 出したうえに電圧と Mode22 を重ねていた。
// このため電圧は 15.8%、ブレーキ状態 (0x1101) は 22% しか返っておらず、
// 「踏んだ瞬間を捉える」つもりの 200ms 周期が実際には 900ms 周期だった。
// MAF/MAP が無事だったのは、常にその tick の1つ目だったからにすぎない。
//
// intervalMs=50 のとき 20 スロットで 1 秒。1周あたりの回数は
// MAF 5 / MAP 5 / ブレーキ 4 / 勾配 1 / エアコン 1 / aux 2 / probe 1 / slow 1。
// MAF と MAP は 100ms から 200ms 周期に落ちるが、燃料の積算は毎 tick
// 直近値を使うので定常走行では差が出ない。取りこぼしが無くなる分、
// 実効では以前より多く取れる。
var sendSchedule = [...]int{
	slotMAF, slotBrake, slotMAP, slotAux,
	slotMAF, slotBrake, slotMAP, slotGrade,
	slotMAF, slotBrake, slotMAP, slotAux,
	slotMAF, slotBrake, slotMAP, slotAC,
	slotMAF, slotSlow, slotMAP, slotProbe,
}

// obdReplyTimeout は応答を待つ上限。これを過ぎたら諦めて次の要求へ進む。
//
// 実測の応答は 0.6〜8ms なので、正常なら 1 tick 待たずに空く。
// 待つのは応答の無い PID を投げたときだけ。
const obdReplyTimeout = 100 * time.Millisecond

// 応答と要求を対応づける鍵。
//
// 【鍵を見ずに門を開けてはいけない】
// タイムアウトで次の要求へ進んだあとに、前の要求の遅い応答が届くことがある。
// 応答というだけで門を開けると、今出したばかりの要求の答えを待たずに次を
// 送ってしまい、「1 tick 1 要求」が崩れて ECU に捨てられる。
// Mode 01 は 0x41xx、Mode 22 は 0x62xxxx、Mode 03/07 は 0x43/0x47 の
// 上位バイトを使うので、3 つの空間は重ならない。
func replyKey01(pid byte) uint32    { return 0x410000 | uint32(pid) }
func replyKey22(pid uint16) uint32  { return 0x620000 | uint32(pid) }
func replyKeyMode(mode byte) uint32 { return uint32(mode) << 16 }

// replyKey は応答ペイロードから鍵を作る。
// 対応づけられない応答は ok=false を返す。
func replyKey(payload []byte) (uint32, bool) {
	if len(payload) < 1 {
		return 0, false
	}
	switch payload[0] {
	case 0x41: // Mode 01 の応答
		if len(payload) < 2 {
			return 0, false
		}
		return replyKey01(payload[1]), true
	case 0x62: // Mode 22 の応答
		if len(payload) < 3 {
			return 0, false
		}
		return replyKey22(uint16(payload[1])<<8 | uint16(payload[2])), true
	case 0x43, 0x47: // 故障コード
		return replyKeyMode(payload[0]), true
	}
	return 0, false
}

// canReaderLoop はCAN-BUSパッシブモニタリング + OBD-2クエリによるデータ取得ループ。
//
// パッシブ受信（毎フレーム ~20ms）:
//   - 0x201: RPM, 車速, エンジン負荷
//   - 0x430: 大気圧, 電圧
//   - 0x4B0: 4輪速度
//
// OBD-2クエリ: sendSchedule の順に 1 tick 1 要求。前の応答を待ってから次を出す。
func canReaderLoop(ctx context.Context, ifname string, intervalMs int, ch chan<- OBDEvent) {
	defer close(ch)

	if intervalMs <= 0 {
		intervalMs = 200
	}

	const (
		reconnectInterval = 10 * time.Second
		staleTimeout      = 1 * time.Second // エンジン ECU (IDEngine 100Hz) 無通信で OFF 判定
	)

	// CAN直結モードでは速度・RPM・負荷・水温はCAN受信で足りる。
	// OBD で取るのは sendSchedule に並べたものだけ。
	// 0x2F (燃料残量)、0x46 (外気温) は DY ZJ-VE 非対応確認済のため投げない。

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
	// sendSchedule に 20 スロット中 2 つ割り当ててあるので、10 個を
	// 5 秒で1周する。表示には使わず記録して後から傾向を見るものなので、
	// この速さで足りる。速くしたいなら MAF/MAP の枠を削ることになる。
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
		slotIdx    int
		auxIdx     int
		probe22Idx int
		slowIdx    int
		aux22      map[uint16]uint32

		// 要求を出して応答を待っている間は true。次の要求を止める門。
		// これが無いと ECU が要求を捨てる (sendSchedule のコメント参照)。
		// inflightKey は待っている応答の鍵。遅れて届いた古い応答で
		// 門が開かないようにする。
		inflight    bool
		inflightAt  time.Time
		inflightKey uint32
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
					// Mode 01/22 の応答も単一フレームとしてここを通る。
					// 待っていた応答かどうかは鍵で見分ける (replyKey)。
					if payload, needFC := isotp.Push(frame); needFC {
						// 続きを待つ。組み立てが終わるまで次の要求は出さない。
						// まだ応答の途中なので、待ち時間を数え直す。数え直さ
						// ないと、故障コードが多いときに組み立ての最中で
						// タイムアウトして次の要求を重ねてしまう。
						_ = s.WriteFrame(can.FlowControlFrame())
						inflightAt = time.Now()
					} else if len(payload) > 0 {
						// 応答が1つ揃った。待っていたものなら門を開ける。
						// Mode 01 (0x41)・Mode 22 (0x62)・故障コード (0x43/0x47)
						// はどれも単一フレームとしてここを通るので、門の解除は
						// この1箇所で足りる。
						//
						// 否定応答 (0x7F) は PID を含まないため鍵を作れない。
						// 直前の要求に対するものなので、そのまま開ける。
						if payload[0] == 0x7F {
							inflight = false
						} else if k, ok := replyKey(payload); ok && k == inflightKey {
							inflight = false
						}
						if payload[0] == 0x43 || payload[0] == 0x47 {
							if codes, ok := obd.ParseDTCPayload(payload); ok {
								if payload[0] == 0x43 {
									dtcCodes = codes
								} else {
									pendingDTCs = codes
								}
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

			// --- OBD 要求の送信 ---
			//
			// 1 tick に 1 要求だけ出し、前の応答が返るまで次を出さない。
			// 重ねて投げると ECU が捨てる (sendSchedule のコメント参照)。
			mu.Lock()
			busy := inflight && time.Since(inflightAt) < obdReplyTimeout
			mu.Unlock()

			if !busy {
				// 【スケジュールは送ったときだけ進める】
				// tickCount で引くと、応答の無い要求の直後のスロットが
				// 毎回スキップされる。1周に1回しかない勾配やエアコンが
				// その位置に来ると、走行中まったく取れなくなる。
				var req can.Frame
				var key uint32

				switch sendSchedule[slotIdx%len(sendSchedule)] {
				case slotMAF:
					req, key = can.OBDRequestFrame(obd.PIDMAFAirFlow), replyKey01(obd.PIDMAFAirFlow)
				case slotMAP:
					req, key = can.OBDRequestFrame(obd.PIDIntakeMAP), replyKey01(obd.PIDIntakeMAP)
				case slotBrake:
					req, key = can.OBDRequestFrame22(can.PID22Status), replyKey22(can.PID22Status)
				case slotGrade:
					req, key = can.OBDRequestFrame22(can.PID22Grade), replyKey22(can.PID22Grade)
				case slotAC:
					req, key = can.OBDRequestFrame22(can.PID22ACCompressor), replyKey22(can.PID22ACCompressor)
				case slotAux:
					pid := auxPIDs[auxIdx%len(auxPIDs)]
					auxIdx++
					req, key = can.OBDRequestFrame(pid), replyKey01(pid)
				case slotProbe:
					// 未同定 PID の生値集め。表示に使わないので最後で良い。
					pid := can.PID22Probe[probe22Idx%len(can.PID22Probe)]
					probe22Idx++
					req, key = can.OBDRequestFrame22(pid), replyKey22(pid)
				case slotSlow:
					// 電圧・ATF油温・故障コード。どれも高頻度は要らない。
					// ATF は熱容量が大きく分解能も1℃で、停車4分間まったく
					// 動かなかった実測がある。
					mu.Lock()
					// 走行中に記録数が変わったら故障コードを読み直す。
					if hasMonitor && milDTCCount != lastDTCCount {
						lastDTCCount = milDTCCount
						dtcStage = 0
					}
					stage := -1
					if hasData && dtcStage < 2 && slowIdx%3 == 2 {
						stage = dtcStage
						dtcStage++
					}
					mu.Unlock()
					switch {
					case stage == 0:
						req, key = can.OBDRequestFrameMode(can.ModeStoredDTC), replyKeyMode(0x43)
					case stage == 1:
						req, key = can.OBDRequestFrameMode(can.ModePendingDTC), replyKeyMode(0x47)
					case slowIdx%3 == 0:
						req, key = can.OBDRequestFrame(obd.PIDControlModuleV), replyKey01(obd.PIDControlModuleV)
					default:
						req, key = can.OBDRequestFrame22(can.PID22ATFTemp), replyKey22(can.PID22ATFTemp)
					}
					slowIdx++
				}
				slotIdx++

				// 鍵が付かないスロットは送らない。表に知らない値が紛れ込んだ
				// ときに、ID 0 のフレームをバスへ流さないための歯止め。
				if key != 0 {
					// 【門は送る前に閉じる】
					// 送ってから閉じると、応答が速いときに競り負ける。実測の
					// 応答は最短 0.6ms で、WriteFrame から次にロックを取るまでの
					// 間にリーダーが応答を受けて門を開けてしまうことがある。
					// その後でこちらが閉じ直すと、応答は済んでいるのに
					// タイムアウトまで次が出せず、周期が倍に落ちる。
					mu.Lock()
					inflight = true
					inflightAt = time.Now()
					inflightKey = key
					mu.Unlock()

					_ = sock.WriteFrame(req)
				}
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
			// 待っていた応答はもう来ない。門を開けておく。
			inflight = false
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
