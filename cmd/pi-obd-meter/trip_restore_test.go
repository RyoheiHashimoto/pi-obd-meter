package main

import "testing"

func TestCalcRestoredTripKm(t *testing.T) {
	tests := []struct {
		name         string
		totalKm      float64
		lastRefuelKm float64
		wantKm       float64
		wantOK       bool
	}{
		{"通常: 給油から264km走った", 108980, 108716, 264, true},
		{"給油直後はトリップ0付近", 108716.5, 108716, 0.5, true},
		{"給油記録が未設定なら復元しない", 108980, 0, 0, false},
		{"累計が給油時と同じなら復元しない", 108716, 108716, 0, false},
		{"累計が給油時を下回るのは異常なので復元しない", 108700, 108716, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			km, ok := calcRestoredTripKm(tt.totalKm, tt.lastRefuelKm)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && (km-tt.wantKm > 0.001 || km-tt.wantKm < -0.001) {
				t.Errorf("km = %.3f, want %.3f", km, tt.wantKm)
			}
		})
	}
}

// #118 の中身: 給油直後は GAS 側の値の方が小さくなる。
// 従来は「大きいときだけ上書き」だったため、この場合に古い値が残り続けた。
func TestCalcRestoredTripKm_ShrinksAfterRefuel(t *testing.T) {
	const totalKm = 108980.0

	// 給油前: 前回給油は 108716 km 地点。トリップは 264 km
	before, ok := calcRestoredTripKm(totalKm, 108716)
	if !ok || before < 263 || before > 265 {
		t.Fatalf("給油前のトリップ = %.1f, want 264 付近", before)
	}

	// 給油した。last_refuel_km が現在地に更新される。
	after, ok := calcRestoredTripKm(totalKm, totalKm-0.2)
	if !ok {
		t.Fatal("給油直後に復元できていない")
	}
	if after >= before {
		t.Errorf("給油後のトリップ %.1f が給油前 %.1f 以上。値が縮まっていない", after, before)
	}
	if after > 1.0 {
		t.Errorf("給油直後のトリップ = %.1f, ほぼ0であるべき", after)
	}
}

// WiFi 接続待ちの間に走っても結果が変わらないこと。
// 従来は手元の値と比較していたため、接続の速さで結果が変わるレースだった。
func TestCalcRestoredTripKm_NoRaceOnWiFiDelay(t *testing.T) {
	const lastRefuelKm = 108716.0

	// WiFi がすぐ繋がった場合 (まだ一度も走っていない)
	fast, _ := calcRestoredTripKm(108980, lastRefuelKm)
	// WiFi に60秒かかり、その間に 1.5km 走った場合
	slow, _ := calcRestoredTripKm(108981.5, lastRefuelKm)

	// どちらも「累計 − 前回給油」なので、走った分だけ素直に増える
	if diff := slow - fast; diff < 1.4 || diff > 1.6 {
		t.Errorf("走行分の差 = %.2f, want 1.5", diff)
	}
	// 従来の実装ではここで「復元されない」分岐に落ちていた
	if fast <= 0 {
		t.Error("WiFi が速いと復元されない、という従来の不具合が残っている")
	}
}
