package can

// OBD-2 over CAN の定数
const (
	IDOBDRequest  uint32 = 0x7DF // OBD-2 ブロードキャストリクエスト
	IDOBDResponse uint32 = 0x7E8 // ECU レスポンス（プライマリ）
)

// OBDRequestFrame はOBD-2 PIDクエリ用のCANフレームを作成する
func OBDRequestFrame(pid byte) Frame {
	return Frame{
		ID:  IDOBDRequest,
		DLC: 8,
		Data: [8]byte{
			0x02, // データバイト数
			0x01, // Mode 01 (現在データ)
			pid,  // PID
			0x00, 0x00, 0x00, 0x00, 0x00,
		},
	}
}

// ParseOBDResponse はOBD-2レスポンスフレームからPIDとデータを抽出する。
// 有効なOBDレスポンスでない場合は ok=false を返す。
func ParseOBDResponse(f Frame) (pid byte, data []byte, ok bool) {
	if f.ID != IDOBDResponse {
		return 0, nil, false
	}
	if f.DLC < 4 || f.Data[1] != 0x41 {
		return 0, nil, false
	}
	pid = f.Data[2]
	numBytes := int(f.Data[0]) - 2 // データ長 - mode(1) - pid(1)
	if numBytes <= 0 || numBytes > 5 {
		return 0, nil, false
	}
	data = make([]byte, numBytes)
	copy(data, f.Data[3:3+numBytes])
	return pid, data, true
}

// Mode 22 (拡張診断データ) の定数。
//
// Mode 01 の標準PIDには ATF 油温が無いため、メーカー独自の Mode 22 を使う。
// リクエストは ECU 個別アドレス 7E0 に送る。7DF のブロードキャストでは
// 応答しない ECU がある。実測では 7DF に応答したのは PCM (7E8) のみで、
// この車両の変速機は PCM が統合制御している。
const (
	IDOBDRequestECU uint32 = 0x7E0 // ECU 個別アドレス (PCM)
)

// PID22ATFTemp は AT フルード油温 (2026-08-28 に実機で同定)。
//
// ScanGauge の公式 X-Gauge 定義 "Mazda CANSF Transmission Temperature" と
// 一致する。TXD 07E02217B3 (7E0 へ Mode 22 で 0x17B3) で、対象は CAN 方式の
// マツダ車 2003年以降。第三者の実車で使われている定義であり、本車両で
// 同定した結果と一致した。
//
// 同定の根拠:
//
//  1. 同じ水温で、発熱条件を正反対にして比べると差が出る。
//     水温90℃で停車中 64℃ / 走行中 72℃。水温から導かれた値なら一致する
//     はずで、実際 0x114D は一致してしまい派生値と判明した。
//  2. 走行中は単調に増え、1〜2分の停車では下がらない。熱容量がある。
//  3. 停車アイドルでの暖機中は、水温が 65→88℃ と 23℃ 上がる間に
//     49.5→51.4℃ と 2℃ しか上がらない。トルコンが仕事をしない停車中は
//     ラジエータの熱交換器だけで温まるため大きく遅れる。
//  4. 別の日には 49℃ を指しており、単調増加のカウンタではない。
const PID22ATFTemp uint16 = 0x17B3

// ATF 油温の換算係数。ScanGauge の MTH 002A0019FFC7 に由来する。
//
//	°F = raw × 42/25 − 57
//	°C = (°F − 32) / 1.8 = raw × 0.93333 − 49.4444
//
// 当初は他の温度PIDからの類推で °C = raw − 40 としていたが誤りだった。
// その式では首都高走行で 120℃ に達し、水温より 31℃ も高くなってしまう。
// 実車の報告では高速走行中の油温は水温とほぼ同じであり、辻褄が合わなかった。
// 正しい式では +1.3℃ となって実態と一致する。
const (
	atfTempMul    = 42.0 / 25.0
	atfTempOffF   = -57.0
	atfFtoCOffset = 32.0
	atfFtoCScale  = 1.8
)

// OBDRequestFrame22 は Mode 22 の2バイトPIDを問い合わせるフレームを作る。
func OBDRequestFrame22(pid uint16) Frame {
	return Frame{
		ID:  IDOBDRequestECU,
		DLC: 8,
		Data: [8]byte{
			0x03,           // データバイト数 (mode + PID2バイト)
			0x22,           // Mode 22 (拡張データ)
			byte(pid >> 8), // PID 上位
			byte(pid),      // PID 下位
			0x00, 0x00, 0x00, 0x00,
		},
	}
}

// ParseOBDResponse22 は Mode 22 のレスポンスから PID とデータを取り出す。
func ParseOBDResponse22(f Frame) (pid uint16, data []byte, ok bool) {
	if f.ID != IDOBDResponse {
		return 0, nil, false
	}
	if f.DLC < 5 || f.Data[1] != 0x62 {
		return 0, nil, false
	}
	pid = uint16(f.Data[2])<<8 | uint16(f.Data[3])
	numBytes := int(f.Data[0]) - 3 // データ長 - mode(1) - PID(2)
	if numBytes <= 0 || numBytes > 4 {
		return 0, nil, false
	}
	data = make([]byte, numBytes)
	copy(data, f.Data[4:4+numBytes])
	return pid, data, true
}

// DecodeATFTemp は 0x17B3 のレスポンスを℃に直す。
// 応答が短ければ ok=false。
func DecodeATFTemp(data []byte) (tempC float64, ok bool) {
	if len(data) < 1 {
		return 0, false
	}
	f := float64(data[0])*atfTempMul + atfTempOffF
	return (f - atfFtoCOffset) / atfFtoCScale, true
}

// ATFAlert は油温に対する注意喚起を返す。何も無ければ空文字列。
//
// 目安は業界の経験則による。約95℃を超えると10℃ごとに油の寿命が半減する。
//
//	100℃ ワニス (酸化生成物) が出始める
//	120℃ シールが硬化する
//	130℃ 酸化が急加速する
//	150℃ クラッチが焼ける
func ATFAlert(tempC float64) string {
	switch {
	case tempC >= 130:
		return "ATF危険"
	case tempC >= 120:
		return "ATF高温"
	case tempC >= 100:
		return "ATF注意"
	}
	return ""
}
