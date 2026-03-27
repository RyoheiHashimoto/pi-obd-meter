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
