// Package can はSocketCANを介したCAN-BUS通信を提供する。
// パッシブモニタリングでCAN フレームを受信し、車両データをデコードする。
package can

// Frame はCANフレームを表す
type Frame struct {
	ID   uint32  // CAN ID (11bit standard)
	DLC  uint8   // データ長 (0-8)
	Data [8]byte // ペイロード
}

// DY デミオ CAN ID 定義
const (
	IDEngine   uint32 = 0x201 // RPM + 車速 + 負荷
	IDATCtrl   uint32 = 0x230 // AT制御: ギア + ギア係合状態 + ギア比
	IDATStatus uint32 = 0x231 // ATステータス: ギア + HOLD + シフトフラグ
	IDCoolant  uint32 = 0x420 // 水温 + 距離パルス
	IDElectric uint32 = 0x430 // 燃料残量 + オドメーター (大気圧・電圧は含まない。DecodeElectric 参照)
	IDWheels   uint32 = 0x4B0 // 4輪速度
)

// DecodeEngine は 0x201 フレームをデコードする
//
//	B0-1: RPM (raw / 4)
//	B4-5: 車速 ((raw - 10000) / 100 km/h)
//	B6:   エンジン負荷 (%)
func DecodeEngine(data [8]byte) (rpm, speedKmh, load float64) {
	rpm = float64(uint16(data[0])<<8|uint16(data[1])) / 4.0
	rawSpeed := int(uint16(data[4])<<8 | uint16(data[5]))
	speedKmh = float64(rawSpeed-10000) / 100.0
	if speedKmh < 0 {
		speedKmh = 0
	}
	load = float64(data[6])
	return
}

// DecodeElectric は 0x430 フレームをデコードする
//
//	B0: 未確定。燃料残量の可能性が高い (raw / 2.55 = %)。issue #119 で検証中
//	B1: 未確定。電圧に連動するが電圧そのものではない。issue #119 で検証中
//	B4-5: オドメーター (raw * 10 km)。実機検証済み
//
// B0 と B1 は独立した2つの量ではなく B0 + 2*B1 ≒ 418 の拘束を受ける。
// 電圧は OBD-2 標準 PID 0x42 (Control module voltage) を使用すること。
func DecodeElectric(data [8]byte) (rawB0Pct, rawB1 float64, odometerKm float64) {
	rawB0Pct = float64(data[0]) / 2.55
	rawB1 = float64(data[1])
	rawOdo := uint16(data[4])<<8 | uint16(data[5])
	odometerKm = float64(rawOdo) * 10.0
	return
}

// MechGearRatio は FN4A-EL の機械ギア比を返す (1-4速、範囲外は0)。
func MechGearRatio(gear int) float64 {
	switch gear {
	case 1:
		return 2.816
	case 2:
		return 1.498
	case 3:
		return 1.000
	case 4:
		return 0.726
	}
	return 0
}

// DecodeATCtrl は 0x230 フレームをデコードする
//
//	B0: ギア (0x01-0x04=1-4速, 0x10=R, 0xF0=N/P)
//	B1: 1速 または N/P のフラグ (ギア段から導けるため独立した情報は無い)
//	B2: 現在のギア比 ×100 (8bit のため 2.55 を超えるとラップする)
//
// B2 はトルコンの滑りを含まない「機械ギア比」である。当初は滑りを含む実効
// ギア比だと解釈したが、実測 (2026-08-27, 310サンプル) で否定された:
//
//	1速 raw=26 (85%)   2速 150 (65%)   3速 100 (89%)   4速 73 (75%)
//
// いずれも機械ギア比と一致し、外れ値は変速の過渡で隣のギアの値を取ったもの
// だけだった。3速で加速中も B2 は 1.00 に固定される一方、rpm と車速から
// 求めた実際の実効比は 1.12〜1.42 まで開いていた。つまり B2 に滑りの情報は
// 含まれない。トルコンの滑りは SlipCalibrator で rpm と車速から求めること。
//
// ラップするのは 2.55 を超える1速 (2.816) と R (約2.7) だけなので、この2つ
// でのみ補正する。2〜4速は機械ギア比が 2.55 未満でラップし得ないため、
// 生値をそのまま使う。以前は「機械ギア比を下回ったらラップ」と判定して
// いたが、変速過渡の値 (3速で73, 2速で100 等) を誤って +2.56 し、3.29 や
// 3.56 といった存在しないギア比を出力していた。
func DecodeATCtrl(data [8]byte) (gear int, gearRatio float64) {
	raw := data[0]
	switch raw {
	case 0x01:
		gear = 1
	case 0x02:
		gear = 2
	case 0x03:
		gear = 3
	case 0x04:
		gear = 4
	default:
		gear = 0 // N/P or transition
	}

	gearRatio = float64(data[2]) / 100.0

	// 1速と R だけ 8bit を超えるのでラップを戻す。
	//
	// 閾値は 0.5 とする。ラップ後の生値は1速で 0.26、R で 0.14 と十分小さい
	// 一方、変速の過渡では 0.95 のような中途半端な値が現れる。閾値を 1.0 に
	// すると後者まで補正して 3.51 という存在しないギア比を出してしまう。
	if gear == 1 || raw == 0x10 {
		if gearRatio < 0.5 {
			gearRatio += 2.56
		}
	}
	return
}

// ActualGear は 0x230 のギア比から、実際に噛んでいるギアを判定する。
//
// 0x231 のギア番号は「これから入れる目標ギア」であって、実際に噛んでいる
// ギアとは限らない。実測 (2026-08-29) では、95km/h で S レンジに入れた瞬間に
// ギア番号が 3速→2速 に変わったが、ギア比は 1.000 (3速) のままで、回転数も
// 3580→3554rpm とほとんど動かなかった。実際に 2速へ落ちたのは 92.8km/h まで
// 減速してからである。2速で 95km/h は 5,500rpm になるため、ECU が待っていた。
//
//	表示  車速   rpm   0x230比  実効比
//	3速  95.0  3580   1.000   0.971  ← 3速
//	2速  94.5  3554   1.000   0.970  ← まだ3速
//	2速  92.8  3807   1.090   1.058  ← ここで動き始めた
//
// ギア番号で滑り比を計算すると、この区間で 0.647 という異常値になる
// (実際は 0.970)。実際のギアはギア比から判定すること。
//
// どの機械ギア比とも一致しない場合 (変速の途中) は 0 を返す。
func ActualGear(gearRatio float64) int {
	const tol = 0.04 // 実測のばらつき (分解能 0.01、ロックアップ時で最大 0.02) を許容する
	best, bestDiff := 0, tol
	for g := 1; g <= 4; g++ {
		m := MechGearRatio(g)
		if m <= 0 {
			continue
		}
		d := gearRatio - m
		if d < 0 {
			d = -d
		}
		if d < bestDiff {
			best, bestDiff = g, d
		}
	}
	return best
}

// ATRange はシフトレバー位置を表す
type ATRange int

const (
	ATRangeUnknown ATRange = 0
	ATRangeP       ATRange = 1
	ATRangeR       ATRange = 2
	ATRangeN       ATRange = 3
	ATRangeD       ATRange = 4
	ATRangeS       ATRange = 5
	ATRangeL       ATRange = 6
)

// String はレンジの文字列表現を返す
func (r ATRange) String() string {
	switch r {
	case ATRangeP:
		return "P"
	case ATRangeR:
		return "R"
	case ATRangeN:
		return "N"
	case ATRangeD:
		return "D"
	case ATRangeS:
		return "S"
	case ATRangeL:
		return "L"
	default:
		return "--"
	}
}

// DecodeATStatus は 0x231 フレームをデコードする
//
//	B0 上位ニブル: ギア (0=N/P, 1-4)
//	B0 下位ニブル: レンジ (1=P, 2=R, 3=N, 4=D, 5=S, 6=L)
//	B1: bit7=HOLD, bit4=TCロックアップ, bit3=ギアチェンジ中
func DecodeATStatus(data [8]byte) (gear int, atRange ATRange, hold bool, tcLocked bool, shifting bool) {
	gear = int(data[0] >> 4)
	sub := data[0] & 0x0F
	atRange = ATRange(sub)
	hold = data[1]&0x80 != 0
	tcLocked = data[1]&0x10 != 0
	shifting = data[1]&0x08 != 0
	return
}

// DecodeCoolant は 0x420 フレームをデコードする
//
//	B0: 水温 = raw - 40 (°C)
//	B1: 距離パルス (8bit rolling counter)
func DecodeCoolant(data [8]byte) (tempC float64, distPulse uint8) {
	tempC = float64(data[0]) - 40.0
	distPulse = data[1]
	return
}

// DecodeWheelSpeed は 0x4B0 フレームから4輪平均車速をデコードする
//
//	B0-1: FL, B2-3: FR, B4-5: RL, B6-7: RR ((raw - 10000) / 100 km/h)
func DecodeWheelSpeed(data [8]byte) float64 {
	var sum float64
	for i := 0; i < 4; i++ {
		raw := int(uint16(data[i*2])<<8 | uint16(data[i*2+1]))
		spd := float64(raw-10000) / 100.0
		if spd < 0 {
			spd = 0
		}
		sum += spd
	}
	return sum / 4.0
}
