package obd

import (
	"fmt"
	"math"
	"testing"
)

// mockDevice は Device インターフェースのモック実装
type mockDevice struct {
	// QueryPID のレスポンスを PID → データ でマッピング
	pidResponses map[byte][]byte
	// QueryMultiPID が呼ばれた回数
	multiCalls int
	// マルチPIDを失敗させるか
	failMulti bool
	// 特定PIDを失敗させる
	failPIDs map[byte]bool
	// ScanSupportedPIDs の戻り値
	supportedPIDs []byte
}

func newMockDevice() *mockDevice {
	return &mockDevice{
		pidResponses: make(map[byte][]byte),
		failPIDs:     make(map[byte]bool),
	}
}

func (m *mockDevice) QueryPID(pid byte) ([]byte, error) {
	if m.failPIDs[pid] {
		return nil, fmt.Errorf("PID 0x%02X 取得失敗", pid)
	}
	if data, ok := m.pidResponses[pid]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("PID 0x%02X 非対応", pid)
}

func (m *mockDevice) QueryMultiPID(pids []byte) (map[byte][]byte, error) {
	m.multiCalls++
	if m.failMulti {
		return nil, fmt.Errorf("マルチPID非対応")
	}
	result := make(map[byte][]byte)
	for _, pid := range pids {
		if m.failPIDs[pid] {
			continue
		}
		if data, ok := m.pidResponses[pid]; ok {
			result[pid] = data
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("レスポンスなし")
	}
	return result, nil
}

func (m *mockDevice) ScanSupportedPIDs() ([]byte, error) {
	if m.supportedPIDs != nil {
		return m.supportedPIDs, nil
	}
	return nil, fmt.Errorf("スキャン失敗")
}

// --- DetectCapabilities ---

func TestDetectCapabilities_WithMAFAndMAP(t *testing.T) {
	dev := newMockDevice()
	dev.supportedPIDs = []byte{
		PIDEngineRPM, PIDVehicleSpeed, PIDEngineLoad,
		PIDThrottlePosition, PIDCoolantTemp,
		PIDMAFAirFlow, PIDIntakeMAP,
	}
	// マルチPIDテスト用のレスポンス
	dev.pidResponses[PIDEngineRPM] = []byte{0x1A, 0x20}
	dev.pidResponses[PIDVehicleSpeed] = []byte{0x3C}

	r := NewReader(dev)
	if err := r.DetectCapabilities(); err != nil {
		t.Fatalf("DetectCapabilities failed: %v", err)
	}
	if !r.HasMAF() {
		t.Error("HasMAF should be true")
	}
	if !r.HasMAP() {
		t.Error("HasMAP should be true")
	}
}

func TestDetectCapabilities_NoMAFNoMAP(t *testing.T) {
	dev := newMockDevice()
	dev.supportedPIDs = []byte{
		PIDEngineRPM, PIDVehicleSpeed, PIDEngineLoad,
		PIDThrottlePosition, PIDCoolantTemp,
	}
	dev.pidResponses[PIDEngineRPM] = []byte{0x1A, 0x20}
	dev.pidResponses[PIDVehicleSpeed] = []byte{0x3C}

	r := NewReader(dev)
	if err := r.DetectCapabilities(); err != nil {
		t.Fatalf("DetectCapabilities failed: %v", err)
	}
	if r.HasMAF() {
		t.Error("HasMAF should be false")
	}
	if r.HasMAP() {
		t.Error("HasMAP should be false")
	}
}

func TestDetectCapabilities_ScanError(t *testing.T) {
	dev := newMockDevice()
	// supportedPIDs = nil → ScanSupportedPIDs がエラーを返す

	r := NewReader(dev)
	err := r.DetectCapabilities()
	if err == nil {
		t.Error("DetectCapabilities should fail when scan fails")
	}
}

func TestDetectCapabilities_MultiPIDSupport(t *testing.T) {
	dev := newMockDevice()
	dev.supportedPIDs = []byte{PIDEngineRPM, PIDVehicleSpeed}
	dev.pidResponses[PIDEngineRPM] = []byte{0x1A, 0x20}
	dev.pidResponses[PIDVehicleSpeed] = []byte{0x3C}

	r := NewReader(dev)
	_ = r.DetectCapabilities()
	if !r.supportsMulti {
		t.Error("supportsMulti should be true when multi PID works")
	}
}

func TestDetectCapabilities_NoMultiPIDSupport(t *testing.T) {
	dev := newMockDevice()
	dev.supportedPIDs = []byte{PIDEngineRPM, PIDVehicleSpeed}
	dev.failMulti = true

	r := NewReader(dev)
	_ = r.DetectCapabilities()
	if r.supportsMulti {
		t.Error("supportsMulti should be false when multi PID fails")
	}
}

// --- ReadFast ---

func TestReadFast_MultiPID(t *testing.T) {
	dev := newMockDevice()
	dev.pidResponses[PIDEngineRPM] = []byte{0x1A, 0x20} // 1672 rpm
	dev.pidResponses[PIDVehicleSpeed] = []byte{60}      // 60 km/h
	dev.pidResponses[PIDEngineLoad] = []byte{128}       // ~50.2%
	dev.pidResponses[PIDThrottlePosition] = []byte{64}  // ~25.1%

	r := &Reader{dev: dev, supportsMulti: true, multiTested: true}
	data, err := r.ReadFast()
	if err != nil {
		t.Fatalf("ReadFast failed: %v", err)
	}

	wantRPM := float64(0x1A20) / 4.0
	if data.RPM != wantRPM {
		t.Errorf("RPM: got %.1f, want %.1f", data.RPM, wantRPM)
	}
	if data.SpeedKmh != 60 {
		t.Errorf("Speed: got %.0f, want 60", data.SpeedKmh)
	}
	if math.Abs(data.EngineLoad-50.2) > 0.1 {
		t.Errorf("Load: got %.1f, want ~50.2", data.EngineLoad)
	}
	if data.ThrottlePos < 25 || data.ThrottlePos > 26 {
		t.Errorf("Throttle: got %.1f, want ~25.1", data.ThrottlePos)
	}
}

func TestReadFast_SinglePID(t *testing.T) {
	dev := newMockDevice()
	dev.pidResponses[PIDEngineRPM] = []byte{0x0C, 0x00} // 768 rpm
	dev.pidResponses[PIDVehicleSpeed] = []byte{30}
	dev.pidResponses[PIDEngineLoad] = []byte{50}
	dev.pidResponses[PIDThrottlePosition] = []byte{32}

	r := &Reader{dev: dev, supportsMulti: false, multiTested: true}
	data, err := r.ReadFast()
	if err != nil {
		t.Fatalf("ReadFast failed: %v", err)
	}

	if data.SpeedKmh != 30 {
		t.Errorf("Speed: got %.0f, want 30", data.SpeedKmh)
	}
}

func TestReadFast_MultiPIDFallback(t *testing.T) {
	dev := newMockDevice()
	dev.failMulti = true
	dev.pidResponses[PIDEngineRPM] = []byte{0x1A, 0x20}
	dev.pidResponses[PIDVehicleSpeed] = []byte{60}
	dev.pidResponses[PIDEngineLoad] = []byte{128}
	dev.pidResponses[PIDThrottlePosition] = []byte{64}

	// supportsMulti=true だがマルチPIDが失敗 → 個別クエリにフォールバック
	r := &Reader{dev: dev, supportsMulti: true, multiTested: true}
	data, err := r.ReadFast()
	if err != nil {
		t.Fatalf("ReadFast fallback failed: %v", err)
	}
	if data.SpeedKmh != 60 {
		t.Errorf("Speed after fallback: got %.0f, want 60", data.SpeedKmh)
	}
}

func TestReadFast_PartialFailure(t *testing.T) {
	dev := newMockDevice()
	dev.pidResponses[PIDEngineRPM] = []byte{0x1A, 0x20}
	dev.pidResponses[PIDVehicleSpeed] = []byte{60}
	// Load と Throttle は失敗
	dev.failPIDs[PIDEngineLoad] = true
	dev.failPIDs[PIDThrottlePosition] = true

	r := &Reader{dev: dev, supportsMulti: false, multiTested: true}
	data, err := r.ReadFast()
	if err != nil {
		t.Fatalf("ReadFast failed: %v", err)
	}
	// 成功したPIDのデータは取得できる
	if data.SpeedKmh != 60 {
		t.Errorf("Speed: got %.0f, want 60", data.SpeedKmh)
	}
	// 失敗したPIDはゼロ値
	if data.EngineLoad != 0 {
		t.Errorf("Load should be 0 on failure, got %.1f", data.EngineLoad)
	}
}

// --- ReadAll ---

func TestReadAll_MultiPID_WithMAFAndMAP(t *testing.T) {
	dev := newMockDevice()
	dev.pidResponses[PIDEngineRPM] = []byte{0x1A, 0x20}
	dev.pidResponses[PIDVehicleSpeed] = []byte{60}
	dev.pidResponses[PIDEngineLoad] = []byte{128}
	dev.pidResponses[PIDThrottlePosition] = []byte{64}
	dev.pidResponses[PIDCoolantTemp] = []byte{130}       // 90℃
	dev.pidResponses[PIDIntakeMAP] = []byte{80}          // 80 kPa
	dev.pidResponses[PIDMAFAirFlow] = []byte{0x01, 0xF4} // 5.0 g/s

	r := &Reader{dev: dev, supportsMulti: true, multiTested: true, hasMAF: true, hasMAP: true}
	data, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	if data.CoolantTemp != 90 {
		t.Errorf("Coolant: got %.0f, want 90", data.CoolantTemp)
	}
	if data.IntakeMAP != 80 {
		t.Errorf("MAP: got %.0f, want 80", data.IntakeMAP)
	}
	if data.MAFAirFlow != 5.0 {
		t.Errorf("MAF: got %.2f, want 5.0", data.MAFAirFlow)
	}
	if data.SpeedKmh != 60 {
		t.Errorf("Speed: got %.0f, want 60", data.SpeedKmh)
	}
}

func TestReadAll_SinglePID_NoMAFNoMAP(t *testing.T) {
	dev := newMockDevice()
	dev.pidResponses[PIDEngineRPM] = []byte{0x1A, 0x20}
	dev.pidResponses[PIDVehicleSpeed] = []byte{60}
	dev.pidResponses[PIDEngineLoad] = []byte{128}
	dev.pidResponses[PIDThrottlePosition] = []byte{64}
	dev.pidResponses[PIDCoolantTemp] = []byte{130}

	r := &Reader{dev: dev, supportsMulti: false, multiTested: true, hasMAF: false, hasMAP: false}
	data, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	if data.CoolantTemp != 90 {
		t.Errorf("Coolant: got %.0f, want 90", data.CoolantTemp)
	}
	if data.MAFAirFlow != 0 {
		t.Errorf("MAF should be 0 when not supported, got %.2f", data.MAFAirFlow)
	}
	if data.IntakeMAP != 0 {
		t.Errorf("MAP should be 0 when not supported, got %.0f", data.IntakeMAP)
	}
}

func TestReadAll_MultiPIDFallbackToSingle(t *testing.T) {
	dev := newMockDevice()
	dev.pidResponses[PIDEngineRPM] = []byte{0x1A, 0x20}
	dev.pidResponses[PIDVehicleSpeed] = []byte{60}
	dev.pidResponses[PIDEngineLoad] = []byte{128}
	dev.pidResponses[PIDThrottlePosition] = []byte{64}
	dev.pidResponses[PIDCoolantTemp] = []byte{130}

	// マルチPID対応だがReadAll中に失敗 → フォールバック
	callCount := 0
	origFailMulti := dev.failMulti
	dev.failMulti = false

	r := &Reader{dev: dev, supportsMulti: true, multiTested: true}

	// QueryMultiPIDをフックして最初の呼び出しで失敗させる
	dev.failMulti = true
	_ = origFailMulti
	_ = callCount

	data, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll fallback failed: %v", err)
	}

	// フォールバック後はsupportsMultiがfalseに
	if r.supportsMulti {
		t.Error("supportsMulti should be false after fallback")
	}
	if data.CoolantTemp != 90 {
		t.Errorf("Coolant after fallback: got %.0f, want 90", data.CoolantTemp)
	}
}

func TestReadAll_MAPOnly(t *testing.T) {
	dev := newMockDevice()
	dev.pidResponses[PIDEngineRPM] = []byte{0x1A, 0x20}
	dev.pidResponses[PIDVehicleSpeed] = []byte{60}
	dev.pidResponses[PIDEngineLoad] = []byte{128}
	dev.pidResponses[PIDThrottlePosition] = []byte{64}
	dev.pidResponses[PIDCoolantTemp] = []byte{130}
	dev.pidResponses[PIDIntakeMAP] = []byte{80}

	r := &Reader{dev: dev, supportsMulti: false, multiTested: true, hasMAP: true, hasMAF: false}
	data, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if data.IntakeMAP != 80 {
		t.Errorf("MAP: got %.0f, want 80", data.IntakeMAP)
	}
	if data.MAFAirFlow != 0 {
		t.Errorf("MAF should be 0 when not supported, got %.2f", data.MAFAirFlow)
	}
}

// --- ReadAll 2バッチ化テスト ---

func TestReadAll_2Batch_VerifyBatchCount(t *testing.T) {
	// マルチPID対応 + MAP + MAF → QueryMultiPIDが2回呼ばれることを検証
	dev := newMockDevice()
	dev.pidResponses[PIDEngineRPM] = []byte{0x1A, 0x20}
	dev.pidResponses[PIDVehicleSpeed] = []byte{60}
	dev.pidResponses[PIDEngineLoad] = []byte{128}
	dev.pidResponses[PIDThrottlePosition] = []byte{64}
	dev.pidResponses[PIDCoolantTemp] = []byte{130}
	dev.pidResponses[PIDIntakeMAP] = []byte{80}
	dev.pidResponses[PIDMAFAirFlow] = []byte{0x01, 0xF4}

	r := &Reader{dev: dev, supportsMulti: true, multiTested: true, hasMAF: true, hasMAP: true}
	data, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	// QueryMultiPIDは2回（バッチ1 + バッチ2）
	if dev.multiCalls != 2 {
		t.Errorf("QueryMultiPID calls: got %d, want 2", dev.multiCalls)
	}

	// 全データが取得できている
	if data.CoolantTemp != 90 {
		t.Errorf("Coolant: got %.0f, want 90", data.CoolantTemp)
	}
	if data.IntakeMAP != 80 {
		t.Errorf("MAP: got %.0f, want 80", data.IntakeMAP)
	}
	if data.MAFAirFlow != 5.0 {
		t.Errorf("MAF: got %.2f, want 5.0", data.MAFAirFlow)
	}
}

func TestReadAll_2Batch_MAPOnly_NoBatchCount(t *testing.T) {
	// MAP対応・MAF非対応 → バッチ2は [水温, MAP] の2PID
	dev := newMockDevice()
	dev.pidResponses[PIDEngineRPM] = []byte{0x1A, 0x20}
	dev.pidResponses[PIDVehicleSpeed] = []byte{60}
	dev.pidResponses[PIDEngineLoad] = []byte{128}
	dev.pidResponses[PIDThrottlePosition] = []byte{64}
	dev.pidResponses[PIDCoolantTemp] = []byte{130}
	dev.pidResponses[PIDIntakeMAP] = []byte{80}

	r := &Reader{dev: dev, supportsMulti: true, multiTested: true, hasMAP: true, hasMAF: false}
	data, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	if dev.multiCalls != 2 {
		t.Errorf("QueryMultiPID calls: got %d, want 2", dev.multiCalls)
	}
	if data.CoolantTemp != 90 {
		t.Errorf("Coolant: got %.0f, want 90", data.CoolantTemp)
	}
	if data.IntakeMAP != 80 {
		t.Errorf("MAP: got %.0f, want 80", data.IntakeMAP)
	}
	if data.MAFAirFlow != 0 {
		t.Errorf("MAF should be 0: got %.2f", data.MAFAirFlow)
	}
}

func TestReadAll_2Batch_CoolantOnly(t *testing.T) {
	// MAP・MAF両方非対応 → バッチ2は [水温] のみ
	dev := newMockDevice()
	dev.pidResponses[PIDEngineRPM] = []byte{0x1A, 0x20}
	dev.pidResponses[PIDVehicleSpeed] = []byte{60}
	dev.pidResponses[PIDEngineLoad] = []byte{128}
	dev.pidResponses[PIDThrottlePosition] = []byte{64}
	dev.pidResponses[PIDCoolantTemp] = []byte{130}

	r := &Reader{dev: dev, supportsMulti: true, multiTested: true, hasMAP: false, hasMAF: false}
	data, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	if dev.multiCalls != 2 {
		t.Errorf("QueryMultiPID calls: got %d, want 2", dev.multiCalls)
	}
	if data.CoolantTemp != 90 {
		t.Errorf("Coolant: got %.0f, want 90", data.CoolantTemp)
	}
}

func TestReadAll_2Batch_Batch2Fallback(t *testing.T) {
	// バッチ2が失敗 → 個別クエリにフォールバック
	dev := newMockDevice()
	dev.pidResponses[PIDEngineRPM] = []byte{0x1A, 0x20}
	dev.pidResponses[PIDVehicleSpeed] = []byte{60}
	dev.pidResponses[PIDEngineLoad] = []byte{128}
	dev.pidResponses[PIDThrottlePosition] = []byte{64}
	dev.pidResponses[PIDCoolantTemp] = []byte{130}
	dev.pidResponses[PIDIntakeMAP] = []byte{80}

	// バッチ2だけ失敗させるため、カウントベースのフック
	batch2Fails := &batch2FailDevice{mockDevice: dev, failOnCall: 2}

	r := &Reader{dev: batch2Fails, supportsMulti: true, multiTested: true, hasMAP: true, hasMAF: false}
	data, err := r.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	// バッチ1は成功、バッチ2は失敗してフォールバック
	if data.SpeedKmh != 60 {
		t.Errorf("Speed: got %.0f, want 60", data.SpeedKmh)
	}
	if data.CoolantTemp != 90 {
		t.Errorf("Coolant (fallback): got %.0f, want 90", data.CoolantTemp)
	}
	if data.IntakeMAP != 80 {
		t.Errorf("MAP (fallback): got %.0f, want 80", data.IntakeMAP)
	}

	// supportsMulti はまだ true（バッチ1は成功しているため）
	if !r.supportsMulti {
		t.Error("supportsMulti should remain true (batch1 succeeded)")
	}
}

// batch2FailDevice は N 回目の QueryMultiPID を失敗させるラッパー
type batch2FailDevice struct {
	*mockDevice
	failOnCall int
	callCount  int
}

func (d *batch2FailDevice) QueryMultiPID(pids []byte) (map[byte][]byte, error) {
	d.callCount++
	if d.callCount == d.failOnCall {
		return nil, fmt.Errorf("バッチ2失敗")
	}
	return d.mockDevice.QueryMultiPID(pids)
}
