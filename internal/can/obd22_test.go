package can

import "testing"

func TestOBDRequestFrame22(t *testing.T) {
	f := OBDRequestFrame22(PID22ATFTemp)
	if f.ID != IDOBDRequestECU {
		t.Errorf("ID = 0x%X, want 0x7E0 (7DF のブロードキャストでは応答しない)", f.ID)
	}
	want := [8]byte{0x03, 0x22, 0x17, 0xB3, 0, 0, 0, 0}
	if f.Data != want {
		t.Errorf("Data = % X, want % X", f.Data, want)
	}
}

func TestParseOBDResponse22(t *testing.T) {
	// 実機の応答: 05 62 17 B3 7A 00 → 0x7A = 122 → 82℃
	f := Frame{ID: 0x7E8, DLC: 8, Data: [8]byte{0x05, 0x62, 0x17, 0xB3, 0x7A, 0x00, 0, 0}}
	pid, data, ok := ParseOBDResponse22(f)
	if !ok {
		t.Fatal("解析に失敗")
	}
	if pid != PID22ATFTemp {
		t.Errorf("PID = 0x%04X, want 0x17B3", pid)
	}
	tempC, ok := DecodeATFTemp(data)
	if !ok || tempC < 64.3 || tempC > 64.5 {
		t.Errorf("油温 = %.2f℃, want 64.4 付近", tempC)
	}
}

func TestParseOBDResponse22_Rejects(t *testing.T) {
	tests := []struct {
		name string
		f    Frame
	}{
		{"別のID", Frame{ID: 0x7E9, DLC: 8, Data: [8]byte{0x05, 0x62, 0x17, 0xB3, 0x7A}}},
		{"Mode 01 の応答", Frame{ID: 0x7E8, DLC: 8, Data: [8]byte{0x03, 0x41, 0x05, 0x82}}},
		{"否定応答 (requestOutOfRange)", Frame{ID: 0x7E8, DLC: 8, Data: [8]byte{0x03, 0x7F, 0x22, 0x31}}},
		{"短すぎる", Frame{ID: 0x7E8, DLC: 3, Data: [8]byte{0x05, 0x62, 0x17}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, ok := ParseOBDResponse22(tt.f); ok {
				t.Error("受け入れてはいけない応答を受け入れた")
			}
		})
	}
}

// 実測値で換算を固定する。
func TestDecodeATFTemp_Measured(t *testing.T) {
	// ScanGauge の公式定義 °F = raw × 42/25 − 57 を℃に直した値で固定する。
	// 当初は他の温度PIDからの類推で raw − 40 としていたが誤りだった。
	tests := []struct {
		raw   byte
		wantC float64
		note  string
	}{
		{106, 49.5, "8/27 21:07 停車暖機の開始"},
		{108, 51.4, "同 15分後。水温は23℃上がったのに2℃しか上がらない"},
		{122, 64.4, "8/28 00:00 走行前の停車。マツダの油量点検温度65℃とほぼ一致"},
		{160, 99.9, "首都高での最高。水温より +1.3℃ で実車報告と一致"},
		{53, 0.0, "氷点付近"},
	}
	for _, tt := range tests {
		got, ok := DecodeATFTemp([]byte{tt.raw})
		if !ok || got < tt.wantC-0.6 || got > tt.wantC+0.6 {
			t.Errorf("%s: raw=%d → %.2f℃, want %.1f", tt.note, tt.raw, got, tt.wantC)
		}
	}
	if _, ok := DecodeATFTemp(nil); ok {
		t.Error("空データを受け入れた")
	}
}
