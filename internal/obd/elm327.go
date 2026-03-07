package obd

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

// ELM327 はELM327アダプタとのシリアル通信を管理する
type ELM327 struct {
	port        serial.Port
	portName    string
	obdProtocol string
	mu          sync.Mutex
	reader      *bufio.Reader
}

// NewELM327 は新しいELM327接続を作成する
// obdProtocol: ATSPコマンドのプロトコル番号 ("0"=自動検出, "6"=CAN 11bit 500kbaud 等)
func NewELM327(portName string, obdProtocol string) *ELM327 {
	if obdProtocol == "" {
		obdProtocol = "6" // デフォルト: CAN 11bit 500kbaud
	}
	return &ELM327{
		portName:    portName,
		obdProtocol: obdProtocol,
	}
}

// Connect はELM327アダプタに接続し初期化する
func (e *ELM327) Connect() error {
	mode := &serial.Mode{
		BaudRate: 38400,
		DataBits: 8,
		StopBits: serial.OneStopBit,
		Parity:   serial.NoParity,
	}

	port, err := serial.Open(e.portName, mode)
	if err != nil {
		return fmt.Errorf("シリアルポートを開けません %s: %w", e.portName, err)
	}

	if err := port.SetReadTimeout(2 * time.Second); err != nil {
		return fmt.Errorf("read timeout設定失敗: %w", err)
	}
	e.port = port
	e.reader = bufio.NewReader(port)

	// ELM327初期化シーケンス
	initCmds := []string{
		"ATZ",                  // リセット
		"ATE0",                 // エコーOFF
		"ATL0",                 // 改行OFF
		"ATS0",                 // スペースOFF
		"ATH0",                 // ヘッダOFF
		"ATSP" + e.obdProtocol, // OBDプロトコル設定
	}

	for _, cmd := range initCmds {
		resp, err := e.sendCommand(cmd)
		if err != nil {
			e.port.Close()
			return fmt.Errorf("初期化コマンド %s 失敗: %w", cmd, err)
		}
		_ = resp
		time.Sleep(200 * time.Millisecond)
	}

	return nil
}

// Close は接続を閉じ、ELM327をスリープさせる
func (e *ELM327) Close() error {
	if e.port != nil {
		// ELM327チップをLow Powerモードに移行（BT基板は起きたまま）
		_, _ = e.sendCommand("AT LP")
		return e.port.Close()
	}
	return nil
}

// sendCommand はELM327にコマンドを送信しレスポンスを取得する
func (e *ELM327) sendCommand(cmd string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	_, err := e.port.Write([]byte(cmd + "\r"))
	if err != nil {
		return "", fmt.Errorf("送信エラー: %w", err)
	}

	var response strings.Builder
	line, err := e.reader.ReadString('>')
	if err != nil {
		// タイムアウトの場合、それまでに読めた分を返す
		if response.Len() > 0 {
			response.WriteString(line)
		} else {
			return "", fmt.Errorf("受信エラー: %w", err)
		}
	} else {
		response.WriteString(line)
	}

	result := strings.TrimSpace(response.String())
	result = strings.TrimSuffix(result, ">")
	result = strings.TrimSpace(result)

	if strings.Contains(result, "NO DATA") || strings.Contains(result, "ERROR") {
		return "", fmt.Errorf("OBDエラー: %s", result)
	}

	return result, nil
}

// QueryPID は指定PIDのデータを取得する（Mode 01）
func (e *ELM327) QueryPID(pid byte) ([]byte, error) {
	cmd := fmt.Sprintf("01%02X", pid)
	resp, err := e.sendCommand(cmd)
	if err != nil {
		return nil, err
	}

	return parseHexResponse(resp)
}

// QueryMultiPID は複数PIDを1コマンドで取得する（Mode 01）
// ELM327は最大6PIDまで対応。未対応の場合は空mapとerrorを返す
func (e *ELM327) QueryMultiPID(pids []byte) (map[byte][]byte, error) {
	if len(pids) == 0 {
		return nil, fmt.Errorf("PIDが指定されていません")
	}
	if len(pids) > 6 {
		pids = pids[:6] // ELM327の制限
	}

	// コマンド構築: "010C0D0405" のように連結
	cmd := "01"
	for _, pid := range pids {
		cmd += fmt.Sprintf("%02X", pid)
	}

	resp, err := e.sendCommand(cmd)
	if err != nil {
		return nil, err
	}

	return parseMultiPIDResponse(resp, pids)
}

// parseMultiPIDResponse はマルチPIDレスポンスをパースする
// レスポンス例: "410C1A2041 0D00410400" → {0x0C: [1A,20], 0x0D: [00], 0x04: [00]}
// ECUによっては改行区切りで返すパターンもある
func parseMultiPIDResponse(resp string, requestedPIDs []byte) (map[byte][]byte, error) {
	result := make(map[byte][]byte)

	// スペース・改行を除去
	resp = strings.ReplaceAll(resp, " ", "")
	resp = strings.ReplaceAll(resp, "\r", "")
	resp = strings.ReplaceAll(resp, "\n", "")

	// "41" をマーカーにしてレスポンスを分割
	// 各PIDの応答は "41" + PID(1byte) + データ(可変長)
	pos := 0
	for pos < len(resp) {
		// 次の "41" を探す
		idx := strings.Index(resp[pos:], "41")
		if idx < 0 {
			break
		}
		pos += idx

		// "41" の後にPIDバイト(2文字)が必要
		if pos+4 > len(resp) {
			break
		}

		pidHex := resp[pos+2 : pos+4]
		pidVal, err := strconv.ParseUint(pidHex, 16, 8)
		if err != nil {
			pos += 2
			continue
		}
		pid := byte(pidVal)

		// このPIDがリクエストしたものか確認
		requested := false
		for _, rp := range requestedPIDs {
			if rp == pid {
				requested = true
				break
			}
		}
		if !requested {
			pos += 2
			continue
		}

		// データ長を決定（PIDによって異なる）
		dataLen := pidDataLength(pid)
		dataStart := pos + 4
		dataEnd := dataStart + dataLen*2

		if dataEnd > len(resp) {
			// データが足りない場合、残り全部をデータとみなす
			dataEnd = len(resp)
		}

		hexData := resp[dataStart:dataEnd]
		if len(hexData)%2 != 0 {
			pos += 4
			continue
		}

		data := make([]byte, len(hexData)/2)
		for i := 0; i < len(hexData); i += 2 {
			val, err := strconv.ParseUint(hexData[i:i+2], 16, 8)
			if err != nil {
				break
			}
			data[i/2] = byte(val)
		}

		result[pid] = data
		pos = dataEnd
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("マルチPIDレスポンスのパースに失敗: %s", resp)
	}

	return result, nil
}

// pidDataLength はPIDのデータバイト数を返す
func pidDataLength(pid byte) int {
	switch pid {
	case 0x0C: // RPM — 2バイト
		return 2
	case 0x10: // MAF — 2バイト
		return 2
	case 0x1F: // Run time — 2バイト
		return 2
	case 0x04, 0x05, 0x0B, 0x0D, 0x0F, 0x2F: // 1バイト系
		return 1
	default:
		return 2 // デフォルト2バイト
	}
}

// ScanSupportedPIDs は対応PIDをスキャンする
func (e *ELM327) ScanSupportedPIDs() ([]byte, error) {
	supported := []byte{}

	// PID 0x00: PIDs supported [01-20]
	ranges := []byte{0x00, 0x20, 0x40, 0x60}
	for _, r := range ranges {
		data, err := e.QueryPID(r)
		if err != nil {
			break // この範囲以降は非対応
		}
		if len(data) >= 4 {
			for i := 0; i < 32; i++ {
				byteIdx := i / 8
				bitIdx := 7 - (i % 8)
				if data[byteIdx]&(1<<bitIdx) != 0 {
					supported = append(supported, r+byte(i)+1)
				}
			}
		}
	}

	return supported, nil
}

// parseHexResponse はELM327のHEXレスポンスをバイト列にパースする
func parseHexResponse(resp string) ([]byte, error) {
	// "4110ABCD" → [AB, CD] (先頭2バイトはモード+PIDのエコー)
	resp = strings.ReplaceAll(resp, " ", "")
	resp = strings.ReplaceAll(resp, "\r", "")
	resp = strings.ReplaceAll(resp, "\n", "")

	if len(resp) < 4 {
		return nil, fmt.Errorf("レスポンスが短すぎます: %s", resp)
	}

	// 先頭4文字（モード応答+PID）をスキップ
	hexData := resp[4:]
	if len(hexData)%2 != 0 {
		return nil, fmt.Errorf("不正なHEXデータ: %s", hexData)
	}

	result := make([]byte, len(hexData)/2)
	for i := 0; i < len(hexData); i += 2 {
		val, err := strconv.ParseUint(hexData[i:i+2], 16, 8)
		if err != nil {
			return nil, fmt.Errorf("HEXパースエラー: %w", err)
		}
		result[i/2] = byte(val)
	}

	return result, nil
}
