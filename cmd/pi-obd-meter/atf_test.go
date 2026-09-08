package main

import (
	"testing"

	"github.com/hashimoto/pi-obd-meter/internal/obd"
)

// ATF が取れていないことと、0℃ であることを取り違えないこと。
//
// 換算後の 0℃ は raw 53 に対応する実在しうる値なので、未取得のまま 0 を
// 流すと冬場に本物の低温と紛れる。
func TestATFTempOrZero(t *testing.T) {
	tests := []struct {
		name      string
		data      *obd.OBDData
		wantTemp  float64
		wantLevel string
	}{
		{"未取得", &obd.OBDData{HasATF: false, ATFTempC: 0}, 0, ""},
		{"未取得だが値が残っている", &obd.OBDData{HasATF: false, ATFTempC: 135}, 0, ""},
		{"正常域", &obd.OBDData{HasATF: true, ATFTempC: 85}, 85, ""},
		{"高速域", &obd.OBDData{HasATF: true, ATFTempC: 95}, 95, "warm"},
		{"氷点", &obd.OBDData{HasATF: true, ATFTempC: 0}, 0, ""},
		{"注意域", &obd.OBDData{HasATF: true, ATFTempC: 105}, 105, "caution"},
		{"高温", &obd.OBDData{HasATF: true, ATFTempC: 115}, 115, "hot"},
		{"危険", &obd.OBDData{HasATF: true, ATFTempC: 125}, 125, "danger"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := atfTempOrZero(tt.data); got != tt.wantTemp {
				t.Errorf("温度 = %.1f, want %.1f", got, tt.wantTemp)
			}
			if got := atfLevelOrEmpty(tt.data); got != tt.wantLevel {
				t.Errorf("区分 = %q, want %q", got, tt.wantLevel)
			}
		})
	}
}

// 未取得なのに危険域の警告を出してはいけない。
func TestATFLevel_NotRaisedWhenUnavailable(t *testing.T) {
	d := &obd.OBDData{HasATF: false, ATFTempC: 200}
	if a := atfLevelOrEmpty(d); a != "" {
		t.Errorf("未取得なのに区分 %q を出した", a)
	}
}
