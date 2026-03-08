// Package trip はトリップ（走行区間）の距離・時間・速度を追跡する。
// 車速を積分して走行距離を算出し、電源断に備えて状態をJSONファイルに永続化する。
// GASダッシュボードから給油記録時にトリップリセットが通知される。
package trip

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// TripData は1トリップ分の集計データ
type TripData struct {
	TripID         string    `json:"trip_id"`
	StartTime      time.Time `json:"start_time"`
	EndTime        time.Time `json:"end_time"`
	DistanceKm     float64   `json:"distance_km"`
	MaxSpeedKmh    float64   `json:"max_speed_kmh"`
	AvgSpeedKmh    float64   `json:"avg_speed_kmh"`
	DrivingTimeSec float64   `json:"driving_time_sec"`
	IdleTimeSec    float64   `json:"idle_time_sec"`
	Samples        int       `json:"samples"`
}

// Tracker はトリップの走行距離を追跡する
type Tracker struct {
	mu sync.Mutex

	// 現在のトリップ
	current       TripData
	lastTimestamp time.Time
	speedSum      float64

	// 永続化パス
	statePath     string
	saveErrLogged bool // 書き込みエラーを既にログ出力したか
}

// TrackerConfig はトラッカーの設定
type TrackerConfig struct {
	StatePath string // 状態保存パス
}

// NewTracker は新しいトラッカーを作成する
func NewTracker(cfg TrackerConfig) *Tracker {
	if cfg.StatePath == "" {
		cfg.StatePath = "/var/lib/pi-obd-meter/trip_state.json"
	}

	t := &Tracker{
		statePath: cfg.StatePath,
	}

	// 前回の状態を復元（電源断対応）
	t.loadState()

	return t
}

// Update はOBDデータからトリップを更新する
func (t *Tracker) Update(speedKmh float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()

	// 初回
	if t.lastTimestamp.IsZero() {
		t.lastTimestamp = now
		t.current.StartTime = now
		t.current.TripID = fmt.Sprintf("trip_%d", now.Unix())
		return
	}

	dt := now.Sub(t.lastTimestamp).Seconds()
	if dt <= 0 || dt > 10 { // 10秒以上の空白はスキップ（接続断等）
		t.lastTimestamp = now
		return
	}

	// 走行距離を積分 (km)
	distanceDelta := (speedKmh / 3600.0) * dt
	t.current.DistanceKm += distanceDelta

	// 統計
	t.current.Samples++
	if speedKmh > 1.0 {
		t.current.DrivingTimeSec += dt
		t.speedSum += speedKmh
	} else {
		t.current.IdleTimeSec += dt
	}
	if speedKmh > t.current.MaxSpeedKmh {
		t.current.MaxSpeedKmh = speedKmh
	}

	t.lastTimestamp = now

	// 定期的に状態を保存（1分ごと）
	if t.current.Samples%60 == 0 {
		t.saveState()
	}
}

// ManualReset はトリップを手動リセットする
func (t *Tracker) ManualReset() *TripData {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.finalize()
}

// finalize は現在のトリップを完了させて新しいトリップを開始する
func (t *Tracker) finalize() *TripData {
	if t.current.Samples == 0 {
		return nil
	}

	// 集計
	t.current.EndTime = time.Now()
	if t.current.DrivingTimeSec > 0 {
		t.current.AvgSpeedKmh = (t.current.DistanceKm / t.current.DrivingTimeSec) * 3600
	}

	completed := t.current

	// 新しいトリップを開始
	t.current = TripData{
		TripID:    fmt.Sprintf("trip_%d", time.Now().Unix()),
		StartTime: time.Now(),
	}
	t.speedSum = 0
	t.lastTimestamp = time.Time{}

	t.saveState()

	return &completed
}

// GetCurrent は現在のトリップデータのコピーを返す
func (t *Tracker) GetCurrent() TripData {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.current
}

// DistanceKm は現在のトリップ走行距離を返す
func (t *Tracker) DistanceKm() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.current.DistanceKm
}

// SaveState は現在のトリップ状態を強制保存する（シャットダウン時に呼ぶ）
func (t *Tracker) SaveState() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.saveState()
}

// --- 永続化（電源断からの復帰用） ---

// persistedState はJSONファイルに保存するトリップ状態
type persistedState struct {
	Current       TripData `json:"current"`
	LastTimestamp int64    `json:"last_timestamp"`
}

// saveState は現在のトリップ状態をJSONファイルに書き出す（overlayFS環境では失敗する）
func (t *Tracker) saveState() {
	state := persistedState{
		Current:       t.current,
		LastTimestamp: t.lastTimestamp.Unix(),
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(t.statePath, data, 0644); err != nil {
		if !t.saveErrLogged {
			slog.Warn("トリップ状態保存失敗", "path", t.statePath, "error", err)
			t.saveErrLogged = true
		}
	}
}

// loadState は前回保存したトリップ状態をJSONファイルから復元する
func (t *Tracker) loadState() {
	data, err := os.ReadFile(t.statePath)
	if err != nil {
		return
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return
	}

	t.current = state.Current
	if state.LastTimestamp > 0 {
		t.lastTimestamp = time.Unix(state.LastTimestamp, 0)
	}

	if t.current.DistanceKm > 0 {
		fmt.Printf("前回のトリップ状態を復元: %.1f km\n", t.current.DistanceKm)
	}
}
