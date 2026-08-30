package health

import (
	"os"
	"path/filepath"
	"testing"
)

// 不正終了の計数。running が立ったまま起動したら前回は不正終了。
func TestMonitor_CountsUncleanShutdown(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "health.json")

	// 1回目の起動。前歴が無いので不正終了は0
	m := NewMonitor(p)
	if got := m.Status().UncleanShutdowns; got != 0 {
		t.Errorf("初回起動の不正終了 = %d, want 0", got)
	}
	if got := m.Status().BootCount; got != 1 {
		t.Errorf("起動回数 = %d, want 1", got)
	}

	// 正常終了して2回目の起動。不正終了は増えない
	m.MarkCleanShutdown()
	m = NewMonitor(p)
	if got := m.Status().UncleanShutdowns; got != 0 {
		t.Errorf("正常終了後の不正終了 = %d, want 0", got)
	}

	// 正常終了せずに3回目の起動 = 電源を引き抜かれた
	m = NewMonitor(p)
	if got := m.Status().UncleanShutdowns; got != 1 {
		t.Errorf("不正終了後の不正終了 = %d, want 1", got)
	}
	if got := m.Status().BootCount; got != 3 {
		t.Errorf("起動回数 = %d, want 3", got)
	}

	// さらにもう一度落ちる
	m = NewMonitor(p)
	if got := m.Status().UncleanShutdowns; got != 2 {
		t.Errorf("不正終了 = %d, want 2", got)
	}
}

// 状態ファイルが壊れていても起動できること。
// SDが壊れやすい環境なので、状態ファイルの破損で起動不能になっては本末転倒。
func TestMonitor_SurvivesCorruptState(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "health.json")
	if err := os.WriteFile(p, []byte("{壊れている"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewMonitor(p)
	if m == nil {
		t.Fatal("壊れた状態ファイルで nil が返った")
	}
	if got := m.Status().BootCount; got != 1 {
		t.Errorf("起動回数 = %d, want 1 (壊れていたら数え直す)", got)
	}
}

func TestMonitor_NilSafe(t *testing.T) {
	var m *Monitor
	m.MarkCleanShutdown()
	s := m.Status() // panic しないこと
	if s.UncleanShutdowns != 0 {
		t.Errorf("nil の不正終了 = %d", s.UncleanShutdowns)
	}
}

func TestStatus_Alert(t *testing.T) {
	tests := []struct {
		name string
		s    Status
		want string
	}{
		{"正常", Status{SoCTempC: 50}, ""},
		{"電圧低下中は最優先", Status{UnderVoltageNow: true, ThrottledNow: true, SoCTempC: 85}, "電圧低下"},
		{"高温で制限中", Status{ThrottledNow: true, SoCTempC: 85}, "高温で制限中"},
		{"SoC高温", Status{SoCTempC: 81}, "SoC高温"},
		{"電圧低下の履歴", Status{SoCTempC: 50, UnderVoltageEver: true}, "電圧低下の履歴あり"},
		{"境界: 79.9℃は正常", Status{SoCTempC: 79.9}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Alert(); got != tt.want {
				t.Errorf("Alert() = %q, want %q", got, tt.want)
			}
		})
	}
}

// throttled レジスタのビット分解。実機の 0x0 と、代表的な異常値で確認する。
func TestThrottledBits(t *testing.T) {
	tests := []struct {
		raw                        uint32
		uvNow, uvEver, thNow, thEv bool
	}{
		{0x0, false, false, false, false},
		{0x1, true, false, false, false},     // 今まさに電圧低下
		{0x10000, false, true, false, false}, // 起動後に電圧低下があった
		{0x50005, true, true, true, true},    // 電圧低下+温度制限が現在も履歴も
		{0x40004, false, false, true, true},  // 温度制限のみ
	}
	for _, tt := range tests {
		s := Status{
			UnderVoltageNow:  tt.raw&bitUnderVoltageNow != 0,
			UnderVoltageEver: tt.raw&bitUnderVoltageEver != 0,
			ThrottledNow:     tt.raw&bitThrottledNow != 0,
			ThrottledEver:    tt.raw&(bitThrottledEver|bitFreqCappedEver) != 0,
		}
		if s.UnderVoltageNow != tt.uvNow || s.UnderVoltageEver != tt.uvEver ||
			s.ThrottledNow != tt.thNow || s.ThrottledEver != tt.thEv {
			t.Errorf("raw=0x%X → %+v", tt.raw, s)
		}
	}
}
