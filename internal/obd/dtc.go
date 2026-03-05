package obd

import (
	"fmt"
	"strconv"
	"strings"
)

// DTC は故障コード1件を表す
type DTC struct {
	Code        string `json:"code"`        // "P0420" 形式
	Description string `json:"description"` // 日本語の説明
	Severity    string `json:"severity"`    // "info", "warning", "critical"
}

// DTCResult はDTC読み取りの結果
type DTCResult struct {
	MIL      bool  `json:"mil"`       // チェックランプ点灯中か
	DTCCount int   `json:"dtc_count"` // 記録されている故障コード数
	Codes    []DTC `json:"codes"`     // 故障コードの一覧
}

// ReadDTCCount はMILステータスとDTC数を取得する（Mode 01, PID 01）
func (e *ELM327) ReadDTCCount() (mil bool, count int, err error) {
	data, err := e.QueryPID(0x01)
	if err != nil {
		return false, 0, fmt.Errorf("PID 0x01 取得失敗: %w", err)
	}
	if len(data) < 4 {
		return false, 0, fmt.Errorf("PID 0x01 データ不足: %d bytes", len(data))
	}

	// Byte A: bit7 = MIL on/off, bit0-6 = DTC数
	mil = (data[0] & 0x80) != 0
	count = int(data[0] & 0x7F)
	return mil, count, nil
}

// ReadDTCs は記録されている故障コードを読み取る（Mode 03）
func (e *ELM327) ReadDTCs() (*DTCResult, error) {
	// まずMILとDTC数を確認
	mil, count, err := e.ReadDTCCount()
	if err != nil {
		// PID 0x01が取れなくても Mode 03 は試す
		mil = false
		count = -1
	}

	result := &DTCResult{
		MIL:      mil,
		DTCCount: count,
		Codes:    []DTC{},
	}

	// Mode 03: 故障コード読み取り
	resp, err := e.sendCommand("03")
	if err != nil {
		// "NO DATA" = 故障コードなし
		if strings.Contains(err.Error(), "NO DATA") {
			result.DTCCount = 0
			return result, nil
		}
		return nil, fmt.Errorf("Mode 03 失敗: %w", err)
	}

	// レスポンスをパース
	codes := parseDTCResponse(resp)
	for _, code := range codes {
		dtc := DTC{
			Code:        code,
			Description: dtcDescription(code),
			Severity:    dtcSeverity(code),
		}
		result.Codes = append(result.Codes, dtc)
	}

	if result.DTCCount < 0 {
		result.DTCCount = len(result.Codes)
	}

	return result, nil
}

// parseDTCResponse はMode 03のレスポンスをDTCコード文字列に変換する
// レスポンス例: "43 01 20 03 40 00 00" → ["P0120", "P0340"]
// "43" はMode 03の応答ヘッダ、その後2バイトずつがDTCコード
func parseDTCResponse(resp string) []string {
	resp = strings.ReplaceAll(resp, " ", "")
	resp = strings.ReplaceAll(resp, "\r", "")
	resp = strings.ReplaceAll(resp, "\n", "")

	var codes []string

	// 複数フレームの場合、"43" が複数回出る
	// "43" を見つけてその後のデータを処理
	pos := 0
	for pos < len(resp) {
		idx := strings.Index(resp[pos:], "43")
		if idx < 0 {
			break
		}
		pos += idx + 2 // "43" の後

		// 1フレームに最大3コード（各2バイト = 4文字）
		for i := 0; i < 3; i++ {
			if pos+4 > len(resp) {
				break
			}

			hexPair := resp[pos : pos+4]
			pos += 4

			// "0000" はパディング（コードなし）
			if hexPair == "0000" {
				continue
			}

			code := decodeDTC(hexPair)
			if code != "" {
				codes = append(codes, code)
			}
		}
	}

	return codes
}

// decodeDTC は2バイトのHEXをDTCコード文字列に変換する
// 例: "0120" → "P0120", "C140" → ????
// ビット構成: [15:14]=カテゴリ, [13:12]=第2桁, [11:8]=第3桁, [7:4]=第4桁, [3:0]=第5桁
func decodeDTC(hex4 string) string {
	if len(hex4) != 4 {
		return ""
	}

	val, err := strconv.ParseUint(hex4, 16, 16)
	if err != nil {
		return ""
	}
	if val == 0 {
		return ""
	}

	// 先頭2ビット → カテゴリ
	categories := []byte{'P', 'C', 'B', 'U'}
	cat := categories[(val>>14)&0x03]

	// 残り14ビット → 4桁の数字
	digit2 := (val >> 12) & 0x03
	digit3 := (val >> 8) & 0x0F
	digit4 := (val >> 4) & 0x0F
	digit5 := val & 0x0F

	return fmt.Sprintf("%c%d%X%X%X", cat, digit2, digit3, digit4, digit5)
}

// === DTC辞書（DYデミオで出やすいもの中心） ===

func dtcDescription(code string) string {
	if desc, ok := dtcDatabase[code]; ok {
		return desc
	}

	// カテゴリ別の汎用説明
	if len(code) > 0 {
		switch code[0] {
		case 'P':
			return "パワートレイン系故障コード（詳細不明）"
		case 'C':
			return "シャシー系故障コード（詳細不明）"
		case 'B':
			return "ボディ系故障コード（詳細不明）"
		case 'U':
			return "通信系故障コード（詳細不明）"
		}
	}
	return "不明な故障コード"
}

func dtcSeverity(code string) string {
	if sev, ok := dtcSeverityMap[code]; ok {
		return sev
	}
	return "warning"
}

// dtcDatabase はよく見る故障コードの日本語説明
var dtcDatabase = map[string]string{
	// エンジン制御系
	"P0100": "MAFセンサー回路異常",
	"P0101": "MAFセンサー範囲/性能異常",
	"P0102": "MAFセンサー回路 低入力",
	"P0103": "MAFセンサー回路 高入力",
	"P0105": "MAP/大気圧センサー回路異常",
	"P0106": "MAP/大気圧センサー範囲/性能異常",
	"P0107": "MAP/大気圧センサー回路 低入力",
	"P0108": "MAP/大気圧センサー回路 高入力",
	"P0110": "吸気温度センサー回路異常",
	"P0112": "吸気温度センサー回路 低入力",
	"P0113": "吸気温度センサー回路 高入力",
	"P0115": "冷却水温センサー回路異常",
	"P0117": "冷却水温センサー回路 低入力",
	"P0118": "冷却水温センサー回路 高入力",
	"P0120": "スロットル位置センサー回路異常",
	"P0121": "スロットル位置センサー範囲/性能異常",
	"P0122": "スロットル位置センサー回路 低入力",
	"P0123": "スロットル位置センサー回路 高入力",
	"P0125": "冷却水温不足（燃料制御不安定）",
	"P0128": "サーモスタット異常（冷却水温が規定温度に達しない）",
	"P0130": "O2センサー回路異常 (Bank1-Sensor1)",
	"P0131": "O2センサー低電圧 (Bank1-Sensor1)",
	"P0132": "O2センサー高電圧 (Bank1-Sensor1)",
	"P0133": "O2センサー応答遅延 (Bank1-Sensor1)",
	"P0134": "O2センサー無反応 (Bank1-Sensor1)",
	"P0135": "O2センサーヒーター回路異常 (Bank1-Sensor1)",
	"P0136": "O2センサー回路異常 (Bank1-Sensor2)",
	"P0137": "O2センサー低電圧 (Bank1-Sensor2)",
	"P0138": "O2センサー高電圧 (Bank1-Sensor2)",
	"P0139": "O2センサー応答遅延 (Bank1-Sensor2)",
	"P0140": "O2センサー無反応 (Bank1-Sensor2)",
	"P0141": "O2センサーヒーター回路異常 (Bank1-Sensor2)",

	// 燃料系
	"P0170": "燃料トリム異常 (Bank1)",
	"P0171": "燃料系リーン（薄い） (Bank1)",
	"P0172": "燃料系リッチ（濃い） (Bank1)",
	"P0174": "燃料系リーン（薄い） (Bank2)",
	"P0175": "燃料系リッチ（濃い） (Bank2)",

	// ミスファイア
	"P0300": "ランダムミスファイア検出",
	"P0301": "1番シリンダー ミスファイア",
	"P0302": "2番シリンダー ミスファイア",
	"P0303": "3番シリンダー ミスファイア",
	"P0304": "4番シリンダー ミスファイア",

	// 点火系
	"P0325": "ノックセンサー回路異常 (Bank1)",
	"P0335": "クランク角センサー回路異常",
	"P0336": "クランク角センサー範囲/性能異常",
	"P0340": "カム角センサー回路異常",
	"P0341": "カム角センサー範囲/性能異常",

	// 触媒・排気系
	"P0401": "EGR流量不足",
	"P0402": "EGR流量過多",
	"P0420": "触媒効率低下 (Bank1)",
	"P0421": "触媒ウォームアップ効率低下 (Bank1)",
	"P0430": "触媒効率低下 (Bank2)",
	"P0440": "エバポシステム異常",
	"P0441": "エバポパージ流量異常",
	"P0442": "エバポシステム微小リーク",
	"P0446": "エバポベントバルブ制御回路異常",
	"P0455": "エバポシステム大リーク",

	// スロットル・アイドル
	"P0500": "車速センサー異常",
	"P0505": "アイドル制御系異常",
	"P0506": "アイドル回転数 低すぎ",
	"P0507": "アイドル回転数 高すぎ",

	// その他よく出るもの
	"P0560": "バッテリー電圧異常",
	"P0562": "バッテリー電圧 低い",
	"P0563": "バッテリー電圧 高い",
	"P0600": "CAN通信異常",
	"P0700": "AT制御系異常",
	"P0715": "タービン回転数センサー異常",
	"P0720": "出力軸回転数センサー異常",
	"P0725": "エンジン回転数入力回路異常",
	"P0731": "1速 ギア比異常",
	"P0732": "2速 ギア比異常",
	"P0733": "3速 ギア比異常",
	"P0734": "4速 ギア比異常",

	// マツダ固有コード（P2xxx系）
	"P2004": "インマニランナー制御 スタック（開固着）",
	"P2006": "インマニランナー制御 スタック（閉固着）",
	"P2008": "インマニランナー制御回路 開",
	"P2009": "インマニランナー制御回路 低",
	"P2096": "ポスト触媒燃料トリム リーン (Bank1)",
	"P2097": "ポスト触媒燃料トリム リッチ (Bank1)",
	"P2177": "リーン異常 低負荷時",
	"P2178": "リッチ異常 低負荷時",
	"P2187": "リーン異常 アイドル時",
	"P2188": "リッチ異常 アイドル時",
}

// dtcSeverityMap は重要度の判定
var dtcSeverityMap = map[string]string{
	// critical: すぐ対処が必要
	"P0300": "critical", "P0301": "critical", "P0302": "critical",
	"P0303": "critical", "P0304": "critical",
	"P0335": "critical", "P0336": "critical",
	"P0340": "critical", "P0341": "critical",
	"P0500": "critical",

	// info: 経過観察で良い
	"P0128": "info",
	"P0420": "info", "P0421": "info", "P0430": "info",
	"P0440": "info", "P0441": "info", "P0442": "info",
	"P0446": "info", "P0455": "info",
	"P0506": "info", "P0507": "info",
}
