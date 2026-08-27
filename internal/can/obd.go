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
// 値は 1バイトで、℃ = raw - 40。分解能 1℃。
//
// 同定の根拠:
//
//  1. 同じ水温90℃で、停車中 82℃ / 走行中 90℃ と 8℃ 違った。
//     水温から導かれた値なら一致するはずで、実際 0x114D は一致した。
//  2. 走行中は上昇11回・下降0回で単調に増え、短い停車では下がらない。
//     熱容量がある。
//  3. 停車アイドルでの暖機中は、水温が 65→88℃ と 23℃ 上がる間に
//     66→68℃ と 2℃ しか上がらなかった。トルコンが仕事をしない停車中は
//     ラジエータの熱交換器だけで温まるため、大きく遅れる。
//  4. 別の日には 66℃ を指しており、単調増加のカウンタではない。
const PID22ATFTemp uint16 = 0x17B3

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
	return float64(data[0]) - 40.0, true
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
