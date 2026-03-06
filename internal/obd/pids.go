package obd

import "fmt"

// PID定義
const (
	PIDEngineRPM         byte = 0x0C // エンジン回転数
	PIDVehicleSpeed      byte = 0x0D // 車速 (km/h)
	PIDEngineLoad        byte = 0x04 // エンジン負荷 (%)
	PIDCoolantTemp       byte = 0x05 // 冷却水温度
	PIDThrottlePosition  byte = 0x11 // スロットル開度 (%)
	PIDFuelTankLevel     byte = 0x2F // 燃料タンクレベル (%)
	PIDRunTimeSinceStart byte = 0x1F // エンジン始動後の経過時間
)

// OBDData はOBD-2から読み取ったリアルタイムデータ
type OBDData struct {
	RPM           float64 // rpm
	SpeedKmh      float64 // km/h
	EngineLoad    float64 // 0-100%
	CoolantTemp   float64 // ℃
	ThrottlePos   float64 // 0-100%
	FuelTankLevel float64 // 0-100%
}

// Reader はOBD-2データを読み取る
type Reader struct {
	elm           *ELM327
	supportsMulti bool // マルチPIDリクエスト対応フラグ
	multiTested   bool // マルチPID対応テスト済みフラグ
}

// NewReader は新しいReaderを作成する
func NewReader(elm *ELM327) *Reader {
	return &Reader{elm: elm}
}

// DetectCapabilities はマルチPIDリクエストの対応をテストする
func (r *Reader) DetectCapabilities() error {
	supported, err := r.elm.ScanSupportedPIDs()
	if err != nil {
		return fmt.Errorf("サポートPIDスキャン失敗: %w", err)
	}
	fmt.Printf("✓ サポートPID: %d 個検出\n", len(supported))

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

// ReadFast はRPM+速度+負荷+スロットルを1回の通信で取得する（メーター追従性優先）
// 4 PIDでもCAN 1フレームに収まるため、2 PIDと通信回数は同じ
func (r *Reader) ReadFast() (*OBDData, error) {
	data := &OBDData{}
	pids := []byte{PIDEngineRPM, PIDVehicleSpeed, PIDEngineLoad, PIDThrottlePosition}

	if r.supportsMulti {
		result, err := r.elm.QueryMultiPID(pids)
		if err != nil {
			// フォールバック: 個別クエリ
			for _, pid := range pids {
				raw, err := r.elm.QueryPID(pid)
				if err != nil {
					continue
				}
				parsePID(data, pid, raw)
			}
			return data, nil
		}
		for pid, raw := range result {
			parsePID(data, pid, raw)
		}
	} else {
		for _, pid := range pids {
			if raw, err := r.elm.QueryPID(pid); err == nil {
				parsePID(data, pid, raw)
			}
		}
	}

	return data, nil
}

// parsePID は1つのPIDレスポンスをOBDDataにセットする
func parsePID(data *OBDData, pid byte, raw []byte) {
	switch pid {
	case PIDEngineRPM:
		if len(raw) >= 2 {
			data.RPM = float64(uint16(raw[0])<<8|uint16(raw[1])) / 4.0
		}
	case PIDVehicleSpeed:
		if len(raw) >= 1 {
			data.SpeedKmh = float64(raw[0])
		}
	case PIDEngineLoad:
		if len(raw) >= 1 {
			data.EngineLoad = float64(raw[0]) * 100.0 / 255.0
		}
	case PIDThrottlePosition:
		if len(raw) >= 1 {
			data.ThrottlePos = float64(raw[0]) * 100.0 / 255.0
		}
	case PIDCoolantTemp:
		if len(raw) >= 1 {
			data.CoolantTemp = float64(raw[0]) - 40.0
		}
	case PIDFuelTankLevel:
		if len(raw) >= 1 {
			data.FuelTankLevel = float64(raw[0]) * 100.0 / 255.0
		}
	}
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
func (r *Reader) readAllBatch() (*OBDData, error) {
	data := &OBDData{}

	// バッチ1: RPM + 車速 + 負荷 + スロットル
	batch1 := []byte{PIDEngineRPM, PIDVehicleSpeed, PIDEngineLoad, PIDThrottlePosition}
	result1, err := r.elm.QueryMultiPID(batch1)
	if err != nil {
		fmt.Println("⚠ マルチPID失敗、個別クエリにフォールバック")
		r.supportsMulti = false
		return r.readAllSingle()
	}
	for pid, raw := range result1 {
		parsePID(data, pid, raw)
	}

	// 燃料タンク（個別クエリ、給油検出用）
	if raw, err := r.elm.QueryPID(PIDFuelTankLevel); err == nil {
		parsePID(data, PIDFuelTankLevel, raw)
	}

	return data, nil
}

// readAllSingle は従来の個別PIDクエリで読み取る（フォールバック）
func (r *Reader) readAllSingle() (*OBDData, error) {
	data := &OBDData{}
	pids := []byte{PIDEngineRPM, PIDVehicleSpeed, PIDEngineLoad, PIDThrottlePosition, PIDFuelTankLevel}

	for _, pid := range pids {
		if raw, err := r.elm.QueryPID(pid); err == nil {
			parsePID(data, pid, raw)
		}
	}

	return data, nil
}

// ReadFuelTankLevel は燃料タンクレベルのみを取得する（給油検出用）
func (r *Reader) ReadFuelTankLevel() (float64, error) {
	raw, err := r.elm.QueryPID(PIDFuelTankLevel)
	if err != nil {
		return 0, err
	}
	if len(raw) < 1 {
		return 0, fmt.Errorf("fuel tank PID: empty response")
	}
	return float64(raw[0]) * 100.0 / 255.0, nil
}
