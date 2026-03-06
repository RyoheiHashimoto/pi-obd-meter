package trip

import (
	"encoding/json"
	"fmt"
	"log"
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

	// リセット検知
	prevDistanceKm float64
	resetThreshold float64 // km - この距離以下になったらリセットと判定

	// 燃料状態（給油検出用）
	lastFuelPct      float64 // 直近のタンク残量%
	tripStartFuelPct float64 // 現トリップ開始時のタンク残量%
	fuelStateValid   bool    // 燃料状態が有効か

	// 永続化パス
	statePath     string
	saveErrLogged bool // 書き込みエラーを既にログ出力したか

	// トリップ完了コールバック
	onTripComplete func(TripData)
}

// TrackerConfig はトラッカーの設定
type TrackerConfig struct {
	ResetThresholdKm float64 // リセット検知閾値 (default: 0.5km)
	StatePath        string  // 状態保存パス
	OnTripComplete   func(TripData)
}

// NewTracker は新しいトラッカーを作成する
func NewTracker(cfg TrackerConfig) *Tracker {
	if cfg.ResetThresholdKm <= 0 {
		cfg.ResetThresholdKm = 0.5
	}
	if cfg.StatePath == "" {
		cfg.StatePath = "/var/lib/pi-obd-meter/trip_state.json"
	}

	t := &Tracker{
		resetThreshold: cfg.ResetThresholdKm,
		statePath:      cfg.StatePath,
		onTripComplete: cfg.OnTripComplete,
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

// CheckReset はトリップリセットを検知する
func (t *Tracker) CheckReset() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.prevDistanceKm > 5.0 && t.current.DistanceKm < t.resetThreshold {
		return true
	}
	return false
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

	// コールバック
	if t.onTripComplete != nil {
		go t.onTripComplete(completed)
	}

	// 新しいトリップを開始
	t.prevDistanceKm = t.current.DistanceKm
	t.current = TripData{
		TripID:    fmt.Sprintf("trip_%d", time.Now().Unix()),
		StartTime: time.Now(),
	}
	t.speedSum = 0
	t.lastTimestamp = time.Time{}

	t.saveState()

	return &completed
}

// GetCurrent は現在のトリップデータを取得する
func (t *Tracker) GetCurrent() TripData {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.current
}

// --- 燃料状態管理（給油検出用） ---

// UpdateFuelLevel は最新のタンク残量を記録する（フルサイクルごとに呼ぶ）
func (t *Tracker) UpdateFuelLevel(pct float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.lastFuelPct = pct
	if !t.fuelStateValid {
		t.fuelStateValid = true
		t.tripStartFuelPct = pct
	}
}

// GetFuelState は起動時の給油検出用に燃料状態を返す
func (t *Tracker) GetFuelState() (tripStartPct, lastPct float64, valid bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tripStartFuelPct, t.lastFuelPct, t.fuelStateValid
}

// ResetFuelBaseline は給油後にトリップ開始時の燃料レベルをリセットする
func (t *Tracker) ResetFuelBaseline(pct float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.tripStartFuelPct = pct
	t.lastFuelPct = pct
	t.fuelStateValid = true
	t.saveState()
}

// --- 永続化 ---

type persistedState struct {
	Current          TripData `json:"current"`
	PrevDistance     float64  `json:"prev_distance"`
	LastTimestamp    int64    `json:"last_timestamp"`
	LastFuelPct      float64  `json:"last_fuel_pct"`
	TripStartFuelPct float64  `json:"trip_start_fuel_pct"`
	FuelStateValid   bool     `json:"fuel_state_valid"`
}

func (t *Tracker) saveState() {
	state := persistedState{
		Current:          t.current,
		PrevDistance:     t.prevDistanceKm,
		LastTimestamp:    t.lastTimestamp.Unix(),
		LastFuelPct:      t.lastFuelPct,
		TripStartFuelPct: t.tripStartFuelPct,
		FuelStateValid:   t.fuelStateValid,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(t.statePath, data, 0644); err != nil {
		if !t.saveErrLogged {
			log.Printf("trip state save failed (overlayFS?): %v", err)
			t.saveErrLogged = true
		}
	}
}

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
	t.prevDistanceKm = state.PrevDistance
	if state.LastTimestamp > 0 {
		t.lastTimestamp = time.Unix(state.LastTimestamp, 0)
	}
	t.lastFuelPct = state.LastFuelPct
	t.tripStartFuelPct = state.TripStartFuelPct
	t.fuelStateValid = state.FuelStateValid

	if t.current.DistanceKm > 0 {
		fmt.Printf("前回のトリップ状態を復元: %.1f km\n", t.current.DistanceKm)
	}
}
