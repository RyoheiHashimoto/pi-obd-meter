package sdlui

import (
	"testing"
)

func TestHex(t *testing.T) {
	tests := []struct {
		input string
		want  RGBA
	}{
		{"#f44336", RGBA{0xf4, 0x43, 0x36, 255}},
		{"#000000", RGBA{0, 0, 0, 255}},
		{"#ffffff", RGBA{255, 255, 255, 255}},
		{"#69f0ae", RGBA{0x69, 0xf0, 0xae, 255}},
	}
	for _, tt := range tests {
		got := Hex(tt.input)
		if got != tt.want {
			t.Errorf("Hex(%q) = %+v, want %+v", tt.input, got, tt.want)
		}
	}
}

func TestHSL(t *testing.T) {
	// 赤 (hue=0, sat=100, lum=50)
	red := HSL(0, 100, 50)
	if red.R != 255 || red.G != 0 || red.B != 0 {
		t.Errorf("HSL(0,100,50) = %+v, want red", red)
	}

	// 緑 (hue=120)
	green := HSL(120, 100, 50)
	if green.G != 255 || green.R != 0 || green.B != 0 {
		t.Errorf("HSL(120,100,50) = %+v, want green", green)
	}

	// 青 (hue=240)
	blue := HSL(240, 100, 50)
	if blue.B != 255 || blue.R != 0 || blue.G != 0 {
		t.Errorf("HSL(240,100,50) = %+v, want blue", blue)
	}
}

func TestSpeedColor(t *testing.T) {
	grey := SpeedColor(10)
	if grey != Hex("#78909c") {
		t.Errorf("SpeedColor(10) = %+v, want #78909c", grey)
	}

	red := SpeedColor(130)
	if red != Hex("#f44336") {
		t.Errorf("SpeedColor(130) = %+v, want #f44336", red)
	}
}
