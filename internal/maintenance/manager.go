// Package maintenance は走行距離/日付ベースのメンテナンスリマインダーを管理する。
// 状態はJSONファイルに永続化し、累計走行距離とリマインダーごとのリセット履歴を保持する。
// GASダッシュボードからリモートリセットが可能。
package maintenance

import (
	"encoding/json"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hashimoto/pi-obd-meter/internal/atomicfile"
)

// ReminderType はリマインダーの種類
type ReminderType string

const (
	TypeDistance ReminderType = "distance" // 距離ベース（オイル交換等）
	TypeDate     ReminderType = "date"     // 日付ベース（車検等）
)

// Severity はリマインダーの深刻度
type Severity string

const (
	SeveritySoft Severity = "soft" // 黄→橙で止まる（超過しても赤にならない）
	SeverityHard Severity = "hard" // 黄→橙→赤（超過で赤）
)

// Reminder はメンテナンスリマインダー
type Reminder struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Lamp         string       `json:"lamp,omitempty"`
	Severity     Severity     `json:"severity,omitempty"`
	Type         ReminderType `json:"type"`
	IntervalKm   float64      `json:"interval_km,omitempty"`   // 距離ベース: 交換間隔 (km)
	IntervalDays int          `json:"interval_days,omitempty"` // 日付ベース: 間隔 (日)
	LastResetKm  float64      `json:"last_reset_km"`           // 前回リセット時の総走行距離
	LastResetAt  time.Time    `json:"last_reset_at"`           // 前回リセット日時
	NotifiedAt   *time.Time   `json:"notified_at,omitempty"`   // 最後に通知した日時
	WarningPct   float64      `json:"warning_pct"`             // 警告を出す割合 (0.8 = 80%到達時)
}

// LampLabel はランプ表示用のラベルを返す。Lamp が未設定なら ID を大文字にする。
func (r *Reminder) LampLabel() string {
	if r.Lamp != "" {
		return r.Lamp
	}
	return strings.ToUpper(r.ID)
}

// AlertLevel は警告灯の色レベル
type AlertLevel string

const (
	AlertNone   AlertLevel = ""       // 消灯
	AlertYellow AlertLevel = "yellow" // 認知「わかってるよ」
	AlertOrange AlertLevel = "orange" // 催促「そろそろですよ」
	AlertRed    AlertLevel = "red"    // 超過「アウト」(hard のみ)
)

// Status はリマインダーの現在状態
type Status struct {
	Reminder    Reminder   `json:"reminder"`
	Lamp        string     `json:"lamp"`
	Alert       AlertLevel `json:"alert"`
	CurrentKm   float64    `json:"current_km,omitempty"`   // 前回リセットからの走行距離
	RemainingKm float64    `json:"remaining_km,omitempty"` // 残り距離
	DaysElapsed int        `json:"days_elapsed,omitempty"` // 前回リセットからの経過日数
	DaysLeft    int        `json:"days_left,omitempty"`    // 残り日数
	Progress    float64    `json:"progress"`               // 進捗 0.0 - 1.0+
	NeedsAlert  bool       `json:"needs_alert"`            // 通知が必要か
	IsOverdue   bool       `json:"is_overdue"`             // 超過しているか
}

// Manager はメンテナンスリマインダーを管理する
type Manager struct {
	mu            sync.RWMutex
	reminders     map[string]*Reminder
	filePath      string
	totalKm       float64 // 累計走行距離
	saveErrLogged bool    // 書き込みエラーを既にログ出力したか
}

// NewManager は新しいManagerを作成する
func NewManager(filePath string) *Manager {
	m := &Manager{
		reminders: make(map[string]*Reminder),
		filePath:  filePath,
	}
	m.load()
	return m
}

// InitDefaults はリマインダーを初期化する。
// configReminders が指定されていればそれを使い、空ならハードコードのデフォルト値を使う。
func (m *Manager) InitDefaults(configReminders []Reminder) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var defaults []*Reminder

	if len(configReminders) > 0 {
		defaults = make([]*Reminder, len(configReminders))
		for i := range configReminders {
			r := configReminders[i]
			defaults[i] = &r
		}
	} else {
		defaults = []*Reminder{
			{
				ID: "oil_change", Name: "エンジンオイル交換",
				Type: TypeDistance, IntervalKm: 3000, WarningPct: 0.8,
			},
			{
				ID: "air_filter", Name: "エアフィルター交換",
				Type: TypeDistance, IntervalKm: 20000, WarningPct: 0.85,
			},
			{
				ID: "tire_rotation", Name: "タイヤローテーション",
				Type: TypeDistance, IntervalKm: 10000, WarningPct: 0.8,
			},
			{
				ID: "shaken", Name: "車検",
				Type: TypeDate, IntervalDays: 730, WarningPct: 0.9,
			},
			{
				ID: "atf_change", Name: "ATF交換",
				Type: TypeDistance, IntervalKm: 40000, WarningPct: 0.9,
			},
		}
	}

	for _, r := range defaults {
		if _, exists := m.reminders[r.ID]; !exists {
			if r.LastResetAt.IsZero() {
				r.LastResetAt = time.Now()
			}
			m.reminders[r.ID] = r
		}
	}

	m.save()
}

// UpdateTotalKm は累計走行距離を更新する。1kmごとにファイルに永続化する。
func (m *Manager) UpdateTotalKm(totalKm float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := m.totalKm
	m.totalKm = totalKm
	// 1km刻みで永続化（頻繁な書き込みを防ぐ）
	if int(totalKm) > int(prev) {
		m.save()
	}
}

// CheckAll は全リマインダーの状態をチェックする
func (m *Manager) CheckAll() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := []Status{}
	for _, r := range m.reminders {
		statuses = append(statuses, m.checkOne(r))
	}
	return statuses
}

// GetAlerts は通知が必要なリマインダーを進捗の高い順（緊急度順）に返す
func (m *Manager) GetAlerts() []Status {
	all := m.CheckAll()
	alerts := []Status{}
	for _, s := range all {
		if s.NeedsAlert {
			alerts = append(alerts, s)
		}
	}
	sort.Slice(alerts, func(i, j int) bool {
		return alerts[i].Progress > alerts[j].Progress
	})
	return alerts
}

// checkOne は1件のリマインダーの進捗・アラート状態を計算する
func (m *Manager) checkOne(r *Reminder) Status {
	s := Status{Reminder: *r, Lamp: r.LampLabel()}

	switch r.Type {
	case TypeDistance:
		s.CurrentKm = m.totalKm - r.LastResetKm
		s.RemainingKm = r.IntervalKm - s.CurrentKm
		if r.IntervalKm > 0 {
			s.Progress = s.CurrentKm / r.IntervalKm
		}
		s.IsOverdue = s.RemainingKm <= 0
		s.NeedsAlert = s.Progress >= r.WarningPct

	case TypeDate:
		s.DaysElapsed = int(time.Since(r.LastResetAt).Hours() / 24)
		s.DaysLeft = r.IntervalDays - s.DaysElapsed
		if r.IntervalDays > 0 {
			s.Progress = float64(s.DaysElapsed) / float64(r.IntervalDays)
		}
		s.IsOverdue = s.DaysLeft <= 0
		s.NeedsAlert = s.Progress >= r.WarningPct
	}

	// 警告灯の色レベルを判定
	s.Alert = r.alertLevel(s.Progress, s.IsOverdue)
	s.NeedsAlert = s.Alert != AlertNone

	// 同じ日に2回通知しない
	if r.NotifiedAt != nil {
		today := time.Now().Truncate(24 * time.Hour)
		lastNotified := r.NotifiedAt.Truncate(24 * time.Hour)
		if today.Equal(lastNotified) {
			s.NeedsAlert = false
		}
	}

	return s
}

// alertLevel は progress と severity から警告灯の色を返す
// soft: 消灯 → 黄(warning_pct) → 橙(超過)
// hard: 消灯 → 橙(warning_pct) → 赤(超過)
func (r *Reminder) alertLevel(progress float64, overdue bool) AlertLevel {
	if overdue {
		if r.Severity == SeverityHard {
			return AlertRed
		}
		return AlertOrange
	}
	if progress >= r.WarningPct {
		if r.Severity == SeverityHard {
			return AlertOrange
		}
		return AlertYellow
	}
	return AlertNone
}

// GetAll は全リマインダーを返す
func (m *Manager) GetAll() []*Reminder {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Reminder, 0, len(m.reminders))
	for _, r := range m.reminders {
		result = append(result, r)
	}
	return result
}

// TotalKm は永続化された累計走行距離を返す（起動時の復元用）
func (m *Manager) TotalKm() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.totalKm
}

// ResetReminder は指定IDのリマインダーをリセットする
func (m *Manager) ResetReminder(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, exists := m.reminders[id]
	if !exists {
		return false
	}

	now := time.Now()
	r.LastResetKm = m.totalKm
	r.LastResetAt = now
	r.NotifiedAt = nil
	m.save()
	return true
}

// SaveState はメンテナンス状態を強制保存する（シャットダウン時に呼ぶ）
func (m *Manager) SaveState() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.save()
}

// --- 永続化（maintenance.json） ---

// persistState はファイルに保存する状態（reminders + totalKm）
type persistState struct {
	TotalKm   float64              `json:"total_km"`
	Reminders map[string]*Reminder `json:"reminders"`
}

// save はリマインダー状態をJSONファイルにアトミックに書き出す。
// 一時ファイルに書き込んでからrenameすることで、電源断時にファイルが壊れるのを防ぐ。
func (m *Manager) save() {
	state := persistState{
		TotalKm:   m.totalKm,
		Reminders: m.reminders,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		slog.Error("メンテ状態シリアライズ失敗", "error", err)
		return
	}
	if err := atomicfile.Write(m.filePath, data, 0644); err != nil {
		if !m.saveErrLogged {
			slog.Warn("メンテ状態保存失敗", "path", m.filePath, "error", err)
			m.saveErrLogged = true
		}
	}
}

// load はJSONファイルからリマインダー状態を復元する（新旧フォーマット対応）
func (m *Manager) load() {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return
	}

	// 新フォーマット（persistState）を試す
	var state persistState
	if err := json.Unmarshal(data, &state); err == nil && state.Reminders != nil {
		m.reminders = state.Reminders
		m.totalKm = state.TotalKm
		return
	}

	// 旧フォーマット（map[string]*Reminder のみ）からのマイグレーション
	var old map[string]*Reminder
	if err := json.Unmarshal(data, &old); err != nil {
		slog.Warn("メンテ状態パース失敗、デフォルト使用", "error", err)
		return
	}
	m.reminders = old
}
