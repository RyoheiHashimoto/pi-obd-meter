package can

import (
	"math"
	"testing"
)

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

// ATF 油温の区分。「1段 = 油の寿命が半分」で刻んである。
//
// 実測 24.1時間 (2026-08-27〜09-01、426,440サンプル) の分布:
//
//	中央 85℃  p90 107  p99 115  最高 116.7
//	緑59% / 黄緑21% / 黄14% / 橙5% / 赤0%
func TestATFLevel(t *testing.T) {
	cases := []struct {
		temp float64
		want string
		why  string
	}{
		{74.7, "", "停車アイドルの中央値"},
		{80.3, "", "街乗りの中央値"},
		{86.8, "", "流れの良い道の中央値"},
		{89.9, "", "境界の直下"},
		{90.0, "warm", "劣化2倍。高速に乗るとここから"},
		{99.9, "warm", "高速巡航の中央値"},
		{100.0, "caution", "劣化4倍"},
		{108.3, "caution", "100-120km/h 巡航の中央値"},
		{110.0, "hot", "劣化8倍。新東名で58分続いた領域"},
		{116.7, "hot", "24時間の実測での最高値"},
		{120.0, "danger", "劣化16倍。実測では未到達"},
		{135.0, "danger", ""},
	}
	for _, c := range cases {
		if got := ATFLevel(c.temp); got != c.want {
			t.Errorf("%.1f℃: %q, want %q  (%s)", c.temp, got, c.want, c.why)
		}
	}
}

// 区分の境目が「劣化2倍ごと」になっていること。
// 業界の経験則では 20°F (11.1℃) ごとに油の寿命が半減する。
func TestATFLevel_BoundariesDoubleDegradation(t *testing.T) {
	const step = 20.0 / 1.8 // 20°F を ℃ に
	bounds := []float64{atfWarmC, atfCautionC, atfHotC, atfDangerC}
	for i := 1; i < len(bounds); i++ {
		gap := bounds[i] - bounds[i-1]
		if math.Abs(gap-10.0) > 0.01 {
			t.Errorf("%.0f℃ と %.0f℃ の間隔が %.1f℃。10℃刻みを崩している", bounds[i-1], bounds[i], gap)
		}
		// 10℃ は 11.1℃ の 0.90 段ぶん。劣化倍率にして 1.87 倍。
		ratio := math.Pow(2, gap/step)
		if ratio < 1.7 || ratio > 2.1 {
			t.Errorf("%.0f→%.0f℃ で劣化 %.2f倍。1段=2倍という前提から外れている", bounds[i-1], bounds[i], ratio)
		}
	}
}
