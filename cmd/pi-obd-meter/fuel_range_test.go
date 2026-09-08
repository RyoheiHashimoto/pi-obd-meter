package main

import "testing"

const (
	testTankL  = 46.0 // DYデミオのタンク容量
	testAvgKmL = 12.0
)

func approx(got, want, tol float64) bool {
	d := got - want
	return d <= tol && d >= -tol
}

// #188: 燃料計が読めるなら、実残量から航続距離を出す。
func TestCalcRangeToEmpty_UsesActualFuelLevel(t *testing.T) {
	// 給油警告灯が点灯した実測時点 (2026-09-06): 残量 14.9pt、トリップ 309.5km
	got := calcRangeToEmpty(testTankL, testAvgKmL, 309.5, 14.9, true)

	// 46L × 14.9% = 6.85L 残 × 12km/L = 82.2km
	if !approx(got, 82.2, 0.5) {
		t.Errorf("航続距離 = %.1f km, want 82.2 付近", got)
	}
}

// 従来式はトリップ距離に依存していた。実残量ベースなら依存しない。
func TestCalcRangeToEmpty_IndependentOfTripKm(t *testing.T) {
	a := calcRangeToEmpty(testTankL, testAvgKmL, 0, 50, true)
	b := calcRangeToEmpty(testTankL, testAvgKmL, 400, 50, true)
	if a != b {
		t.Errorf("トリップ距離で結果が変わった: %.1f vs %.1f", a, b)
	}
}

// #188 の主症状: 給油したのにトリップがリセットされないと、
// 従来式では実際より悲観的な値 (ここでは 0) になっていた。
func TestCalcRangeToEmpty_SurvivesMissedTripReset(t *testing.T) {
	// 満タン給油したが、トリップは 600km のまま残っている
	const staleTrip = 600.0

	old := testTankL*testAvgKmL - staleTrip // 従来式 = 552 - 600 → 負 → 0
	if old > 0 {
		t.Fatalf("前提が崩れている: 従来式が %.1f で正の値", old)
	}

	got := calcRangeToEmpty(testTankL, testAvgKmL, staleTrip, 95.3, true)
	if got < 500 {
		t.Errorf("満タン直後の航続距離 = %.1f km, 500km 以上であるべき", got)
	}
}

// 燃料計が読めないとき (ELM327 経由など) は従来式に落ちる。
func TestCalcRangeToEmpty_FallsBackWithoutLevel(t *testing.T) {
	got := calcRangeToEmpty(testTankL, testAvgKmL, 100, 0, false)
	want := testTankL*testAvgKmL - 100
	if !approx(got, want, 0.01) {
		t.Errorf("フォールバック = %.1f, want %.1f", got, want)
	}
}

func TestCalcRangeToEmpty_Guards(t *testing.T) {
	tests := []struct {
		name       string
		tankL      float64
		avgKmL     float64
		tripKm     float64
		levelPct   float64
		levelValid bool
		want       float64
	}{
		{"平均燃費が未確定なら0", testTankL, 0, 100, 50, true, 0},
		{"タンク容量が未設定なら0", 0, testAvgKmL, 100, 50, true, 0},
		{"残量0ptは従来式に落ちる", testTankL, testAvgKmL, 100, 0, true, testTankL*testAvgKmL - 100},
		{"フォールバックの負値は0でクリップ", testTankL, testAvgKmL, 9999, 0, false, 0},
		{"100%超の生値は100%に抑える", testTankL, testAvgKmL, 0, 120, true, testTankL * testAvgKmL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calcRangeToEmpty(tt.tankL, tt.avgKmL, tt.tripKm, tt.levelPct, tt.levelValid)
			if !approx(got, tt.want, 0.01) {
				t.Errorf("= %.2f, want %.2f", got, tt.want)
			}
		})
	}
}
