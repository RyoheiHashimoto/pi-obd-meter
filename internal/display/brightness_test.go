package display

import (
	"testing"
	"time"
)

// デフォルトスケジュールでコントローラーを作成（xrandrは呼ばない）
func newTestController() *BrightnessController {
	return &BrightnessController{
		config:  DefaultConfig(),
		current: 1.0,
		stopCh:  make(chan struct{}),
	}
}

func timeAt(hour int) time.Time {
	return time.Date(2026, 1, 1, hour, 0, 0, 0, time.Local)
}

// --- brightnessForTime ---

func TestBrightnessForTime_Default(t *testing.T) {
	// デフォルトスケジュール:
	//   5時=0.6  7時=1.0  17時=0.7  19時=0.5  22時=0.3
	bc := newTestController()

	tests := []struct {
		name string
		hour int
		want float64
	}{
		{"深夜 0時 → 0.3（前日22時の設定が継続）", 0, 0.3},
		{"深夜 3時 → 0.3", 3, 0.3},
		{"早朝 5時 → 0.6", 5, 0.6},
		{"早朝 6時 → 0.6", 6, 0.6},
		{"昼間 7時 → 1.0", 7, 1.0},
		{"昼間 12時 → 1.0", 12, 1.0},
		{"昼間 16時 → 1.0", 16, 1.0},
		{"夕方 17時 → 0.7", 17, 0.7},
		{"夕方 18時 → 0.7", 18, 0.7},
		{"夜間 19時 → 0.5", 19, 0.5},
		{"夜間 21時 → 0.5", 21, 0.5},
		{"深夜 22時 → 0.3", 22, 0.3},
		{"深夜 23時 → 0.3", 23, 0.3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bc.brightnessForTime(timeAt(tt.hour))
			if got != tt.want {
				t.Errorf("hour %d: got %.1f, want %.1f", tt.hour, got, tt.want)
			}
		})
	}
}

// --- labelForTime ---

func TestLabelForTime_Default(t *testing.T) {
	bc := newTestController()

	tests := []struct {
		hour int
		want string
	}{
		{0, "深夜"},
		{4, "深夜"},
		{5, "早朝"},
		{7, "昼間"},
		{12, "昼間"},
		{17, "夕方"},
		{19, "夜間"},
		{22, "深夜"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := bc.labelForTime(timeAt(tt.hour))
			if got != tt.want {
				t.Errorf("hour %d: got %q, want %q", tt.hour, got, tt.want)
			}
		})
	}
}

// --- カスタムスケジュール ---

func TestBrightnessForTime_Custom(t *testing.T) {
	bc := &BrightnessController{
		config: BrightnessConfig{
			Schedule: []BrightnessSchedule{
				{Hour: 8, Brightness: 1.0, Label: "昼"},
				{Hour: 20, Brightness: 0.4, Label: "夜"},
			},
		},
		current: 1.0,
		stopCh:  make(chan struct{}),
	}

	// 8時前 → 最後のエントリ(0.4)がデフォルト
	if got := bc.brightnessForTime(timeAt(6)); got != 0.4 {
		t.Errorf("hour 6: got %.1f, want 0.4", got)
	}
	// 8時 → 1.0
	if got := bc.brightnessForTime(timeAt(8)); got != 1.0 {
		t.Errorf("hour 8: got %.1f, want 1.0", got)
	}
	// 20時 → 0.4
	if got := bc.brightnessForTime(timeAt(20)); got != 0.4 {
		t.Errorf("hour 20: got %.1f, want 0.4", got)
	}
}

// --- DefaultConfig ---

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.HDMIOutput != "HDMI-1" {
		t.Errorf("HDMIOutput: got %q, want HDMI-1", cfg.HDMIOutput)
	}
	if len(cfg.Schedule) != 5 {
		t.Errorf("Schedule length: got %d, want 5", len(cfg.Schedule))
	}
}

// --- NewBrightnessController ---

func TestNewBrightnessController_EmptySchedule(t *testing.T) {
	bc := NewBrightnessController(BrightnessConfig{})
	// 空スケジュールの場合デフォルトが使われる
	if len(bc.config.Schedule) != 5 {
		t.Errorf("expected default schedule (5 entries), got %d", len(bc.config.Schedule))
	}
}

func TestNewBrightnessController_EmptyOutput(t *testing.T) {
	bc := NewBrightnessController(BrightnessConfig{
		Schedule: []BrightnessSchedule{
			{Hour: 0, Brightness: 0.5, Label: "test"},
		},
	})
	if bc.config.HDMIOutput != "HDMI-1" {
		t.Errorf("expected default HDMI-1, got %q", bc.config.HDMIOutput)
	}
}
