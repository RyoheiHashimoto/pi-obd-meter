package obd

import (
	"math"
	"testing"
)

func TestParsePID_RPM(t *testing.T) {
	data := &OBDData{}
	parsePID(data, PIDEngineRPM, []byte{0x1A, 0x20}) // (0x1A20) / 4 = 1672
	want := float64(0x1A20) / 4.0
	if data.RPM != want {
		t.Errorf("RPM: got %.1f, want %.1f", data.RPM, want)
	}
}

func TestParsePID_Speed(t *testing.T) {
	data := &OBDData{}
	parsePID(data, PIDVehicleSpeed, []byte{60})
	if data.SpeedKmh != 60 {
		t.Errorf("Speed: got %.0f, want 60", data.SpeedKmh)
	}
}

func TestParsePID_Load(t *testing.T) {
	data := &OBDData{}
	parsePID(data, PIDEngineLoad, []byte{128}) // 128/255*100 ≈ 50.2%
	want := float64(128) * 100.0 / 255.0
	if math.Abs(data.EngineLoad-want) > 0.1 {
		t.Errorf("Load: got %.1f, want %.1f", data.EngineLoad, want)
	}
}

func TestParsePID_Throttle(t *testing.T) {
	data := &OBDData{}
	parsePID(data, PIDThrottlePosition, []byte{255}) // 100%
	want := 100.0
	if math.Abs(data.ThrottlePos-want) > 0.1 {
		t.Errorf("Throttle: got %.1f, want %.1f", data.ThrottlePos, want)
	}
}

func TestParsePID_Coolant(t *testing.T) {
	data := &OBDData{}
	parsePID(data, PIDCoolantTemp, []byte{130}) // 130-40 = 90℃
	if data.CoolantTemp != 90 {
		t.Errorf("Coolant: got %.0f, want 90", data.CoolantTemp)
	}
}

func TestParsePID_FuelTank(t *testing.T) {
	data := &OBDData{}
	parsePID(data, PIDFuelTankLevel, []byte{128}) // 128/255*100 ≈ 50.2%
	want := float64(128) * 100.0 / 255.0
	if math.Abs(data.FuelTankLevel-want) > 0.1 {
		t.Errorf("FuelTank: got %.1f, want %.1f", data.FuelTankLevel, want)
	}
}

func TestParsePID_ShortData(t *testing.T) {
	// データが空でもパニックしない
	data := &OBDData{}
	parsePID(data, PIDEngineRPM, []byte{})
	if data.RPM != 0 {
		t.Errorf("RPM should be 0 with empty data, got %.1f", data.RPM)
	}

	parsePID(data, PIDVehicleSpeed, []byte{})
	if data.SpeedKmh != 0 {
		t.Errorf("Speed should be 0 with empty data, got %.0f", data.SpeedKmh)
	}
}

func TestParsePID_RPMZero(t *testing.T) {
	data := &OBDData{}
	parsePID(data, PIDEngineRPM, []byte{0x00, 0x00})
	if data.RPM != 0 {
		t.Errorf("RPM: got %.1f, want 0", data.RPM)
	}
}

func TestParsePID_RPMMax(t *testing.T) {
	data := &OBDData{}
	parsePID(data, PIDEngineRPM, []byte{0xFF, 0xFF}) // 65535/4 = 16383.75
	want := float64(0xFFFF) / 4.0
	if data.RPM != want {
		t.Errorf("RPM: got %.2f, want %.2f", data.RPM, want)
	}
}
