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
	b0Pct, b1, odoKm := DecodeElectric(data)

	// B0: 0x72=114, /2.55 = 44.7%（燃料残量の可能性・未確定）
	if math.Abs(b0Pct-44.7) > 0.1 {
		t.Errorf("B0Pct = %f, want ~44.7", b0Pct)
	}
	// B1: 0x99=153（生値のまま返す）
	if math.Abs(b1-153) > 0.01 {
		t.Errorf("B1 = %f, want 153", b1)
	}
	// オドメーター: 0x266D=9837, *10 = 98,370 km
	// このキャプチャ採取時の実走行距離。2026-08 時点では 108,120 km まで進んでおり整合する。
	if math.Abs(odoKm-98370) > 0.01 {
		t.Errorf("OdometerKm = %f, want 98370", odoKm)
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
func TestDecodeATCtrl_MechanicalRatio(t *testing.T) {
	// 2026-08-27 の実走 310サンプルで最頻だった raw 値を使う。
	// B2 は機械ギア比そのもので、トルコンの滑りは含まれない。
	tests := []struct {
		name     string
		b0, b2   byte
		wantGear int
		wantRT   float64
	}{
		{"1速 ラップを戻す", 0x01, 26, 1, 2.82},
		{"2速 ラップしない", 0x02, 150, 2, 1.50},
		{"3速 ラップしない", 0x03, 100, 3, 1.00},
		{"4速 ラップしない", 0x04, 73, 4, 0.73},
		{"R ラップを戻す", 0x10, 14, 0, 2.70},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gear, ratio := DecodeATCtrl([8]byte{tt.b0, 0, tt.b2, 0, 0, 0, 0, 0})
			if gear != tt.wantGear {
				t.Errorf("gear = %d, want %d", gear, tt.wantGear)
			}
			if diff := ratio - tt.wantRT; diff > 0.001 || diff < -0.001 {
				t.Errorf("ratio = %.4f, want %.4f", ratio, tt.wantRT)
			}
		})
	}
}

// 変速の過渡では B2 が隣のギアの値を取る。以前はこれを「ラップ」と誤判定して
// +2.56 し、3.29 や 3.56 という存在しないギア比を出力していた (#132)。
func TestDecodeATCtrl_ShiftTransientIsNotWrapped(t *testing.T) {
	tests := []struct {
		name   string
		b0, b2 byte
		want   float64
	}{
		{"3速なのにB2が4速の値", 0x03, 73, 0.73},
		{"1速なのにB2が過渡値0.95", 0x01, 95, 0.95},
		{"2速なのにB2が3速の値", 0x02, 100, 1.00},
		{"4速なのにB2が3速の値", 0x04, 100, 1.00},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ratio := DecodeATCtrl([8]byte{tt.b0, 0, tt.b2, 0, 0, 0, 0, 0})
			if diff := ratio - tt.want; diff > 0.001 || diff < -0.001 {
				t.Errorf("ratio = %.4f, want %.4f (2.56を足してはいけない)", ratio, tt.want)
			}
		})
	}
}
