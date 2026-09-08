package main

import (
	"math"
	"testing"
)

// #188: 航続距離が実燃料残量を使うこと。
func TestCalcRangeToEmpty(t *testing.T) {
	const tank = 46.0
	tests := []struct {
		name       string
		eco        float64
		tripKm     float64
		remainingL float64
		want       float64
	}{
		// 実残量があればトリップ距離に依存しない。
		{"実残量ベース", 15.0, 300, 6.7, 100.5},
		{"実残量ベース・トリップが違っても同じ", 15.0, 0, 6.7, 100.5},
		// 実残量が無ければ従来式に落ちる。
		{"代用式", 15.0, 300, 0, 46*15 - 300},
		{"代用式・満タン超過は0でクリップ", 15.0, 10000, 0, 0},
		// センダーが満タン側でクリップしてタンク超過が来ても頭打ち。
		{"実残量がタンク超過", 15.0, 0, 60, tank * 15},
		// 平均燃費が壊れていたら出さない。
		{"燃費が異常に大きい", 200.0, 0, 6.7, 0},
		{"燃費が0", 0, 0, 6.7, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calcRangeToEmpty(tank, tt.eco, tt.tripKm, tt.remainingL)
			if math.Abs(got-tt.want) > 0.01 {
				t.Errorf("calcRangeToEmpty() = %.2f, want %.2f", got, tt.want)
			}
		})
	}
}

// 2026-09-06 04:34 の実測。純正の給油警告灯が点灯した時点。
func TestCalcRangeToEmpty_警告灯点灯時の実測(t *testing.T) {
	const tank, pt, litersPerPoint = 46.0, 14.90, 0.45
	remainingL := pt * litersPerPoint // 6.705 L
	if remainingL < 6.0 || remainingL > 7.0 {
		t.Fatalf("残量 %.2f L。DYデミオの警告灯は残り6〜7Lで点くので、この範囲を外れたら換算が疑わしい", remainingL)
	}
	// 当時の平均燃費 309.5km / 29.87L = 10.36 km/L
	got := calcRangeToEmpty(tank, 309.5/29.87, 309.5, remainingL)
	if got < 50 || got > 90 {
		t.Errorf("航続 %.0f km。6.7L × 10.4km/L ≒ 70km を大きく外れている", got)
	}
	// 従来式なら 46×10.36−309.5 ≒ 167km と、実残量ベースの倍以上を表示していた。
	old := calcRangeToEmpty(tank, 309.5/29.87, 309.5, 0)
	if old <= got {
		t.Errorf("従来式 %.0f km が実残量ベース %.0f km を上回らない。前提が変わった可能性", old, got)
	}
	t.Logf("実残量ベース %.0f km / 従来式 %.0f km", got, old)
}
