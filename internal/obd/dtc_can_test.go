package obd

import "testing"

func TestParseDTCPayloadWithCount(t *testing.T) {
	// 43 02 = Mode 03 応答、DTC 2件。0120 と 0340。
	codes, ok := ParseDTCPayload([]byte{0x43, 0x02, 0x01, 0x20, 0x03, 0x40})
	if !ok {
		t.Fatal("ok=false")
	}
	if len(codes) != 2 {
		t.Fatalf("件数 = %d, want 2 (%v)", len(codes), codes)
	}
	if codes[0].Code != "P0120" || codes[1].Code != "P0340" {
		t.Errorf("codes = %s, %s", codes[0].Code, codes[1].Code)
	}
	// コード表にあるものは説明が付く
	if codes[0].Description == "" {
		t.Error("説明が空")
	}
}

// 件数バイトを持たない実装。0x01 が件数なら残り 2 件のはずで
// 辻褄が合わないので、そのままコードとして読む。
func TestParseDTCPayloadWithoutCount(t *testing.T) {
	codes, ok := ParseDTCPayload([]byte{0x43, 0x01, 0x20, 0x03, 0x40})
	if !ok {
		t.Fatal("ok=false")
	}
	if len(codes) != 2 || codes[0].Code != "P0120" || codes[1].Code != "P0340" {
		t.Fatalf("codes = %v", codes)
	}
}

// 「読んで0件」と「読んでいない」を区別する。
func TestParseDTCPayloadEmpty(t *testing.T) {
	codes, ok := ParseDTCPayload([]byte{0x43, 0x00})
	if !ok {
		t.Fatal("ok=false。読めて0件は ok=true でなければならない")
	}
	if codes == nil {
		t.Fatal("nil を返した。nil は「まだ読んでいない」の意味なので空スライスであるべき")
	}
	if len(codes) != 0 {
		t.Errorf("件数 = %d, want 0", len(codes))
	}
}

func TestParseDTCPayloadPending(t *testing.T) {
	codes, ok := ParseDTCPayload([]byte{0x47, 0x01, 0x01, 0x71})
	if !ok || len(codes) != 1 || codes[0].Code != "P0171" {
		t.Fatalf("Mode 07 が読めない: ok=%v codes=%v", ok, codes)
	}
}

func TestParseDTCPayloadRejectsOtherModes(t *testing.T) {
	for _, p := range [][]byte{nil, {}, {0x41, 0x0C, 0x1A, 0xF8}, {0x62, 0x17, 0xB3, 0x50}} {
		if _, ok := ParseDTCPayload(p); ok {
			t.Errorf("DTC でない応答を受け入れた: %X", p)
		}
	}
}

// パディングの 0000 はコードにしない。
func TestParseDTCPayloadSkipsPadding(t *testing.T) {
	codes, _ := ParseDTCPayload([]byte{0x43, 0x01, 0x20, 0x00, 0x00, 0x00, 0x00})
	if len(codes) != 1 || codes[0].Code != "P0120" {
		t.Fatalf("codes = %v", codes)
	}
}

func TestDecodeDTCWordCategories(t *testing.T) {
	cases := map[uint16]string{
		0x0120: "P0120",
		0x4140: "C0140",
		0x8103: "B0103",
		0xC155: "U0155",
		0x0000: "",
	}
	for v, want := range cases {
		if got := decodeDTCWord(v); got != want {
			t.Errorf("decodeDTCWord(%04X) = %q, want %q", v, got, want)
		}
	}
}

func TestFuelSystemStatusString(t *testing.T) {
	if got := FuelSystemStatusString(FuelSysClosedLoop); got != "クローズドループ" {
		t.Errorf("got %q", got)
	}
	// 未取得は空。0 に文言を付けると「取れていない」が「正常」に化ける。
	if got := FuelSystemStatusString(0); got != "" {
		t.Errorf("未取得が %q になった", got)
	}
}
