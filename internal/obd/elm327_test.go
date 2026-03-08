package obd

import (
	"reflect"
	"testing"
)

// --- parseHexResponse ---

func TestParseHexResponse_Normal(t *testing.T) {
	// "410D3C" → PID 0x0D(速度), data=[0x3C]=60
	got, err := parseHexResponse("410D3C")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0x3C}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseHexResponse_TwoBytes(t *testing.T) {
	// "410C1A20" → PID 0x0C(RPM), data=[0x1A,0x20]
	got, err := parseHexResponse("410C1A20")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0x1A, 0x20}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseHexResponse_WithSpaces(t *testing.T) {
	got, err := parseHexResponse("41 0D 3C")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0x3C}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseHexResponse_WithCRLF(t *testing.T) {
	got, err := parseHexResponse("410D3C\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0x3C}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseHexResponse_TooShort(t *testing.T) {
	_, err := parseHexResponse("41")
	if err == nil {
		t.Error("expected error for short response")
	}
}

func TestParseHexResponse_Empty(t *testing.T) {
	_, err := parseHexResponse("")
	if err == nil {
		t.Error("expected error for empty response")
	}
}

func TestParseHexResponse_OddHex(t *testing.T) {
	// 先頭4文字スキップ後のデータが奇数文字
	_, err := parseHexResponse("410D3")
	if err == nil {
		t.Error("expected error for odd hex data")
	}
}

func TestParseHexResponse_InvalidHex(t *testing.T) {
	_, err := parseHexResponse("410DZZ")
	if err == nil {
		t.Error("expected error for invalid hex")
	}
}

func TestParseHexResponse_HeaderOnly(t *testing.T) {
	// データなし（ヘッダのみ）
	got, err := parseHexResponse("410D")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty data, got %v", got)
	}
}

// --- pidDataLength ---

func TestPidDataLength(t *testing.T) {
	tests := []struct {
		name string
		pid  byte
		want int
	}{
		{"RPM (2 bytes)", 0x0C, 2},
		{"MAF (2 bytes)", 0x10, 2},
		{"Speed (1 byte)", 0x0D, 1},
		{"Load (1 byte)", 0x04, 1},
		{"Coolant (1 byte)", 0x05, 1},
		{"Throttle (1 byte)", 0x11, 1},
		{"Intake pressure (1 byte)", 0x0B, 1},
		{"Intake temp (1 byte)", 0x0F, 1},
		{"Unknown PID defaults to 2", 0xFF, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pidDataLength(tt.pid)
			if got != tt.want {
				t.Errorf("pidDataLength(0x%02X) = %d, want %d", tt.pid, got, tt.want)
			}
		})
	}
}

// --- parseMultiPIDResponse ---

func TestParseMultiPIDResponse_Basic(t *testing.T) {
	// RPM=0x1A20, Speed=0x3C
	resp := "410C1A20410D3C"
	pids := []byte{0x0C, 0x0D}
	got, err := parseMultiPIDResponse(resp, pids)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(got[0x0C], []byte{0x1A, 0x20}) {
		t.Errorf("RPM data: got %v, want [1A 20]", got[0x0C])
	}
	if !reflect.DeepEqual(got[0x0D], []byte{0x3C}) {
		t.Errorf("Speed data: got %v, want [3C]", got[0x0D])
	}
}

func TestParseMultiPIDResponse_WithSpaces(t *testing.T) {
	resp := "41 0C 1A 20 41 0D 3C"
	pids := []byte{0x0C, 0x0D}
	got, err := parseMultiPIDResponse(resp, pids)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 PIDs, got %d", len(got))
	}
}

func TestParseMultiPIDResponse_FourPIDs(t *testing.T) {
	// RPM=1672(0x1A20), Speed=60(0x3C), Load=50%(0x80), Throttle=25%(0x40)
	resp := "410C1A20410D3C41048041113F"
	pids := []byte{0x0C, 0x0D, 0x04, 0x11}
	got, err := parseMultiPIDResponse(resp, pids)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("expected 4 PIDs, got %d", len(got))
	}
}

func TestParseMultiPIDResponse_WithNewlines(t *testing.T) {
	// ECUが改行区切りで返すパターン
	resp := "410C1A20\r\n410D3C"
	pids := []byte{0x0C, 0x0D}
	got, err := parseMultiPIDResponse(resp, pids)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 PIDs, got %d", len(got))
	}
}

func TestParseMultiPIDResponse_UnrequestedPID(t *testing.T) {
	// レスポンスに含まれるがリクエストしていないPIDは無視
	resp := "410C1A20410D3C41050A"
	pids := []byte{0x0C, 0x0D} // 0x05はリクエストしていない
	got, err := parseMultiPIDResponse(resp, pids)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 PIDs (unrequested filtered), got %d", len(got))
	}
	if _, ok := got[0x05]; ok {
		t.Error("unrequested PID 0x05 should not be in result")
	}
}

func TestParseMultiPIDResponse_Empty(t *testing.T) {
	_, err := parseMultiPIDResponse("", []byte{0x0C})
	if err == nil {
		t.Error("expected error for empty response")
	}
}

func TestParseMultiPIDResponse_NoValidData(t *testing.T) {
	_, err := parseMultiPIDResponse("NODATA", []byte{0x0C})
	if err == nil {
		t.Error("expected error when no PID data found")
	}
}
