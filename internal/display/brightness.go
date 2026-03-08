// Package display はHDMIディスプレイの輝度を時刻ベースで自動制御する。
// xrandr コマンドを使用してソフトウェア輝度を変更する。
package display

import (
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"time"
)

// BrightnessSchedule は時間帯ごとの輝度設定
type BrightnessSchedule struct {
	Hour       int     `json:"hour"`       // 開始時刻（0-23）
	Brightness float64 `json:"brightness"` // 輝度（0.0〜1.0）
	Label      string  `json:"label"`      // 表示名
}

// BrightnessConfig は輝度制御の設定
type BrightnessConfig struct {
	HDMIOutput string               `json:"hdmi_output"` // "HDMI-1" etc.
	Schedule   []BrightnessSchedule `json:"schedule"`
}

// DefaultConfig は日本の一般的な日照に合わせたデフォルト設定
func DefaultConfig() BrightnessConfig {
	return BrightnessConfig{
		HDMIOutput: "HDMI-1",
		Schedule: []BrightnessSchedule{
			{Hour: 5, Brightness: 0.6, Label: "早朝"},
			{Hour: 7, Brightness: 1.0, Label: "昼間"},
			{Hour: 17, Brightness: 0.7, Label: "夕方"},
			{Hour: 19, Brightness: 0.5, Label: "夜間"},
			{Hour: 22, Brightness: 0.3, Label: "深夜"},
		},
	}
}

// BrightnessController は液晶の輝度を時刻ベースで自動制御する
type BrightnessController struct {
	mu      sync.RWMutex
	config  BrightnessConfig
	current float64 // 現在の輝度（0.0〜1.0）
	stopCh  chan struct{}
}

// NewBrightnessController は新しい輝度コントローラーを作成する
func NewBrightnessController(config BrightnessConfig) *BrightnessController {
	if len(config.Schedule) == 0 {
		config = DefaultConfig()
	}
	if config.HDMIOutput == "" {
		config.HDMIOutput = "HDMI-1"
	}

	bc := &BrightnessController{
		config:  config,
		current: 1.0,
		stopCh:  make(chan struct{}),
	}

	// 起動時に即座に適用
	bc.applyScheduled(time.Now())

	return bc
}

// Start は自動輝度制御を開始する（1分間隔）
func (bc *BrightnessController) Start() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case now := <-ticker.C:
				bc.applyScheduled(now)
			case <-bc.stopCh:
				return
			}
		}
	}()
	slog.Info("輝度自動制御開始", "interval", "1m")
}

// Stop は自動輝度制御を停止する
func (bc *BrightnessController) Stop() {
	close(bc.stopCh)
}

// applyScheduled は時刻に基づいて輝度を適用する
func (bc *BrightnessController) applyScheduled(now time.Time) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	target := bc.brightnessForTime(now)
	if target != bc.current {
		bc.setBrightness(target)
		slog.Info("輝度変更", "time", now.Format("15:04"), "brightness", fmt.Sprintf("%.0f%%", target*100), "label", bc.labelForTime(now))
	}
}

// brightnessForTime は指定時刻のスケジュール輝度を返す
func (bc *BrightnessController) brightnessForTime(t time.Time) float64 {
	hour := t.Hour()
	schedule := bc.config.Schedule

	// スケジュールを逆順に走査して、現在時刻以前で最も近い設定を見つける
	result := schedule[len(schedule)-1].Brightness // デフォルトは最後のエントリ
	for i := len(schedule) - 1; i >= 0; i-- {
		if hour >= schedule[i].Hour {
			result = schedule[i].Brightness
			break
		}
	}
	return result
}

// labelForTime は指定時刻のスケジュールラベルを返す
func (bc *BrightnessController) labelForTime(t time.Time) string {
	hour := t.Hour()
	schedule := bc.config.Schedule

	result := schedule[len(schedule)-1].Label
	for i := len(schedule) - 1; i >= 0; i-- {
		if hour >= schedule[i].Hour {
			result = schedule[i].Label
			break
		}
	}
	return result
}

// setBrightness は実際にxrandrを呼んで輝度を変更する（ロック保持下で呼ぶ）
func (bc *BrightnessController) setBrightness(value float64) {
	bc.current = value

	// xrandr --output HDMI-1 --brightness 0.5
	cmd := exec.Command("xrandr",
		"--output", bc.config.HDMIOutput,
		"--brightness", fmt.Sprintf("%.2f", value),
	)
	if err := cmd.Run(); err != nil {
		slog.Warn("xrandr輝度変更失敗", "error", err)
	}
}
