package obd

import "fmt"

// PID定義
const (
	PIDEngineRPM        byte = 0x0C // エンジン回転数
	PIDVehicleSpeed     byte = 0x0D // 車速 (km/h)
	PIDEngineLoad       byte = 0x04 // エンジン負荷 (%)
	PIDCoolantTemp      byte = 0x05 // 冷却水温度
	PIDIntakeMAP        byte = 0x0B // インマニ圧 (kPa)
	PIDMAFAirFlow       byte = 0x10 // MAFエアフローレート (g/s)
	PIDThrottlePosition byte = 0x11 // スロットル開度 (%)
	PIDFuelLevel        byte = 0x2F // 燃料レベル (%)
	PIDAmbientTemp      byte = 0x46 // 外気温 (°C)
)

// Device はOBD-2アダプタの通信インタフェース。
// テスト時にモック実装に差し替えることでハードウェアなしでのテストを可能にする。
type Device interface {
	QueryPID(pid byte) ([]byte, error)
	QueryMultiPID(pids []byte) (map[byte][]byte, error)
	ScanSupportedPIDs() ([]byte, error)
}

// OBDData はOBD-2から読み取ったリアルタイムデータ
type OBDData struct {
	RPM         float64 // rpm
	SpeedKmh    float64 // km/h
	EngineLoad  float64 // 0-100%
	CoolantTemp float64 // ℃
	IntakeMAP   float64 // kPa (0=非対応)
	MAFAirFlow  float64 // g/s (0=非対応)
	ThrottlePos float64 // 0-100%
	Voltage     float64 // バッテリー電圧 (V) — CAN経由
	FuelLevel   float64 // 燃料レベル (%) — OBD PID 0x2F
	AmbientTemp float64 // 外気温 (°C) — OBD PID 0x46
	HasMAF      bool    // MAFセンサー対応か
}

// Reader はOBD-2データを読み取る
type Reader struct {
	dev           Device
	supportsMulti bool // マルチPIDリクエスト対応フラグ
	multiTested   bool // マルチPID対応テスト済みフラグ
	hasMAF        bool // MAF (PID 0x10) 対応
	hasMAP        bool // MAP (PID 0x0B) 対応
}

// NewReader は新しいReaderを作成する
func NewReader(dev Device) *Reader {
	return &Reader{dev: dev}
}

// DetectCapabilities はマルチPIDリクエストの対応をテストする
func (r *Reader) DetectCapabilities() error {
	supported, err := r.dev.ScanSupportedPIDs()
	if err != nil {
		return fmt.Errorf("サポートPIDスキャン失敗: %w", err)
	}
	fmt.Printf("✓ サポートPID: %d 個検出\n", len(supported))

	// オプショナルPIDの対応チェック
	pidSet := make(map[byte]bool)
	for _, p := range supported {
		pidSet[p] = true
	}
	r.hasMAF = pidSet[PIDMAFAirFlow]
	if r.hasMAF {
		fmt.Println("✓ MAFエアフロー対応 → 燃費計算に使用")
	} else {
		fmt.Println("✗ MAF非対応 → 負荷×RPMで燃費推定")
	}
	r.hasMAP = pidSet[PIDIntakeMAP]
	if r.hasMAP {
		fmt.Println("✓ MAPセンサー対応 → インマニ圧取得")
	}

	r.testMultiPID()
	return nil
}

// HasMAF はMAFセンサー対応かどうかを返す
func (r *Reader) HasMAF() bool { return r.hasMAF }

// HasMAP はMAPセンサー対応かどうかを返す
func (r *Reader) HasMAP() bool { return r.hasMAP }

// testMultiPID はマルチPIDリクエストの対応をテストする
func (r *Reader) testMultiPID() {
	testPIDs := []byte{PIDEngineRPM, PIDVehicleSpeed}
	result, err := r.dev.QueryMultiPID(testPIDs)
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
	pids := []byte{PIDVehicleSpeed, PIDThrottlePosition}

	if r.supportsMulti {
		result, err := r.dev.QueryMultiPID(pids)
		if err != nil {
			// フォールバック: 個別クエリ
			for _, pid := range pids {
				raw, err := r.dev.QueryPID(pid)
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
			if raw, err := r.dev.QueryPID(pid); err == nil {
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
	case PIDIntakeMAP:
		if len(raw) >= 1 {
			data.IntakeMAP = float64(raw[0]) // kPa (0-255)
		}
	case PIDMAFAirFlow:
		if len(raw) >= 2 {
			data.MAFAirFlow = float64(uint16(raw[0])<<8|uint16(raw[1])) / 100.0
			data.HasMAF = true
		}
	case PIDFuelLevel:
		if len(raw) >= 1 {
			data.FuelLevel = float64(raw[0]) * 100.0 / 255.0
		}
	case PIDAmbientTemp:
		if len(raw) >= 1 {
			data.AmbientTemp = float64(raw[0]) - 40.0
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
	result1, err := r.dev.QueryMultiPID(batch1)
	if err != nil {
		fmt.Println("⚠ マルチPID失敗、個別クエリにフォールバック")
		r.supportsMulti = false
		return r.readAllSingle()
	}
	for pid, raw := range result1 {
		parsePID(data, pid, raw)
	}

	// 冷却水温
	if raw, err := r.dev.QueryPID(PIDCoolantTemp); err == nil {
		parsePID(data, PIDCoolantTemp, raw)
	}

	// インマニ圧（MAP）
	if r.hasMAP {
		if raw, err := r.dev.QueryPID(PIDIntakeMAP); err == nil {
			parsePID(data, PIDIntakeMAP, raw)
		}
	}

	// MAFエアフロー（燃費計算用）
	if r.hasMAF {
		if raw, err := r.dev.QueryPID(PIDMAFAirFlow); err == nil {
			parsePID(data, PIDMAFAirFlow, raw)
		}
	}

	return data, nil
}

// readAllSingle は従来の個別PIDクエリで読み取る（フォールバック）
func (r *Reader) readAllSingle() (*OBDData, error) {
	data := &OBDData{}
	pids := []byte{PIDEngineRPM, PIDVehicleSpeed, PIDEngineLoad, PIDThrottlePosition, PIDCoolantTemp}
	if r.hasMAP {
		pids = append(pids, PIDIntakeMAP)
	}
	if r.hasMAF {
		pids = append(pids, PIDMAFAirFlow)
	}

	for _, pid := range pids {
		if raw, err := r.dev.QueryPID(pid); err == nil {
			parsePID(data, pid, raw)
		}
	}

	return data, nil
}
