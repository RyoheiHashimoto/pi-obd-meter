package obd

import (
	"fmt"
	"math"
)

// PID定義（DYデミオで使いそうなもの）
const (
	PIDEngineRPM         byte = 0x0C // エンジン回転数
	PIDVehicleSpeed      byte = 0x0D // 車速 (km/h)
	PIDIntakeAirTemp     byte = 0x0F // 吸気温度
	PIDMAF               byte = 0x10 // MAFセンサー (g/s)
	PIDEngineLoad        byte = 0x04 // エンジン負荷 (%)
	PIDCoolantTemp       byte = 0x05 // 冷却水温度
	PIDIntakeManifold    byte = 0x0B // インマニ圧 (kPa) ← DYデミオはMAP式の可能性
	PIDFuelTankLevel     byte = 0x2F // 燃料タンクレベル (%)
	PIDRunTimeSinceStart byte = 0x1F // エンジン始動後の経過時間
)

// OBDData はOBD-2から読み取ったリアルタイムデータ
type OBDData struct {
	RPM            float64      // rpm
	SpeedKmh       float64      // km/h
	MAF            float64      // g/s (取得できない場合は0)
	EngineLoad     float64      // 0-100%
	IntakeManifold float64      // kPa
	IntakeAirTemp  float64      // ℃
	CoolantTemp    float64      // ℃
	FuelTankLevel  float64      // 0-100%
	HasMAF         bool         // MAFセンサーが使えるか
	engineCfg      EngineConfig // 計算用
}

// DYデミオ固有パラメータ
const (
	// ガソリン密度 (g/mL)
	GasolineDensity = 0.745
	// 理論空燃比
	StoichAFR = 14.7
	// ガソリン低位発熱量 (J/g)
	GasolineHeatValue = 44000.0
	// 推定熱効率（初期値、全開加速でカタログ値と照合して調整）
	DefaultThermalEfficiency = 0.28
)

// EngineConfig はエンジン固有の設定値（外部から注入）
type EngineConfig struct {
	DisplacementL        float64 // 排気量 (L) — ZJ-VE: 1.348, ZY-VE: 1.498
	ThermalEfficiency    float64 // 熱効率 (0.25〜0.30)
	VolumetricEfficiency float64 // 体積効率 (0.80〜0.90、満タン法で校正)
}

// Reader はOBD-2データを読み取る
type Reader struct {
	elm           *ELM327
	hasMAF        bool
	supportsMulti bool // マルチPIDリクエスト対応フラグ
	multiTested   bool // マルチPID対応テスト済みフラグ
	EngineCfg     EngineConfig
}

// NewReader は新しいReaderを作成する
func NewReader(elm *ELM327, cfg EngineConfig) *Reader {
	if cfg.DisplacementL <= 0 {
		cfg.DisplacementL = 1.348 // ZJ-VE デフォルト
	}
	if cfg.ThermalEfficiency <= 0 {
		cfg.ThermalEfficiency = DefaultThermalEfficiency
	}
	if cfg.VolumetricEfficiency <= 0 {
		cfg.VolumetricEfficiency = 0.85
	}
	return &Reader{elm: elm, EngineCfg: cfg}
}

// DetectCapabilities はPID 0x00でサポートPIDをスキャンし、燃費計算方式を自動判定する
func (r *Reader) DetectCapabilities() error {
	// PID 0x00 でサポートPID一覧を取得
	supported, err := r.elm.ScanSupportedPIDs()
	if err != nil {
		return fmt.Errorf("サポートPIDスキャン失敗: %w", err)
	}
	fmt.Printf("✓ サポートPID: %d 個検出\n", len(supported))

	// サポートPIDからMAF/MAPの有無を判定
	hasMAP := false
	hasIAT := false
	for _, pid := range supported {
		switch pid {
		case PIDMAF:
			r.hasMAF = true
		case PIDIntakeManifold:
			hasMAP = true
		case PIDIntakeAirTemp:
			hasIAT = true
		}
	}

	// 燃費計算方式を決定（MAF優先）
	if r.hasMAF {
		fmt.Println("✓ MAFセンサー検出 → MAF方式で燃費計算")
	} else if hasMAP && hasIAT {
		fmt.Println("✗ MAFセンサーなし → Speed-Density方式で燃費計算")
		fmt.Println("✓ MAPセンサー + 吸気温度センサー検出")
	} else {
		return fmt.Errorf("燃費計算に必要なセンサーが不足: MAF=%v, MAP=%v, IAT=%v", r.hasMAF, hasMAP, hasIAT)
	}

	// マルチPIDリクエスト対応テスト
	r.testMultiPID()

	return nil
}

// testMultiPID はマルチPIDリクエストの対応をテストする
func (r *Reader) testMultiPID() {
	testPIDs := []byte{PIDEngineRPM, PIDVehicleSpeed}
	result, err := r.elm.QueryMultiPID(testPIDs)
	if err != nil || len(result) < 2 {
		r.supportsMulti = false
		fmt.Println("✗ マルチPIDリクエスト非対応 → 個別クエリで動作")
	} else {
		r.supportsMulti = true
		fmt.Println("✓ マルチPIDリクエスト対応 → バッチクエリで高速化")
	}
	r.multiTested = true
}

// ReadFast はRPM+速度のみを最速で取得する（メーター追従性優先）
func (r *Reader) ReadFast() (*OBDData, error) {
	data := &OBDData{HasMAF: r.hasMAF, engineCfg: r.EngineCfg}

	if r.supportsMulti {
		result, err := r.elm.QueryMultiPID([]byte{PIDEngineRPM, PIDVehicleSpeed})
		if err != nil {
			// フォールバック: 個別クエリ
			if raw, err := r.elm.QueryPID(PIDEngineRPM); err == nil && len(raw) >= 2 {
				data.RPM = float64(uint16(raw[0])<<8|uint16(raw[1])) / 4.0
			}
			if raw, err := r.elm.QueryPID(PIDVehicleSpeed); err == nil && len(raw) >= 1 {
				data.SpeedKmh = float64(raw[0])
			}
			return data, nil
		}
		if raw, ok := result[PIDEngineRPM]; ok && len(raw) >= 2 {
			data.RPM = float64(uint16(raw[0])<<8|uint16(raw[1])) / 4.0
		}
		if raw, ok := result[PIDVehicleSpeed]; ok && len(raw) >= 1 {
			data.SpeedKmh = float64(raw[0])
		}
	} else {
		if raw, err := r.elm.QueryPID(PIDEngineRPM); err == nil && len(raw) >= 2 {
			data.RPM = float64(uint16(raw[0])<<8|uint16(raw[1])) / 4.0
		}
		if raw, err := r.elm.QueryPID(PIDVehicleSpeed); err == nil && len(raw) >= 1 {
			data.SpeedKmh = float64(raw[0])
		}
	}

	return data, nil
}

// ReadAll は全データを一度に読み取る
// マルチPID対応ならバッチ、非対応なら個別クエリ
func (r *Reader) ReadAll() (*OBDData, error) {
	if r.supportsMulti {
		return r.readAllBatch()
	}
	return r.readAllSingle()
}

// readAllBatch はマルチPIDでバッチ読み取りする
// 7回の通信 → 2回に削減（MAF方式の場合）
func (r *Reader) readAllBatch() (*OBDData, error) {
	data := &OBDData{HasMAF: r.hasMAF, engineCfg: r.EngineCfg}

	// バッチ1: 高頻度データ（毎ポーリング必須）
	// RPM(0C) + 車速(0D) + 負荷(04) + MAF(10) or MAP(0B)+吸気温(0F)
	var batch1 []byte
	if r.hasMAF {
		batch1 = []byte{PIDEngineRPM, PIDVehicleSpeed, PIDEngineLoad, PIDMAF}
	} else {
		batch1 = []byte{PIDEngineRPM, PIDVehicleSpeed, PIDEngineLoad, PIDIntakeManifold, PIDIntakeAirTemp}
	}

	result1, err := r.elm.QueryMultiPID(batch1)
	if err != nil {
		// マルチPID失敗 → 個別に切り替え
		fmt.Println("⚠ マルチPID失敗、個別クエリにフォールバック")
		r.supportsMulti = false
		return r.readAllSingle()
	}

	// バッチ1の結果をパース
	if raw, ok := result1[PIDEngineRPM]; ok && len(raw) >= 2 {
		data.RPM = float64(uint16(raw[0])<<8|uint16(raw[1])) / 4.0
	}
	if raw, ok := result1[PIDVehicleSpeed]; ok && len(raw) >= 1 {
		data.SpeedKmh = float64(raw[0])
	}
	if raw, ok := result1[PIDEngineLoad]; ok && len(raw) >= 1 {
		data.EngineLoad = float64(raw[0]) * 100.0 / 255.0
	}
	if r.hasMAF {
		if raw, ok := result1[PIDMAF]; ok && len(raw) >= 2 {
			data.MAF = float64(uint16(raw[0])<<8|uint16(raw[1])) / 100.0
		}
	} else {
		if raw, ok := result1[PIDIntakeManifold]; ok && len(raw) >= 1 {
			data.IntakeManifold = float64(raw[0])
		}
		if raw, ok := result1[PIDIntakeAirTemp]; ok && len(raw) >= 1 {
			data.IntakeAirTemp = float64(raw[0]) - 40.0
		}
	}

	// バッチ2: 低頻度データ（冷却水温 + 燃料タンク）
	batch2 := []byte{PIDCoolantTemp, PIDFuelTankLevel}
	result2, err := r.elm.QueryMultiPID(batch2)
	if err == nil {
		if raw, ok := result2[PIDCoolantTemp]; ok && len(raw) >= 1 {
			data.CoolantTemp = float64(raw[0]) - 40.0
		}
		if raw, ok := result2[PIDFuelTankLevel]; ok && len(raw) >= 1 {
			data.FuelTankLevel = float64(raw[0]) * 100.0 / 255.0
		}
	} else {
		// バッチ2失敗は無視（水温と燃料は必須ではない）
		// 個別に取ってみる
		if raw, err := r.elm.QueryPID(PIDCoolantTemp); err == nil && len(raw) >= 1 {
			data.CoolantTemp = float64(raw[0]) - 40.0
		}
		if raw, err := r.elm.QueryPID(PIDFuelTankLevel); err == nil && len(raw) >= 1 {
			data.FuelTankLevel = float64(raw[0]) * 100.0 / 255.0
		}
	}

	return data, nil
}

// readAllSingle は従来の個別PIDクエリで読み取る（フォールバック）
func (r *Reader) readAllSingle() (*OBDData, error) {
	data := &OBDData{HasMAF: r.hasMAF, engineCfg: r.EngineCfg}

	// 車速（必須）
	if raw, err := r.elm.QueryPID(PIDVehicleSpeed); err == nil && len(raw) >= 1 {
		data.SpeedKmh = float64(raw[0])
	}

	// RPM（必須）
	if raw, err := r.elm.QueryPID(PIDEngineRPM); err == nil && len(raw) >= 2 {
		data.RPM = float64(uint16(raw[0])<<8|uint16(raw[1])) / 4.0
	}

	// エンジン負荷
	if raw, err := r.elm.QueryPID(PIDEngineLoad); err == nil && len(raw) >= 1 {
		data.EngineLoad = float64(raw[0]) * 100.0 / 255.0
	}

	if r.hasMAF {
		// MAF方式
		if raw, err := r.elm.QueryPID(PIDMAF); err == nil && len(raw) >= 2 {
			data.MAF = float64(uint16(raw[0])<<8|uint16(raw[1])) / 100.0
		}
	} else {
		// Speed-Density方式用のデータ
		if raw, err := r.elm.QueryPID(PIDIntakeManifold); err == nil && len(raw) >= 1 {
			data.IntakeManifold = float64(raw[0])
		}
		if raw, err := r.elm.QueryPID(PIDIntakeAirTemp); err == nil && len(raw) >= 1 {
			data.IntakeAirTemp = float64(raw[0]) - 40.0
		}
	}

	// 燃料タンクレベル（あれば）
	if raw, err := r.elm.QueryPID(PIDFuelTankLevel); err == nil && len(raw) >= 1 {
		data.FuelTankLevel = float64(raw[0]) * 100.0 / 255.0
	}

	// 冷却水温度
	if raw, err := r.elm.QueryPID(PIDCoolantTemp); err == nil && len(raw) >= 1 {
		data.CoolantTemp = float64(raw[0]) - 40.0
	}

	return data, nil
}

// CalcFuelRateLph は燃料消費量 (L/h) を計算する
func (d *OBDData) CalcFuelRateLph() float64 {
	if d.HasMAF && d.MAF > 0 {
		// MAF方式: 燃料消費量 = MAF / (空燃比 × ガソリン密度)
		// MAF (g/s) → 燃料 (g/s) → (L/h)
		fuelGramsPerSec := d.MAF / StoichAFR
		fuelLitersPerHour := (fuelGramsPerSec / (GasolineDensity * 1000)) * 3600
		return fuelLitersPerHour
	}

	// Speed-Density方式（MAP + 吸気温度 + RPM から推定）
	// 理想気体の状態方程式でMAFを推定
	if d.RPM <= 0 || d.IntakeManifold <= 0 {
		return 0
	}

	imap := d.RPM * d.IntakeManifold / (d.IntakeAirTemp + 273.15)
	// MAF推定 (g/s)
	estimatedMAF := imap / 120.0 * d.engineCfg.VolumetricEfficiency * d.engineCfg.DisplacementL * 28.97 / 8.314
	fuelGramsPerSec := estimatedMAF / StoichAFR
	fuelLitersPerHour := (fuelGramsPerSec / (GasolineDensity * 1000)) * 3600

	return math.Max(0, fuelLitersPerHour)
}

// CalcInstantFuelEconomy は瞬間燃費 (km/L) を計算する
func (d *OBDData) CalcInstantFuelEconomy() float64 {
	fuelRateLph := d.CalcFuelRateLph()
	if fuelRateLph <= 0 || d.SpeedKmh <= 0 {
		return 0 // 停車中 or データなし
	}

	// km/L = 速度(km/h) / 燃料消費(L/h)
	return d.SpeedKmh / fuelRateLph
}

// CalcEstimatedPowerKW は推定出力 (kW) を計算する
// 燃料の化学エネルギー × 熱効率 = 軸出力
func (d *OBDData) CalcEstimatedPowerKW() float64 {
	fuelRateLph := d.CalcFuelRateLph()
	if fuelRateLph <= 0 {
		return 0
	}
	// L/h → g/s
	fuelGramsPerSec := fuelRateLph * GasolineDensity * 1000 / 3600
	// 化学エネルギー (W) × 熱効率 → 軸出力 (kW)
	powerW := fuelGramsPerSec * GasolineHeatValue * d.engineCfg.ThermalEfficiency
	return powerW / 1000.0
}

// CalcEstimatedPowerPS は推定出力 (PS/馬力) を計算する
func (d *OBDData) CalcEstimatedPowerPS() float64 {
	return d.CalcEstimatedPowerKW() * 1.3596
}

// CalcEstimatedTorqueNm は推定トルク (Nm) を計算する
// P(W) = T(Nm) × ω(rad/s) → T = P / (2π × RPM / 60)
func (d *OBDData) CalcEstimatedTorqueNm() float64 {
	powerKW := d.CalcEstimatedPowerKW()
	if d.RPM <= 0 || powerKW <= 0 {
		return 0
	}
	omega := 2 * math.Pi * d.RPM / 60.0
	return (powerKW * 1000) / omega
}
