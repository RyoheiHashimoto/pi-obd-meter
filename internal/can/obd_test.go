package can

import "testing"

// 0x1101 のビット割り当て。2026-08-31 に停車・アイドル・アクセル全閉で
// 固定し、20秒踏む→離す→20秒踏む を実施して同定した。
func TestDecodeStatus1101(t *testing.T) {
	cases := []struct {
		raw                byte
		wantBrake, wantFan bool
	}{
		{0x00, false, false},
		{0x01, false, true},  // ファンのみ
		{0x02, true, false},  // ブレーキのみ
		{0x03, true, true},   // 両方
		{0x08, false, false}, // bit3 は別の用途。誤検出しないこと
		{0x0A, true, false},  // bit3 + ブレーキ
		{0x0B, true, true},
	}
	for _, c := range cases {
		b, f, ok := DecodeStatus1101([]byte{c.raw})
		if !ok {
			t.Fatalf("0x%02X: デコードできない", c.raw)
		}
		if b != c.wantBrake || f != c.wantFan {
			t.Errorf("0x%02X: ブレーキ=%v ファン=%v, want %v/%v", c.raw, b, f, c.wantBrake, c.wantFan)
		}
	}
	if _, _, ok := DecodeStatus1101(nil); ok {
		t.Error("空データを受け入れてはいけない")
	}
}

// 0x1103 bit2 = エアコンコンプレッサー。
func TestDecodeACCompressor(t *testing.T) {
	for raw, want := range map[byte]bool{0x00: false, 0x04: true, 0x0C: true, 0x08: false, 0x1C: true} {
		on, ok := DecodeACCompressor([]byte{raw})
		if !ok {
			t.Fatalf("0x%02X: デコードできない", raw)
		}
		if on != want {
			t.Errorf("0x%02X: %v, want %v", raw, on, want)
		}
	}
	if _, ok := DecodeACCompressor(nil); ok {
		t.Error("空データを受け入れてはいけない")
	}
}

// 0x3201 = 勾配。符号付き16bit で負が登り。
// 大橋JCT (8.9%) で -690、比叡山の急勾配区間で -2210 が観測されている。
func TestDecodeGrade(t *testing.T) {
	cases := []struct {
		hi, lo byte
		want   int
	}{
		{0x00, 0x00, 0},
		{0x00, 0x1E, 30},
		{0xFD, 0x4E, -690},  // 大橋JCT の実測値
		{0xF7, 0x5E, -2210}, // 比叡山の実測値
		{0x05, 0x28, 1320},  // 登り切って正に反転した瞬間
	}
	for _, c := range cases {
		got, ok := DecodeGrade([]byte{c.hi, c.lo})
		if !ok {
			t.Fatalf("%02X%02X: デコードできない", c.hi, c.lo)
		}
		if got != c.want {
			t.Errorf("%02X%02X: %d, want %d", c.hi, c.lo, got, c.want)
		}
	}
	if _, ok := DecodeGrade([]byte{0x00}); ok {
		t.Error("1バイトを受け入れてはいけない")
	}
}
