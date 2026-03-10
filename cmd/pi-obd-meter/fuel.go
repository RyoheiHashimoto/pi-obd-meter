package main

// 燃費計算用の物理定数
const (
	stoichiometricAFR = 14.7  // ガソリンの理論空燃比 (空気kg / 燃料kg)
	gasolineDensityGL = 750.0 // ガソリン密度 (g/L)
	airDensityGL      = 1.225 // 標準大気密度 (g/L = kg/m³)
	idleFuelRateLH    = 0.8   // アイドリング時の最低燃料消費量 (L/h)
	maxDisplayKmL     = 99.9  // 燃費表示の上限値 (km/L)
	minDisplaySpeedKm = 10.0  // 燃費表示の最低速度 (km/h)
)

// calcFuelEconomy は瞬間燃費(km/L)を計算する
// MAF対応: 燃料レート = MAF(g/s) × 3600 / (14.7 × 750) L/h
// MAF非対応: 燃料レート ≈ RPM × 負荷% × 排気量 / 定数 L/h
func calcFuelEconomy(speed, rpm, load, maf float64, hasMAF bool, displacementL float64) (kmL, rateLH float64) {
	if speed < 0.5 && rpm < 100 {
		return 0, 0 // エンジン停止
	}

	var fuelRateLH float64
	if hasMAF && maf > 0 {
		// MAFから直接計算: g/s → L/h
		fuelRateLH = maf * 3600.0 / (stoichiometricAFR * gasolineDensityGL)
	} else {
		// 負荷×RPM×排気量から推定
		// 4ストロークなので吸気は2回転に1回
		// 体積効率を負荷%で近似
		if rpm < 100 || load < 0.1 {
			fuelRateLH = idleFuelRateLH
		} else {
			airFlowEstimate := (rpm / 2.0) * (load / 100.0) * displacementL / 60.0 // L/s of air
			airMassGS := airFlowEstimate * airDensityGL                            // g/s
			fuelRateLH = airMassGS * 3600.0 / (stoichiometricAFR * gasolineDensityGL)
		}
	}

	if fuelRateLH < 0.01 {
		return -1, fuelRateLH // エンブレ・燃料カット（-1 = 特別表示）
	}
	// エンブレ検出（負荷ベース）: 走行中に極低負荷 → 燃料カットと判定
	if speed >= minDisplaySpeedKm && load < 5.0 {
		return -1, fuelRateLH
	}
	if speed < minDisplaySpeedKm {
		return 0, fuelRateLH // 低速域（クリープ等）は燃費表示しない
	}
	kmL = speed / fuelRateLH
	if kmL > maxDisplayKmL {
		kmL = maxDisplayKmL
	}
	return kmL, fuelRateLH
}
