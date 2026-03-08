package main

import (
	"math"
	"testing"
)

func TestCalcFuelEconomy_EngineStopped(t *testing.T) {
	// エンジン停止 (speed<0.5, rpm<100) → 0
	got := calcFuelEconomy(0, 0, 0, 0, false, 1.3)
	if got != 0 {
		t.Errorf("engine stopped: got %.1f, want 0", got)
	}
}

func TestCalcFuelEconomy_LowSpeed(t *testing.T) {
	// 低速域 (<10 km/h) → 0（クリープ等は燃費表示しない）
	got := calcFuelEconomy(5, 800, 30, 0, false, 1.3)
	if got != 0 {
		t.Errorf("low speed: got %.1f, want 0", got)
	}
}

func TestCalcFuelEconomy_NormalDriving_LoadRPM(t *testing.T) {
	// 60km/h, 2000rpm, 30%負荷, MAFなし, 1.3L
	got := calcFuelEconomy(60, 2000, 30, 0, false, 1.3)
	if got <= 0 || got > maxDisplayKmL {
		t.Errorf("normal driving (load×RPM): got %.1f, expected positive value", got)
	}
	// 一般的な1.3Lの燃費は10-25 km/Lの範囲
	if got < 5 || got > 50 {
		t.Errorf("normal driving (load×RPM): got %.1f, expected 5-50 range", got)
	}
}

func TestCalcFuelEconomy_NormalDriving_MAF(t *testing.T) {
	// 60km/h, MAF=5g/s(一般的な巡航値)
	got := calcFuelEconomy(60, 2000, 30, 5.0, true, 1.3)
	if got <= 0 || got > maxDisplayKmL {
		t.Errorf("normal driving (MAF): got %.1f, expected positive value", got)
	}
	if got < 5 || got > 50 {
		t.Errorf("normal driving (MAF): got %.1f, expected 5-50 range", got)
	}
}

func TestCalcFuelEconomy_HighLoad(t *testing.T) {
	// 高負荷: 120km/h, 4000rpm, 80%負荷 → 燃費が悪い
	got := calcFuelEconomy(120, 4000, 80, 0, false, 1.3)
	if got <= 0 {
		t.Errorf("high load: got %.1f, expected positive", got)
	}
	// 高負荷時は低めの燃費
	normalGot := calcFuelEconomy(60, 2000, 30, 0, false, 1.3)
	if got >= normalGot {
		t.Errorf("high load (%.1f) should be worse than normal (%.1f)", got, normalGot)
	}
}

func TestCalcFuelEconomy_EngineBraking(t *testing.T) {
	// エンブレ: 速度あり、負荷ほぼ0 → idle燃料消費(0.8L/h)で計算
	// 60km/h / 0.8L/h = 75 km/L
	got := calcFuelEconomy(60, 2000, 0, 0, false, 1.3)
	if got <= 0 {
		t.Errorf("engine braking: got %.1f, expected positive", got)
	}
	// 低負荷なので高燃費が出る
	normalGot := calcFuelEconomy(60, 2000, 30, 0, false, 1.3)
	if got <= normalGot {
		t.Errorf("engine braking (%.1f) should be better than normal driving (%.1f)", got, normalGot)
	}
}

func TestCalcFuelEconomy_ZeroMAF_Fallback(t *testing.T) {
	// hasMAF=true でも MAF=0 → load×RPM にフォールバック
	mafZero := calcFuelEconomy(60, 2000, 30, 0, true, 1.3)
	noMAF := calcFuelEconomy(60, 2000, 30, 0, false, 1.3)
	if mafZero != noMAF {
		t.Errorf("MAF=0 should fall back to load×RPM: MAF0=%.1f, noMAF=%.1f", mafZero, noMAF)
	}
}

func TestCalcFuelEconomy_Idle(t *testing.T) {
	// アイドリング: 速度0, RPM 800 → 0 (低速域で非表示)
	got := calcFuelEconomy(0, 800, 20, 0, false, 1.3)
	if got != 0 {
		t.Errorf("idle: got %.1f, want 0 (below min display speed)", got)
	}
}

func TestCalcFuelEconomy_CappedAtMax(t *testing.T) {
	// MAFが非常に小さい値 → maxDisplayKmL でキャップ
	got := calcFuelEconomy(60, 2000, 5, 0.01, true, 1.3)
	if got > maxDisplayKmL {
		t.Errorf("cap: got %.1f, should not exceed %.1f", got, maxDisplayKmL)
	}
}

func TestCalcFuelEconomy_MAFPriority(t *testing.T) {
	// MAFがある場合、load×RPMより優先される
	mafResult := calcFuelEconomy(60, 2000, 30, 5.0, true, 1.3)
	noMafResult := calcFuelEconomy(60, 2000, 30, 5.0, false, 1.3)
	// 両方とも有効な値を返すが、異なる計算パス
	if mafResult <= 0 || noMafResult <= 0 {
		t.Errorf("both paths should return positive: MAF=%.1f, noMAF=%.1f", mafResult, noMafResult)
	}
	// MAFとload×RPMは一般的に異なる結果
	if math.Abs(mafResult-noMafResult) < 0.001 {
		t.Log("MAF and load×RPM gave same result (coincidental)")
	}
}

func TestCalcFuelEconomy_LargerDisplacement(t *testing.T) {
	// 排気量が大きいほど燃費が悪い (load×RPMベース)
	small := calcFuelEconomy(60, 2000, 30, 0, false, 1.3)
	large := calcFuelEconomy(60, 2000, 30, 0, false, 2.0)
	if large >= small {
		t.Errorf("larger displacement should use more fuel: 1.3L=%.1f, 2.0L=%.1f", small, large)
	}
}
