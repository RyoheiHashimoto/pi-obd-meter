package obd

// CAN 直結での故障コードの読み取り。
//
// ELM327 経由 (dtc.go) は応答が "43 01 20 ..." という文字列で来るが、
// CAN 直結では ISO-TP で組み上げたバイト列が来る。コード表と重要度の
// 判定は共通なので、ここではバイト列を DTC に直すところだけを持つ。

// ParseDTCPayload は Mode 03 / 07 の応答ペイロードから故障コードを取り出す。
//
// payload は ISO-TP で組み上げたあとの、先頭がモード応答バイト
// (0x43 または 0x47) の列。ok=false は「この応答は DTC ではない」で、
// 空スライス + ok=true が「読めて 0 件」を意味する。この2つを混ぜると
// 「まだ読んでいない」と「異常なし」が区別できなくなる。
func ParseDTCPayload(payload []byte) (codes []DTC, ok bool) {
	if len(payload) < 1 {
		return nil, false
	}
	if payload[0] != 0x43 && payload[0] != 0x47 {
		return nil, false
	}
	body := payload[1:]

	// CAN の応答は先頭に件数バイトが入る実装と入らない実装がある。
	// 件数が残りの長さと辻褄が合うときだけ件数とみなして読み飛ばす。
	// 辻褄で判断するのは、件数を取り違えると先頭のコードが化けるため。
	//
	// 残りが偶数であることまで見る。整数除算の切り捨てだけで判定すると
	// "43 01 20 03 40" (件数なし・2件) の残り3バイトが「件数1」と一致して
	// しまい、先頭が P0120 ではなく P2003 に化ける。
	if n := len(body) - 1; n > 0 && n%2 == 0 && int(body[0]) == n/2 {
		body = body[1:]
	} else if len(body) == 1 && body[0] == 0 {
		// "43 00" = 件数0。読めて0件。
		body = nil
	}

	codes = []DTC{}
	for i := 0; i+1 < len(body); i += 2 {
		v := uint16(body[i])<<8 | uint16(body[i+1])
		// 0x0000 は末尾のパディング。コードではない。
		code := decodeDTCWord(v)
		if code == "" {
			continue
		}
		codes = append(codes, DTC{
			Code:        code,
			Description: dtcDescription(code),
			Severity:    dtcSeverity(code),
		})
	}
	return codes, true
}
