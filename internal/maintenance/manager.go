// Package maintenance はオイル交換の距離管理を行う。
// 状態はJSONファイルに永続化し、累計走行距離と前回交換時ODOを保持する。
// GASダッシュボードからリモートリセットが可能。
package maintenance

import (
	"encoding/json"
	"log/slog"
	"os"
	"sync"

	"github.com/hashimoto/pi-obd-meter/internal/atomicfile"
)

// OilConfig はオイル交換の設定
type OilConfig struct {
	IntervalKm float64 `json:"interval_km"` // 交換間隔 (km)
	WarningKm  float64 `json:"warning_km"`  // 黄色警告の距離 (km)
	DangerKm   float64 `json:"danger_km"`   // 赤警告の距離 (km)
}

// DefaultOilConfig はデフォルトのオイル交換設定を返す
func DefaultOilConfig() OilConfig {
	return OilConfig{
		IntervalKm: 3000,
		WarningKm:  2500,
		DangerKm:   4000,
	}
}

// AlertLevel は警告灯の色レベル
type AlertLevel string

const (
	AlertGreen  AlertLevel = "green"  // 正常
	AlertYellow AlertLevel = "yellow" // そろそろ準備
	AlertOrange AlertLevel = "orange" // 交換時期
	AlertRed    AlertLevel = "red"    // 超過
)

// OilStatus はオイル交換の現在状態
type OilStatus struct {
	CurrentKm   float64    `json:"current_km"`   // 前回交換からの走行距離
	RemainingKm float64    `json:"remaining_km"` // 交換までの残り距離
	Alert       AlertLevel `json:"alert"`        // 警告レベル
	IntervalKm  float64    `json:"interval_km"`  // 交換間隔
	WarningKm   float64    `json:"warning_km"`   // 黄色警告距離
	DangerKm    float64    `json:"danger_km"`    // 赤警告距離
}

// Manager はオイル交換と累計走行距離を管理する
type Manager struct {
	mu            sync.RWMutex
	oil           OilConfig
	lastResetKm   float64 // 前回オイル交換時のODO
	totalKm       float64 // 累計走行距離
	filePath      string
	saveErrLogged bool
}

// NewManager は新しいManagerを作成する
func NewManager(filePath string, oil OilConfig) *Manager {
	m := &Manager{
		oil:      oil,
		filePath: filePath,
	}
	m.load()
	return m
}

// OilStatus はオイル交換の現在状態を返す
func (m *Manager) OilStatus() OilStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	current := m.totalKm - m.lastResetKm
	remaining := m.oil.IntervalKm - current

	return OilStatus{
		CurrentKm:   current,
		RemainingKm: remaining,
		Alert:       m.oilAlert(current),
		IntervalKm:  m.oil.IntervalKm,
		WarningKm:   m.oil.WarningKm,
		DangerKm:    m.oil.DangerKm,
	}
}

// oilAlert は走行距離から警告レベルを返す
// 緑: 0〜warning_km, 黄: warning_km〜interval_km, 橙: interval_km〜danger_km, 赤: danger_km〜
func (m *Manager) oilAlert(currentKm float64) AlertLevel {
	if currentKm >= m.oil.DangerKm {
		return AlertRed
	}
	if currentKm >= m.oil.IntervalKm {
		return AlertOrange
	}
	if currentKm >= m.oil.WarningKm {
		return AlertYellow
	}
	return AlertGreen
}

// UpdateTotalKm は累計走行距離を更新する。1kmごとにファイルに永続化する。
func (m *Manager) UpdateTotalKm(totalKm float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := m.totalKm
	m.totalKm = totalKm
	if int(totalKm) > int(prev) {
		m.save()
	}
}

// TotalKm は永続化された累計走行距離を返す
func (m *Manager) TotalKm() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.totalKm
}

// ResetOil はオイル交換をリセットする（現在のODOを記録）
func (m *Manager) ResetOil() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastResetKm = m.totalKm
	m.save()
}

// LastResetKm は前回オイル交換時のODOを返す
func (m *Manager) LastResetKm() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastResetKm
}

// SaveState は状態を強制保存する（シャットダウン時に呼ぶ）
func (m *Manager) SaveState() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.save()
}

// --- 永続化（maintenance.json） ---

type persistState struct {
	TotalKm     float64 `json:"total_km"`
	LastResetKm float64 `json:"last_reset_km"`
}

func (m *Manager) save() {
	state := persistState{
		TotalKm:     m.totalKm,
		LastResetKm: m.lastResetKm,
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

func (m *Manager) load() {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return
	}

	// まず旧フォーマット（reminders付き）をチェック
	var old struct {
		TotalKm   float64                    `json:"total_km"`
		Reminders map[string]json.RawMessage `json:"reminders"`
	}
	if err := json.Unmarshal(data, &old); err == nil && old.Reminders != nil {
		m.totalKm = old.TotalKm
		// 旧フォーマットの oil_change から last_reset_km を復元
		if raw, ok := old.Reminders["oil_change"]; ok {
			var r struct {
				LastResetKm float64 `json:"last_reset_km"`
			}
			if json.Unmarshal(raw, &r) == nil {
				m.lastResetKm = r.LastResetKm
			}
		}
		// 新フォーマットで保存し直す
		m.save()
		slog.Info("メンテ状態を新フォーマットにマイグレーション", "total_km", m.totalKm, "last_reset_km", m.lastResetKm)
		return
	}

	// 新フォーマット
	var state persistState
	if err := json.Unmarshal(data, &state); err == nil && state.TotalKm > 0 {
		m.totalKm = state.TotalKm
		m.lastResetKm = state.LastResetKm
	}
}
