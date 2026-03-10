package main

// 燃費計算用の物理定数
const (
	stoichiometricAFR  = 14.7   // ガソリンの理論空燃比 (空気kg / 燃料kg)
	gasolineDensityGL  = 750.0  // ガソリン密度 (g/L)
	airDensityGL       = 1.225  // 標準大気密度 (g/L = kg/m³)
	idleFuelRateCoeff  = 0.6    // アイドル燃料消費係数 (L/h per L排気量)
	maxDisplayKmL      = 99.9   // 燃費表示の上限値 (km/L)
	minDisplaySpeedKm  = 10.0   // 燃費表示の最低速度 (km/h)
	atmosphericKPa     = 101.3  // 標準大気圧 (kPa)
	engineBrakeMAPKPa  = 35.0   // エンブレ判定MAP閾値 (kPa) — 強い負圧
)

// calcFuelEconomy は瞬間燃費(km/L)を計算する
//
// 優先順位: MAF > MAP (Speed-Density) > 負荷×RPM
//   - MAF対応: 燃料レート = MAF(g/s) × 3600 / (14.7 × 750) L/h
//   - MAP対応: Speed-Density法: 吸入空気量 = MAP/101.3 × RPM/2 × 排気量 / 60 L/s
//   - 上記なし: 燃料レート ≈ RPM × 負荷% × 排気量 / 定数 L/h
//
// correction は燃料レート補正係数（理論値と実燃費の乖離を補正、1.0=補正なし）
func calcFuelEconomy(speed, rpm, load, maf float64, hasMAF bool, intakeMAP float64, hasMAP bool, displacementL, correction float64) (kmL, rateLH float64) {
	if speed < 0.5 && rpm < 100 {
		return 0, 0 // エンジン停止
	}

	var fuelRateLH float64
	if hasMAF && maf > 0 {
		// MAFから直接計算: g/s → L/h
		fuelRateLH = maf * 3600.0 / (stoichiometricAFR * gasolineDensityGL)
	} else if hasMAP && intakeMAP > 0 && rpm >= 100 {
		// Speed-Density法: MAPから吸入空気量を推定
		// 4ストロークなので吸気は2回転に1回
		// MAPを大気圧で正規化して体積効率の代わりに使う
		ve := intakeMAP / atmosphericKPa // 体積効率の近似（MAP/大気圧）
		airFlowLS := ve * (rpm / 2.0) * displacementL / 60.0 // L/s of air
		airMassGS := airFlowLS * airDensityGL                 // g/s
		fuelRateLH = airMassGS * 3600.0 / (stoichiometricAFR * gasolineDensityGL)
	} else {
		// 負荷×RPM×排気量から推定（フォールバック）
		// 4ストロークなので吸気は2回転に1回
		// 体積効率を負荷%で近似
		if rpm < 100 || load < 0.1 {
			fuelRateLH = idleFuelRateCoeff * displacementL
		} else {
			airFlowEstimate := (rpm / 2.0) * (load / 100.0) * displacementL / 60.0 // L/s of air
			airMassGS := airFlowEstimate * airDensityGL                            // g/s
			fuelRateLH = airMassGS * 3600.0 / (stoichiometricAFR * gasolineDensityGL)
		}
	}

	// 補正係数を適用（暖機増量・過渡補正等の理論値との乖離を補正）
	if correction > 0 {
		fuelRateLH *= correction
	}

	if fuelRateLH < 0.01 {
		return -1, fuelRateLH // エンブレ・燃料カット（-1 = 特別表示）
	}

	// エンブレ検出: MAP対応時は負圧で判定（より正確）、非対応時は負荷で判定
	if speed >= minDisplaySpeedKm {
		if hasMAP && intakeMAP > 0 && intakeMAP < engineBrakeMAPKPa {
			return -1, fuelRateLH // MAP低い = スロットル閉 = エンブレ
		}
		if load < 5.0 {
			return -1, fuelRateLH // 負荷ベースのフォールバック
		}
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
