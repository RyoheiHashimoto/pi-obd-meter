// Package can はSocketCANを介したCAN-BUS通信を提供する。
// パッシブモニタリングでCAN フレームを受信し、車両データをデコードする。
package can

// Frame はCANフレームを表す
type Frame struct {
	ID   uint32   // CAN ID (11bit standard)
	DLC  uint8    // データ長 (0-8)
	Data [8]byte  // ペイロード
}

// DY デミオ CAN ID 定義
const (
	IDEngine   uint32 = 0x201 // RPM + 車速 + 負荷
	IDWheels   uint32 = 0x4B0 // 4輪速度
	IDElectric uint32 = 0x430 // オルタ負荷 + 電圧 + 大気圧
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
