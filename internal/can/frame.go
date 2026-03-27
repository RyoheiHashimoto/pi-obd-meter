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
	IDATCtrl   uint32 = 0x230 // AT制御: ギア + TCロックアップ + ギア比
	IDATStatus uint32 = 0x231 // ATステータス: ギア + HOLD + シフトフラグ
	IDCoolant  uint32 = 0x420 // 水温 + 距離パルス
	IDElectric uint32 = 0x430 // オルタ負荷 + 電圧 + 大気圧
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
//	B0: オルタネーター負荷 (raw / 2.55 = %)
//	B1: バッテリー電圧 (raw * 0.08 V)
//	B4-5: 大気圧 (raw / 100 kPa)
func DecodeElectric(data [8]byte) (altLoadPct, voltageV, baroKPa float64) {
	altLoadPct = float64(data[0]) / 2.55
	voltageV = float64(data[1]) * 0.08
	rawBaro := uint16(data[4])<<8 | uint16(data[5])
	baroKPa = float64(rawBaro) / 100.0
	return
}

// DecodeATCtrl は 0x230 フレームをデコードする
//
//	B0: ギア (0x01-0x04=1-4速, 0x10=R, 0xF0=N/P)
//	B1: ギア係合状態 (0x00/0x01)
//	B2: ギア比 (×0.01、1バイトオーバーフロー: 1速/Rはギア比>2.55のため+256して解釈)
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
	b2 := int(data[2])
	// 1速(gear=1)またはR(B0=0x10): ギア比>2.55で1バイトオーバーフロー
	if gear == 1 || raw == 0x10 {
		b2 += 256
	}
	gearRatio = float64(b2) / 100.0
	return
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
		return "?"
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

// DecodeWheelSpeed は 0x4B0 フレームから前輪左の車速をデコードする
//
//	B0-1: FL速度 ((raw - 10000) / 100 km/h)
func DecodeWheelSpeed(data [8]byte) float64 {
	raw := int(uint16(data[0])<<8 | uint16(data[1]))
	speed := float64(raw-10000) / 100.0
	if speed < 0 {
		return 0
	}
	return speed
}
