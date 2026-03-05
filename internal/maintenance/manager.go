package maintenance

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// ReminderType はリマインダーの種類
type ReminderType string

const (
	TypeDistance ReminderType = "distance" // 距離ベース（オイル交換等）
	TypeDate    ReminderType = "date"     // 日付ベース（車検等）
)

// Reminder はメンテナンスリマインダー
type Reminder struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Type        ReminderType `json:"type"`
	IntervalKm  float64      `json:"interval_km,omitempty"`  // 距離ベース: 交換間隔 (km)
	IntervalDays int         `json:"interval_days,omitempty"` // 日付ベース: 間隔 (日)
	LastResetKm float64      `json:"last_reset_km"`           // 前回リセット時の総走行距離
	LastResetAt time.Time    `json:"last_reset_at"`           // 前回リセット日時
	NotifiedAt  *time.Time   `json:"notified_at,omitempty"`   // 最後に通知した日時
	WarningPct  float64      `json:"warning_pct"`             // 警告を出す割合 (0.8 = 80%到達時)
}

// Status はリマインダーの現在状態
type Status struct {
	Reminder    Reminder `json:"reminder"`
	CurrentKm   float64  `json:"current_km,omitempty"`     // 前回リセットからの走行距離
	RemainingKm float64  `json:"remaining_km,omitempty"`   // 残り距離
	DaysElapsed int      `json:"days_elapsed,omitempty"`   // 前回リセットからの経過日数
	DaysLeft    int      `json:"days_left,omitempty"`      // 残り日数
	Progress    float64  `json:"progress"`                 // 進捗 0.0 - 1.0+
	NeedsAlert  bool     `json:"needs_alert"`              // 通知が必要か
	IsOverdue   bool     `json:"is_overdue"`               // 超過しているか
}

// Manager はメンテナンスリマインダーを管理する
type Manager struct {
	mu        sync.RWMutex
	reminders map[string]*Reminder
	filePath  string
	totalKm   float64 // 累計走行距離
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

// InitDefaults はDYデミオ用のデフォルトリマインダーを設定する
func (m *Manager) InitDefaults() {
	m.mu.Lock()
	defer m.mu.Unlock()

	defaults := []*Reminder{
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
			Type: TypeDate, IntervalDays: 730, WarningPct: 0.9, // 2年、残り2ヶ月で警告
		},
		{
			ID: "atf_change", Name: "ATF交換",
			Type: TypeDistance, IntervalKm: 40000, WarningPct: 0.9, // 残り4,000kmで警告
			LastResetKm: 80000, // 前回交換: ODO 80,000km → 次回: 120,000km
		},
	}

	for _, r := range defaults {
		if _, exists := m.reminders[r.ID]; !exists {
			now := time.Now()
			r.LastResetAt = now
			m.reminders[r.ID] = r
		}
	}

	m.save()
}

// UpdateTotalKm は累計走行距離を更新する
func (m *Manager) UpdateTotalKm(totalKm float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.totalKm = totalKm
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

// GetAlerts は通知が必要なリマインダーを返す
func (m *Manager) GetAlerts() []Status {
	all := m.CheckAll()
	alerts := []Status{}
	for _, s := range all {
		if s.NeedsAlert {
			alerts = append(alerts, s)
		}
	}
	return alerts
}

func (m *Manager) checkOne(r *Reminder) Status {
	s := Status{Reminder: *r}

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

// ResetReminder はリマインダーをリセットする（メンテナンス実施時）
func (m *Manager) ResetReminder(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.reminders[id]
	if !ok {
		return fmt.Errorf("リマインダーが見つかりません: %s", id)
	}

	now := time.Now()
	r.LastResetKm = m.totalKm
	r.LastResetAt = now
	r.NotifiedAt = nil

	m.save()
	return nil
}

// MarkNotified は通知済みにマークする
func (m *Manager) MarkNotified(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if r, ok := m.reminders[id]; ok {
		now := time.Now()
		r.NotifiedAt = &now
		m.save()
	}
}

// AddReminder はカスタムリマインダーを追加する
func (m *Manager) AddReminder(r *Reminder) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if r.LastResetAt.IsZero() {
		r.LastResetAt = time.Now()
	}
	if r.WarningPct == 0 {
		r.WarningPct = 0.8
	}
	r.LastResetKm = m.totalKm
	m.reminders[r.ID] = r
	m.save()
}

// RemoveReminder はリマインダーを削除する
func (m *Manager) RemoveReminder(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.reminders[id]; !ok {
		return fmt.Errorf("リマインダーが見つかりません: %s", id)
	}

	delete(m.reminders, id)
	m.save()
	return nil
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

// --- 永続化 ---

func (m *Manager) save() {
	data, _ := json.MarshalIndent(m.reminders, "", "  ")
	os.WriteFile(m.filePath, data, 0644)
}

func (m *Manager) load() {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return
	}
	json.Unmarshal(data, &m.reminders)
}
