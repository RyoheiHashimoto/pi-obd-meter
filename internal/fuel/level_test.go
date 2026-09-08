package fuel

import (
	"math"
	"testing"
	"time"
)

// #188: 航続距離は走行中に出すので、停車中しか更新されない settled 値では足りない。
func TestLevelPt_UpdatesWhileMoving(t *testing.T) {
	d := NewDetector("")

	// stopped=false でも残量は取り込まれる
	d.Update(50.0, false)

	pt, ok := d.LevelPt()
	if !ok {
		t.Fatal("走行中に残量が得られていない")
	}
	if math.Abs(pt-50.0) > 0.01 {
		t.Errorf("初回サンプル = %.2f, want 50.00 (生値そのまま)", pt)
	}

	// 給油検出の窓は走行中サンプルで汚れないこと
	if d.Settled() {
		t.Error("走行中サンプルで settled になっている")
	}
}

func TestLevelPt_NilDetector(t *testing.T) {
	var d *Detector
	if _, ok := d.LevelPt(); ok {
		t.Error("nil Detector が有効な残量を返した")
	}
}

func TestLevelPt_UnavailableBeforeFirstSample(t *testing.T) {
	d := NewDetector("")
	if _, ok := d.LevelPt(); ok {
		t.Error("サンプル前なのに有効扱いになっている")
	}
	// 0以下の生値 (燃料計が読めない) は取り込まない
	d.Update(0, true)
	if _, ok := d.LevelPt(); ok {
		t.Error("残量0ptを有効な値として取り込んでいる")
	}
}

// 走行中は 24〜33ポイント振れる。平滑化がその揺れを均せること。
func TestUpdateLevel_SmoothsSloshing(t *testing.T) {
	d := NewDetector("")
	const trueLevel = 40.0
	const amplitude = 15.0 // 振幅±15 = 振れ幅30ポイント

	now := time.Unix(0, 0)
	d.updateLevel(trueLevel, now)

	// 50ms 周期で5分ぶん、±15pt を交互に与える
	for i := 0; i < 6000; i++ {
		now = now.Add(50 * time.Millisecond)
		v := trueLevel + amplitude
		if i%2 == 1 {
			v = trueLevel - amplitude
		}
		d.updateLevel(v, now)
	}

	pt, ok := d.LevelPt()
	if !ok {
		t.Fatal("残量が得られていない")
	}
	if math.Abs(pt-trueLevel) > 1.0 {
		t.Errorf("平滑化後 = %.2f, want %.1f ±1.0 (振れ幅30ptを均せていない)", pt, trueLevel)
	}
}

// 周期が変わっても時定数の意味が変わらないこと。
// CAN 直結は 50ms、ELM327 は 200ms で呼ばれる。
func TestUpdateLevel_RateIndependent(t *testing.T) {
	run := func(period time.Duration) float64 {
		d := NewDetector("")
		now := time.Unix(0, 0)
		d.updateLevel(90.0, now) // 満タンから始める

		// 3分ぶん、残量20ptを与え続ける (給油の逆: 一気に減った想定)
		for elapsed := time.Duration(0); elapsed < 3*time.Minute; elapsed += period {
			now = now.Add(period)
			d.updateLevel(20.0, now)
		}
		pt, _ := d.LevelPt()
		return pt
	}

	fast := run(50 * time.Millisecond)
	slow := run(200 * time.Millisecond)

	if math.Abs(fast-slow) > 0.5 {
		t.Errorf("周期で結果が変わった: 50ms=%.2f, 200ms=%.2f", fast, slow)
	}
	// 指数追従なので、経過 3分 = 時定数 (2分) の 1.5倍で、差の
	// 1 - e^-1.5 = 77.7% が埋まる。90 → 20 なら 35.6 付近に来るはず。
	want := 20.0 + 70.0*math.Exp(-1.5)
	if math.Abs(fast-want) > 1.0 {
		t.Errorf("3分後 = %.2f, want %.2f 付近 (時定数 %.0fs の指数追従)", fast, want, levelTauSec)
	}
}

// サンプルが長時間途切れても一気に飛ばないこと。
func TestUpdateLevel_ClampsLongGap(t *testing.T) {
	d := NewDetector("")
	now := time.Unix(0, 0)
	d.updateLevel(90.0, now)

	// 1時間の空白のあとに1サンプル
	d.updateLevel(20.0, now.Add(time.Hour))

	pt, _ := d.LevelPt()
	// alpha は最大 0.5 に抑えられるので、中点より下には行かない
	if pt < 55.0 {
		t.Errorf("1サンプルで %.2f まで飛んだ。空白のクランプが効いていない", pt)
	}
}
