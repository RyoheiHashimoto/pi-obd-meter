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
		wantAlert string
	}{
		{"未取得", &obd.OBDData{HasATF: false, ATFTempC: 0}, 0, ""},
		{"未取得だが値が残っている", &obd.OBDData{HasATF: false, ATFTempC: 135}, 0, ""},
		{"正常域", &obd.OBDData{HasATF: true, ATFTempC: 90}, 90, ""},
		{"氷点", &obd.OBDData{HasATF: true, ATFTempC: 0}, 0, ""},
		{"注意域", &obd.OBDData{HasATF: true, ATFTempC: 105}, 105, "ATF注意"},
		{"高温", &obd.OBDData{HasATF: true, ATFTempC: 122}, 122, "ATF高温"},
		{"危険", &obd.OBDData{HasATF: true, ATFTempC: 131}, 131, "ATF危険"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := atfTempOrZero(tt.data); got != tt.wantTemp {
				t.Errorf("温度 = %.1f, want %.1f", got, tt.wantTemp)
			}
			if got := atfAlertOrEmpty(tt.data); got != tt.wantAlert {
				t.Errorf("警告 = %q, want %q", got, tt.wantAlert)
			}
		})
	}
}

// 未取得なのに危険域の警告を出してはいけない。
func TestATFAlert_NotRaisedWhenUnavailable(t *testing.T) {
	d := &obd.OBDData{HasATF: false, ATFTempC: 200}
	if a := atfAlertOrEmpty(d); a != "" {
		t.Errorf("未取得なのに警告 %q を出した", a)
	}
}
