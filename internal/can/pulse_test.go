package can

import (
	"math"
	"testing"
	"time"
)

func TestPulseCounter_Rollover(t *testing.T) {
	var p PulseCounter

	// 初回は基準値として記録するだけで進まない
	p.Add(250)
	if p.Total() != 0 {
		t.Fatalf("初回で進んだ: %d", p.Total())
	}

	// 通常の増加
	p.Add(253)
	if p.Total() != 3 {
		t.Errorf("Total = %d, want 3", p.Total())
	}

	// 255 を跨ぐラップ
	p.Add(5) // 253 -> 5 は +8
	if p.Total() != 11 {
		t.Errorf("ラップ後 Total = %d, want 11", p.Total())
	}

	// 実測で観測された最大 delta は 11 (115km/h)
	p.Add(16)
	if p.Total() != 22 {
		t.Errorf("Total = %d, want 22", p.Total())
	}
}

func TestPulseCounter_Invalidate(t *testing.T) {
	var p PulseCounter
	p.Add(10)
	p.Add(20) // +10
	if p.Total() != 10 {
		t.Fatalf("Total = %d, want 10", p.Total())
	}

	// 通信断: 断絶中に進んだ分は失われるが、それまでの累積は保持する
	p.Invalidate()
	p.Add(200) // 基準値として捨てられる
	if p.Total() != 10 {
		t.Errorf("Invalidate後に進んだ: %d, want 10", p.Total())
	}
	p.Add(210) // +10
	if p.Total() != 20 {
		t.Errorf("復帰後 Total = %d, want 20", p.Total())
	}
}

// TestPulseCounter_DistanceKm は実機ログの実測値で係数を検証する。
// 2026-08-25 の走行: 18,882パルス、メーター TRIP A 実測 7.4km (199.7→207.1)。
func TestPulseCounter_DistanceKm(t *testing.T) {
	var p PulseCounter
	p.SetTotal(18882)
	got := p.DistanceKm()
	const want = 7.352 // 18882 / 2568.14

	if math.Abs(got-want) > 0.001 {
		t.Errorf("DistanceKm = %.4f, want %.4f", got, want)
	}
	// TRIP A は0.1km刻みのため真値は 7.30〜7.50。その範囲に入ること。
	if got < 7.30 || got > 7.50 {
		t.Errorf("メーター実測 7.4±0.1km の範囲外: %.4f", got)
	}
}

// TestPulsesPerKm_OdometerBoundary は10km境界での実測値を検証する。
// 独立した7ファイル・17区間の平均が 25,681.4 パルス/10km だった。
func TestPulsesPerKm_OdometerBoundary(t *testing.T) {
	var p PulseCounter
	p.SetTotal(25681)
	got := p.DistanceKm()
	if math.Abs(got-10.0) > 0.01 {
		t.Errorf("10km境界の実測パルスで %.4f km。想定 10.00 km", got)
	}
	if math.Abs(MetersPerPulse-0.389387) > 0.000001 {
		t.Errorf("MetersPerPulse = %.6f, want 0.389387", MetersPerPulse)
	}
}

// 欠測が長いと8bitのラップを取りこぼす。時間で基準を捨てること。
func TestPulseCounter_DropsBaselineOnLongGap(t *testing.T) {
	base := time.Unix(1700000000, 0)
	var p PulseCounter

	p.AddAt(0, base)
	p.AddAt(50, base.Add(500*time.Millisecond))
	if got := p.Total(); got != 50 {
		t.Fatalf("連続受信の累積 = %d, want 50", got)
	}

	// 4秒欠測した。この間に何周したか分からない。
	p.AddAt(10, base.Add(4500*time.Millisecond))
	if got := p.Total(); got != 50 {
		t.Errorf("欠測後に加算された。累積 = %d, want 50 (基準を取り直すだけ)", got)
	}

	// 取り直した基準から再開する
	p.AddAt(30, base.Add(5000*time.Millisecond))
	if got := p.Total(); got != 70 {
		t.Errorf("再開後の累積 = %d, want 70", got)
	}
}

// 上限ちょうどまでは連続とみなす。
func TestPulseCounter_KeepsBaselineWithinGap(t *testing.T) {
	base := time.Unix(1700000000, 0)
	var p PulseCounter
	p.AddAt(0, base)
	p.AddAt(100, base.Add(MaxPulseGap))
	if got := p.Total(); got != 100 {
		t.Errorf("上限内なのに基準を捨てた。累積 = %d, want 100", got)
	}
}

// Invalidate 後は時刻もリセットされ、次の受信が基準になる。
func TestPulseCounter_InvalidateResetsTime(t *testing.T) {
	base := time.Unix(1700000000, 0)
	var p PulseCounter
	p.AddAt(0, base)
	p.AddAt(20, base.Add(100*time.Millisecond))
	p.Invalidate()
	p.AddAt(200, base.Add(200*time.Millisecond))
	if got := p.Total(); got != 20 {
		t.Errorf("Invalidate 後に加算された。累積 = %d, want 20", got)
	}
}
