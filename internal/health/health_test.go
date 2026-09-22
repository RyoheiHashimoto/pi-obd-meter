package health

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

// ログが実際に伸びているかの判定。
//
// systemd の active/inactive では「動いているのに書けていない」を拾えない。
// #164 の gps-log がまさにそれで、active のまま中身が空だった。
func TestReadLoggers_WritingFromMTime(t *testing.T) {
	dir := t.TempDir()

	fresh := filepath.Join(dir, "fresh")
	stale := filepath.Join(dir, "stale")
	empty := filepath.Join(dir, "empty")
	for _, d := range []string{fresh, stale, empty} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeAt := func(dir, name string, age time.Duration) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-age)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	writeAt(fresh, "drive-0101-0000.csv", 3*time.Second)
	writeAt(stale, "gps-0101-0000.csv", 10*time.Minute)

	orig := loggerLogDirs
	loggerLogDirs = map[string]string{
		"drive-verify": fresh,
		"gps-log":      stale,
		"imu-log":      empty,
		"can-verify":   filepath.Join(dir, "存在しない"),
	}
	defer func() { loggerLogDirs = orig }()

	got := readLoggers()

	if !got["drive-verify"].Writing {
		t.Errorf("drive-verify: 3秒前に更新されたのに writing=false (age=%d)", got["drive-verify"].AgeSec)
	}
	if got["gps-log"].Writing {
		t.Errorf("gps-log: 10分止まっているのに writing=true")
	}
	if got["gps-log"].AgeSec < 540 {
		t.Errorf("gps-log: 経過 = %d秒, want 540以上", got["gps-log"].AgeSec)
	}
	// ログが1本も無いのと「今まさに書いた (age=0)」は別物。-1 で区別する。
	if got["imu-log"].AgeSec != -1 {
		t.Errorf("imu-log: 空ディレクトリの経過 = %d, want -1", got["imu-log"].AgeSec)
	}
	if got["can-verify"].AgeSec != -1 {
		t.Errorf("can-verify: ディレクトリ無しの経過 = %d, want -1", got["can-verify"].AgeSec)
	}
	if len(got) != len(loggerUnits) {
		t.Errorf("ロガー数 = %d, want %d", len(got), len(loggerUnits))
	}
}

// 壁時計が飛んでも赤くしない。Pi に RTC が無く、NTP 同期の瞬間に
// 時刻が数時間ずれる (#195)。mtime が未来になっても経過は負にしない。
func TestAgeSec_NeverNegative(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		mt   time.Time
		want int64
	}{
		{"3秒前", now.Add(-3 * time.Second), 3},
		{"同時刻", now, 0},
		{"2時間先 (時刻が飛んだ直後)", now.Add(2 * time.Hour), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ageSec(now, tt.mt); got != tt.want {
				t.Errorf("ageSec() = %d, want %d", got, tt.want)
			}
		})
	}
}

// 一番新しいファイルを見る。ディレクトリは数えない。
func TestNewestMTime(t *testing.T) {
	dir := t.TempDir()
	if _, ok := newestMTime(dir); ok {
		t.Error("空ディレクトリで ok=true")
	}
	if _, ok := newestMTime(filepath.Join(dir, "無い")); ok {
		t.Error("存在しないディレクトリで ok=true")
	}
	if _, ok := newestMTime(""); ok {
		t.Error("空文字列で ok=true")
	}

	// サブディレクトリだけがあっても「ファイルは無い」
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := newestMTime(dir); ok {
		t.Error("サブディレクトリのみで ok=true")
	}

	old := time.Now().Add(-time.Hour)
	recent := time.Now().Add(-time.Minute)
	for _, f := range []struct {
		name string
		mt   time.Time
	}{{"a.csv", old}, {"b.csv", recent}, {"c.csv", old}} {
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, f.mt, f.mt); err != nil {
			t.Fatal(err)
		}
	}
	got, ok := newestMTime(dir)
	if !ok {
		t.Fatal("ファイルがあるのに ok=false")
	}
	if d := got.Sub(recent); d < -time.Second || d > time.Second {
		t.Errorf("最新の mtime = %v, want %v", got, recent)
	}
}
