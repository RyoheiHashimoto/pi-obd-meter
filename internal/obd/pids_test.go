package obd

import (
	"math"
	"testing"
)

const epsilon = 0.01

func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > epsilon {
		t.Errorf("%s: got %.4f, want %.4f", name, got, want)
	}
}

// --- CalcFuelRateLph ---

func TestCalcFuelRateLph_MAF(t *testing.T) {
	tests := []struct {
		name string
		maf  float64
		want float64
	}{
		{
			name: "typical MAF 5.0 g/s",
			maf:  5.0,
			// (5.0 / 14.7) / (0.745 * 1000) * 3600 = 1.6447...
			want: (5.0 / StoichAFR) / (GasolineDensity * 1000) * 3600,
		},
		{
			name: "zero MAF",
			maf:  0,
			want: 0,
		},
		{
			name: "high MAF 50 g/s (WOT)",
			maf:  50.0,
			want: (50.0 / StoichAFR) / (GasolineDensity * 1000) * 3600,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &OBDData{HasMAF: true, MAF: tt.maf}
			got := d.CalcFuelRateLph()
			assertFloat(t, "FuelRateLph", got, tt.want)
		})
	}
}

func TestCalcFuelRateLph_MAP(t *testing.T) {
	cfg := EngineConfig{
		DisplacementL:        1.348,
		ThermalEfficiency:    0.28,
		VolumetricEfficiency: 0.85,
	}

	tests := []struct {
		name    string
		rpm     float64
		mapKpa  float64
		iat     float64
		wantGt0 bool
	}{
		{
			name:    "typical idle",
			rpm:     800,
			mapKpa:  30,
			iat:     25,
			wantGt0: true,
		},
		{
			name:    "high load",
			rpm:     6000,
			mapKpa:  90,
			iat:     40,
			wantGt0: true,
		},
		{
			name:    "zero RPM",
			rpm:     0,
			mapKpa:  30,
			iat:     25,
			wantGt0: false,
		},
		{
			name:    "zero MAP",
			rpm:     800,
			mapKpa:  0,
			iat:     25,
			wantGt0: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &OBDData{
				HasMAF:         false,
				RPM:            tt.rpm,
				IntakeManifold: tt.mapKpa,
				IntakeAirTemp:  tt.iat,
				engineCfg:      cfg,
			}
			got := d.CalcFuelRateLph()
			if tt.wantGt0 && got <= 0 {
				t.Errorf("expected positive fuel rate, got %.4f", got)
			}
			if !tt.wantGt0 && got != 0 {
				t.Errorf("expected 0 fuel rate, got %.4f", got)
			}
		})
	}
}

func TestCalcFuelRateLph_MAFPriority(t *testing.T) {
	// MAF が利用可能な場合は MAP の値に関係なく MAF 方式を使用する
	d := &OBDData{
		HasMAF:         true,
		MAF:            5.0,
		RPM:            3000,
		IntakeManifold: 80,
		IntakeAirTemp:  30,
		engineCfg: EngineConfig{
			DisplacementL:        1.348,
			VolumetricEfficiency: 0.85,
		},
	}

	mafOnly := &OBDData{HasMAF: true, MAF: 5.0}
	assertFloat(t, "MAF takes precedence", d.CalcFuelRateLph(), mafOnly.CalcFuelRateLph())
}

// --- CalcInstantFuelEconomy ---

func TestCalcInstantFuelEconomy(t *testing.T) {
	tests := []struct {
		name  string
		speed float64
		maf   float64
		want  float64
	}{
		{
			name:  "60 km/h at 3.0 L/h = 20.0 km/L",
			speed: 60,
			maf:   3.0 * (StoichAFR * GasolineDensity * 1000) / 3600, // reverse-engineer MAF for 3.0 L/h
			want:  20.0,
		},
		{
			name:  "stopped",
			speed: 0,
			maf:   5.0,
			want:  0,
		},
		{
			name:  "zero fuel rate",
			speed: 60,
			maf:   0,
			want:  0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &OBDData{HasMAF: true, MAF: tt.maf, SpeedKmh: tt.speed}
			got := d.CalcInstantFuelEconomy()
			assertFloat(t, "InstantEcon", got, tt.want)
		})
	}
}

// --- CalcEstimatedPowerKW ---

func TestCalcEstimatedPowerKW(t *testing.T) {
	cfg := EngineConfig{ThermalEfficiency: 0.28}

	t.Run("typical fuel rate", func(t *testing.T) {
		// 5.0 L/h の燃料消費時の出力
		maf := 5.0 * (StoichAFR * GasolineDensity * 1000) / 3600
		d := &OBDData{HasMAF: true, MAF: maf, engineCfg: cfg}

		// 手計算: 5.0 L/h → g/s → × 44000 J/g × 0.28 / 1000
		fuelGPS := 5.0 * GasolineDensity * 1000 / 3600
		want := fuelGPS * GasolineHeatValue * 0.28 / 1000.0

		got := d.CalcEstimatedPowerKW()
		assertFloat(t, "PowerKW", got, want)
	})

	t.Run("zero fuel rate", func(t *testing.T) {
		d := &OBDData{HasMAF: true, MAF: 0, engineCfg: cfg}
		if got := d.CalcEstimatedPowerKW(); got != 0 {
			t.Errorf("expected 0, got %.4f", got)
		}
	})
}

// --- CalcEstimatedPowerPS ---

func TestCalcEstimatedPowerPS(t *testing.T) {
	cfg := EngineConfig{ThermalEfficiency: 0.28}
	maf := 5.0 * (StoichAFR * GasolineDensity * 1000) / 3600
	d := &OBDData{HasMAF: true, MAF: maf, engineCfg: cfg}

	kw := d.CalcEstimatedPowerKW()
	ps := d.CalcEstimatedPowerPS()
	assertFloat(t, "PS = KW * 1.3596", ps, kw*1.3596)
}

// --- CalcEstimatedTorqueNm ---

func TestCalcEstimatedTorqueNm(t *testing.T) {
	cfg := EngineConfig{ThermalEfficiency: 0.28}

	t.Run("known power and RPM", func(t *testing.T) {
		maf := 10.0 * (StoichAFR * GasolineDensity * 1000) / 3600
		d := &OBDData{HasMAF: true, MAF: maf, RPM: 3000, engineCfg: cfg}

		powerKW := d.CalcEstimatedPowerKW()
		omega := 2 * math.Pi * 3000 / 60.0
		want := (powerKW * 1000) / omega

		got := d.CalcEstimatedTorqueNm()
		assertFloat(t, "TorqueNm", got, want)
	})

	t.Run("zero RPM", func(t *testing.T) {
		maf := 5.0 * (StoichAFR * GasolineDensity * 1000) / 3600
		d := &OBDData{HasMAF: true, MAF: maf, RPM: 0, engineCfg: cfg}
		if got := d.CalcEstimatedTorqueNm(); got != 0 {
			t.Errorf("expected 0, got %.4f", got)
		}
	})

	t.Run("zero power", func(t *testing.T) {
		d := &OBDData{HasMAF: true, MAF: 0, RPM: 3000, engineCfg: cfg}
		if got := d.CalcEstimatedTorqueNm(); got != 0 {
			t.Errorf("expected 0, got %.4f", got)
		}
	})
}
