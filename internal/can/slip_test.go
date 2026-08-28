package can

import (
	"math"
	"testing"
)

// 2026-08-27 実走のロックアップ係合中サンプル (rpm, 車速, ギア)。
// この区間の滑り比は定義上ほぼ 1.000 になるはず。
var lockedSamples = []struct {
	rpm, speed float64
	gear       int
}{
	{1815.75, 64.645, 4},
	{2084.20, 45.870, 3},
	{2113.20, 46.690, 3},
	{2139.80, 47.670, 3},
	{2167.20, 48.460, 3},
}

func TestSlipCalibrator_LockedIsUnity(t *testing.T) {
	c := NewSlipCalibrator()
	for _, s := range lockedSamples {
		mech := MechGearRatio(s.gear)
		slip, ok := c.Slip(s.rpm, s.speed, mech)
		if !ok {
			t.Fatalf("滑りが計算できない: %+v", s)
		}
		// 初期定数のままでも 4速ロックアップは 1.0 付近に来る
		if s.gear == 4 && math.Abs(slip-1.0) > 0.05 {
			t.Errorf("4速ロック時の滑り = %.4f, 1.0 から離れすぎ", slip)
		}
	}
}

func TestSlipCalibrator_ObserveConverges(t *testing.T) {
	c := NewSlipCalibrator()
	// 実際の k が初期値と 10% ずれた車両を模擬する
	const trueK = DefaultSpeedRatioK * 1.10
	mech := MechGearRatio(4)
	for i := 0; i < 2000; i++ {
		speed := 80.0
		c.Observe(speed*mech*trueK, speed, mech)
	}
	if math.Abs(c.K()-trueK)/trueK > 0.01 {
		t.Errorf("校正後 k = %.3f, want %.3f 付近", c.K(), trueK)
	}
	// 校正後はロック中の滑りが 1.000 になる
	slip, _ := c.Slip(80.0*mech*trueK, 80.0, mech)
	if math.Abs(slip-1.0) > 0.01 {
		t.Errorf("校正後の滑り = %.4f, want 1.0", slip)
	}
}

// 変速過渡やノイズで飛んだ値は校正に取り込まない。
func TestSlipCalibrator_RejectsOutliers(t *testing.T) {
	c := NewSlipCalibrator()
	before := c.K()
	mech := MechGearRatio(3)
	for i := 0; i < 100; i++ {
		c.Observe(6000, 20, mech) // k = 300、明らかに異常
	}
	if c.K() != before {
		t.Errorf("異常値で k が動いた: %.3f → %.3f", before, c.K())
	}
}

func TestSlipCalibrator_LockPctClamped(t *testing.T) {
	c := NewSlipCalibrator()
	mech := MechGearRatio(3)
	// 大きく滑っている状態 (発進直後など)
	if pct := c.LockPct(3000, 30, mech); pct >= 100 || pct <= 0 {
		t.Errorf("滑走中のロック率 = %.1f%%, 0〜100 の内側であるべき", pct)
	}
	// タービンが先行する惰行状態でも 100% で頭打ちにする
	if pct := c.LockPct(1000, 60, mech); pct != 100 {
		t.Errorf("惰行時のロック率 = %.1f%%, want 100", pct)
	}
}

func TestSlipCalibrator_NilSafe(t *testing.T) {
	var c *SlipCalibrator
	c.Observe(2000, 60, 1.0)
	if c.K() != DefaultSpeedRatioK {
		t.Errorf("nil の K() = %.3f", c.K())
	}
}

// 外れ値ガードは学習値からの相対で判定すること。
//
// 従来は初期値からの相対だったため、タイヤ外径や最終減速比が初期値と
// 大きく違う車両では正しい値まで捨てて永久に収束しなかった。
func TestSlipCalibrator_ConvergesToDistantK(t *testing.T) {
	c := NewSlipCalibrator()
	const trueK = DefaultSpeedRatioK * 1.35 // 初期値から35%離れた車両
	mech := MechGearRatio(4)
	for i := 0; i < 20000; i++ {
		c.Observe(80.0*mech*trueK, 80.0, mech)
	}
	if math.Abs(c.K()-trueK)/trueK > 0.02 {
		t.Errorf("校正後 k = %.2f, want %.2f 付近 (初期値から遠い値へ収束できない)", c.K(), trueK)
	}
}

// ありえない値は学習値に近くても弾く。
func TestSlipCalibrator_RejectsImplausibleAbsolute(t *testing.T) {
	c := NewSlipCalibrator()
	before := c.K()
	mech := MechGearRatio(3)
	for i := 0; i < 500; i++ {
		c.Observe(10.0*mech*minPlausibleK*0.5, 10.0, mech) // 常識外に小さい k
	}
	if c.K() != before {
		t.Errorf("ありえない k を取り込んだ: %.2f → %.2f", before, c.K())
	}
}
