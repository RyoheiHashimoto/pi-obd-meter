package display

import (
	"fmt"
	"log"
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
	mu       sync.RWMutex
	config   BrightnessConfig
	current  float64  // 現在の輝度（0.0〜1.0）
	manual   *float64 // 手動オーバーライド（nilなら自動）
	manualAt time.Time
	stopCh   chan struct{}
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
	log.Println("✓ 輝度自動制御 開始（1分間隔）")
}

// Stop は自動輝度制御を停止する
func (bc *BrightnessController) Stop() {
	close(bc.stopCh)
}

// applyScheduled は時刻に基づいて輝度を適用する
func (bc *BrightnessController) applyScheduled(now time.Time) {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	// 手動オーバーライド中なら、次の時間帯境界まで維持
	if bc.manual != nil {
		scheduled := bc.brightnessForTime(now)
		prev := bc.brightnessForTime(bc.manualAt)
		// 時間帯が変わったら手動オーバーライドを解除
		if scheduled != prev {
			log.Printf("🔆 時間帯変更を検知、手動オーバーライド解除 → 自動 %.0f%%", scheduled*100)
			bc.manual = nil
		} else {
			return // まだ同じ時間帯、手動を維持
		}
	}

	target := bc.brightnessForTime(now)
	if target != bc.current {
		bc.setBrightness(target)
		log.Printf("🔆 時刻 %s → 輝度 %.0f%% (%s)",
			now.Format("15:04"), target*100, bc.labelForTime(now))
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

// SetManual は手動で輝度を設定する（次の時間帯切替まで有効）
func (bc *BrightnessController) SetManual(brightness float64) {
	if brightness < 0.05 {
		brightness = 0.05 // 完全に真っ暗にはしない
	}
	if brightness > 1.0 {
		brightness = 1.0
	}

	bc.mu.Lock()
	defer bc.mu.Unlock()

	bc.manual = &brightness
	bc.manualAt = time.Now()
	bc.setBrightness(brightness)
	log.Printf("🔆 手動輝度設定: %.0f%% (次の時間帯切替で自動に復帰)", brightness*100)
}

// ClearManual は手動オーバーライドを解除して自動に戻す
func (bc *BrightnessController) ClearManual() {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	bc.manual = nil
	target := bc.brightnessForTime(time.Now())
	bc.setBrightness(target)
	log.Printf("🔆 自動輝度に復帰: %.0f%%", target*100)
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
		log.Printf("⚠ xrandr輝度変更失敗: %v (DISPLAY未設定またはHDMI未接続の可能性)", err)
	}
}

// Status は現在の輝度状態を返す
type BrightnessStatus struct {
	Current   float64              `json:"current"`    // 現在の輝度 0.0〜1.0
	Percent   int                  `json:"percent"`    // 現在の輝度 %
	IsManual  bool                 `json:"is_manual"`  // 手動オーバーライド中か
	Scheduled float64              `json:"scheduled"`  // スケジュール上の輝度
	TimeLabel string               `json:"time_label"` // 現在の時間帯ラベル
	Schedule  []BrightnessSchedule `json:"schedule"`   // 全スケジュール
}

func (bc *BrightnessController) Status() BrightnessStatus {
	bc.mu.RLock()
	defer bc.mu.RUnlock()

	now := time.Now()
	return BrightnessStatus{
		Current:   bc.current,
		Percent:   int(bc.current * 100),
		IsManual:  bc.manual != nil,
		Scheduled: bc.brightnessForTime(now),
		TimeLabel: bc.labelForTime(now),
		Schedule:  bc.config.Schedule,
	}
}
