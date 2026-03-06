package obd

import (
	"reflect"
	"testing"
)

// --- decodeDTC ---

func TestDecodeDTC(t *testing.T) {
	tests := []struct {
		name string
		hex4 string
		want string
	}{
		{"powertrain P0120", "0120", "P0120"},
		{"chassis C0120", "4120", "C0120"},
		{"body B0120", "8120", "B0120"},
		{"network U0120", "C120", "U0120"},
		{"zero value", "0000", ""},
		{"empty string", "", ""},
		{"too short", "01", ""},
		{"too long", "01200", ""},
		{"invalid hex", "ZZZZ", ""},
		{"P0300 misfire", "0300", "P0300"},
		{"P0420 catalyst", "0420", "P0420"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeDTC(tt.hex4)
			if got != tt.want {
				t.Errorf("decodeDTC(%q) = %q, want %q", tt.hex4, got, tt.want)
			}
		})
	}
}

// --- parseDTCResponse ---

func TestParseDTCResponse(t *testing.T) {
	tests := []struct {
		name string
		resp string
		want []string
	}{
		{
			name: "two codes with padding",
			resp: "43 01 20 03 40 00 00",
			want: []string{"P0120", "P0340"},
		},
		{
			name: "all padding (no codes)",
			resp: "43 00 00 00 00 00 00",
			want: nil,
		},
		{
			name: "empty response",
			resp: "",
			want: nil,
		},
		{
			name: "single code",
			resp: "43 04 20 00 00 00 00",
			want: []string{"P0420"},
		},
		{
			name: "multi-frame response",
			resp: "43 01 20 03 40 01 71 43 04 20 00 00 00 00",
			want: []string{"P0120", "P0340", "P0171", "P0420"},
		},
		{
			name: "response with CR/LF",
			resp: "43 01 20 00 00 00 00\r\n",
			want: []string{"P0120"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDTCResponse(tt.resp)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseDTCResponse(%q) = %v, want %v", tt.resp, got, tt.want)
			}
		})
	}
}

// --- dtcDescription ---

func TestDTCDescription(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{"known code P0420", "P0420", "触媒効率低下 (Bank1)"},
		{"known code P0300", "P0300", "ランダムミスファイア検出"},
		{"unknown P code", "P9999", "パワートレイン系故障コード（詳細不明）"},
		{"unknown C code", "C9999", "シャシー系故障コード（詳細不明）"},
		{"unknown B code", "B9999", "ボディ系故障コード（詳細不明）"},
		{"unknown U code", "U9999", "通信系故障コード（詳細不明）"},
		{"empty code", "", "不明な故障コード"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dtcDescription(tt.code)
			if got != tt.want {
				t.Errorf("dtcDescription(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

// --- dtcSeverity ---

func TestDTCSeverity(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{"critical - misfire", "P0300", "critical"},
		{"critical - crank sensor", "P0335", "critical"},
		{"info - catalyst", "P0420", "info"},
		{"info - thermostat", "P0128", "info"},
		{"default warning", "P0171", "warning"},
		{"unknown code", "P9999", "warning"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dtcSeverity(tt.code)
			if got != tt.want {
				t.Errorf("dtcSeverity(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}
