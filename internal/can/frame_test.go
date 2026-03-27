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
