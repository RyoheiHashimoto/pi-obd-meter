package can

import (
	"math"
	"testing"
)

func TestDecodeEngine_Idle(t *testing.T) {
	// candump: 0x201 [8] 0A EF 7D 28 27 10 00 64
	data := [8]byte{0x0A, 0xEF, 0x7D, 0x28, 0x27, 0x10, 0x00, 0x64}
	rpm, speed, load := DecodeEngine(data)

	// RPM: 0x0AEF = 2799, /4 = 699.75
	if math.Abs(rpm-699.75) > 0.1 {
		t.Errorf("RPM = %f, want ~699.75", rpm)
	}
	// Speed: 0x2710 = 10000, (10000-10000)/100 = 0
	if speed != 0 {
		t.Errorf("Speed = %f, want 0", speed)
	}
	// Load: 0x00 = 0
	if load != 0 {
		t.Errorf("Load = %f, want 0", load)
	}
}

func TestDecodeEngine_Moving(t *testing.T) {
	// 60 km/h = raw 16000 = 0x3E80
	// 2000 RPM = raw 8000 = 0x1F40
	// 30% load = 30
	data := [8]byte{0x1F, 0x40, 0x00, 0x00, 0x3E, 0x80, 0x1E, 0x00}
	rpm, speed, load := DecodeEngine(data)

	if math.Abs(rpm-2000) > 0.1 {
		t.Errorf("RPM = %f, want 2000", rpm)
	}
	if math.Abs(speed-60) > 0.1 {
		t.Errorf("Speed = %f, want 60", speed)
	}
	if load != 30 {
		t.Errorf("Load = %f, want 30", load)
	}
}

func TestDecodeElectric(t *testing.T) {
	// candump: 0x430 [7] 72 99 00 00 26 6D 60
	data := [8]byte{0x72, 0x99, 0x00, 0x00, 0x26, 0x6D, 0x60, 0x00}
	altLoad, voltage, baro := DecodeElectric(data)

	// Alt load: 0x72=114, /2.55 = 44.7%
	if math.Abs(altLoad-44.7) > 0.1 {
		t.Errorf("AltLoad = %f, want ~44.7", altLoad)
	}
	// Voltage: 0x99=153, *0.08 = 12.24V
	if math.Abs(voltage-12.24) > 0.01 {
		t.Errorf("Voltage = %f, want ~12.24", voltage)
	}
	// Baro: 0x266D=9837, /100 = 98.37 kPa
	if math.Abs(baro-98.37) > 0.01 {
		t.Errorf("Baro = %f, want ~98.37", baro)
	}
}

func TestDecodeEngine_NegativeSpeed(t *testing.T) {
	// raw speed < 10000 should clamp to 0
	data := [8]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00}
	_, speed, _ := DecodeEngine(data)
	if speed != 0 {
		t.Errorf("Speed = %f, want 0 (clamped)", speed)
	}
}

func TestDecodeWheelSpeed(t *testing.T) {
	// candump: 0x4B0 [8] 27 10 27 10 27 10 27 10 → all 0 km/h
	data := [8]byte{0x27, 0x10, 0x27, 0x10, 0x27, 0x10, 0x27, 0x10}
	speed := DecodeWheelSpeed(data)
	if speed != 0 {
		t.Errorf("WheelSpeed = %f, want 0", speed)
	}
}

// TestDecodeATCtrl_EffectiveRatio は 0x230 B2 の実効ギア比とオーバーフロー補正を検証する。
// 期待値は実機ログ (2026-08-20 / 08-25) の実測に基づく。
func TestDecodeATCtrl_EffectiveRatio(t *testing.T) {
	tests := []struct {
		name      string
		b0, b2    byte
		wantGear  int
		wantRatio float64
	}{
		// ロックアップ中は滑りゼロ = 実効比が機械比と一致する。
		// 実測: 4速ロック中 B2 = 72.9 ± 0.7 (機械比 0.726)
		{"4速 ロックアップ中", 0x04, 73, 4, 0.73},
		// 実測: 3速ロック中の滑り比 1.0000 ± 0.0008
		{"3速 ロックアップ中", 0x03, 100, 3, 1.00},

		// 滑りがある通常状態。機械比を上回る。
		{"3速 滑りあり", 0x03, 110, 3, 1.10},
		{"2速 滑りあり", 0x02, 155, 2, 1.55},

		// オーバーフロー: 1速は機械比 2.816 が 1バイト上限 2.55 を超えるため常にラップ
		{"1速 ラップ", 0x01, 68, 1, 3.24},

		// オーバーフロー: 2速でも発進時など滑りが大きいとラップする。
		// 実測では2速サンプルの 11.4% がこの状態だった。
		// 従来は1速とRにしか補正しておらず、0.16 のような不正値を出力していた。
		{"2速 ラップ (従来バグ)", 0x02, 20, 2, 2.76},
		{"3速 ラップ", 0x03, 30, 3, 2.86},

		// R は機械比 2.7 前後で常にラップする
		{"R ラップ", 0x10, 20, 0, 2.76},

		// N/P はギア比が意味を持たない (補正もしない)
		{"N/P", 0xF0, 0, 0, 0.00},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d [8]byte
			d[0] = tt.b0
			d[2] = tt.b2
			gear, ratio := DecodeATCtrl(d)
			if gear != tt.wantGear {
				t.Errorf("gear = %d, want %d", gear, tt.wantGear)
			}
			if diff := ratio - tt.wantRatio; diff > 0.005 || diff < -0.005 {
				t.Errorf("ratio = %.4f, want %.4f", ratio, tt.wantRatio)
			}
		})
	}
}

// TestMechGearRatio_SlipRatio は実効ギア比から滑り比が正しく求まることを検証する。
// ロックアップ係合中は 1.000 になる (実測 1.0000 ± 0.0008)。
func TestMechGearRatio_SlipRatio(t *testing.T) {
	tests := []struct {
		name     string
		b0, b2   byte
		wantSlip float64
	}{
		{"3速 ロック中は滑りゼロ", 0x03, 100, 1.000},
		{"4速 ロック中は滑りゼロ", 0x04, 73, 1.006},
		{"2速 発進時は大きく滑る", 0x02, 20, 1.842},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d [8]byte
			d[0] = tt.b0
			d[2] = tt.b2
			gear, ratio := DecodeATCtrl(d)
			mech := MechGearRatio(gear)
			if mech == 0 {
				t.Fatalf("機械ギア比が取得できない (gear=%d)", gear)
			}
			slip := ratio / mech
			if diff := slip - tt.wantSlip; diff > 0.005 || diff < -0.005 {
				t.Errorf("滑り比 = %.4f, want %.4f", slip, tt.wantSlip)
			}
		})
	}
}
