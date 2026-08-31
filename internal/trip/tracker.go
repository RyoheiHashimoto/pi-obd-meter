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

	"github.com/hashimoto/pi-obd-meter/internal/atomicfile"
)

const (
	// maxPlausibleSpeedKmh は距離パルスの差分を受け入れる上限速度。
	// これを超える見かけの速度は、通信断からの復帰で累積値が飛んだ場合しか
	// 起こらない。
	maxPlausibleSpeedKmh = 200.0

	// pulseStaleSec はパルス更新が途絶えたとみなす時間。
	//
	// 0x420 は 10Hz で来る (実測: 周期 中央100.0ms / p95 100.2ms / 欠測ゼロ)。
	// 更新が来ない間は距離を 0 として次回にまとめて入れるが、本当に途絶えた
	// 場合まで 0 を積み続けると距離を落とす。これを超えたら車速積分に戻す。
	pulseStaleSec = 1.0
)

// TripData は1トリップ分の集計データ
type TripData struct {
	TripID           string    `json:"trip_id"`
	StartTime        time.Time `json:"start_time"`
	EndTime          time.Time `json:"end_time"`
	DistanceKm       float64   `json:"distance_km"`
	FuelConsumptionL float64   `json:"fuel_consumption_l"` // 給油間の燃料消費量 (L)
	MaxSpeedKmh      float64   `json:"max_speed_kmh"`
	AvgSpeedKmh      float64   `json:"avg_speed_kmh"`
	DrivingTimeSec   float64   `json:"driving_time_sec"`
	IdleTimeSec      float64   `json:"idle_time_sec"`
	Samples          int       `json:"samples"`
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
	saveErrLogged bool    // 書き込みエラーを既にログ出力したか
	lastSavedKm   float64 // 最後に保存した時点の走行距離

	// 距離パルスによる累積距離の前回値。差分を取って距離に加算する。
	lastPulseKm    float64
	lastPulseValid bool

	// パルス値が最後に「変化した」時刻。
	//
	// 上限判定をこの間隔で行う。呼び出し側の dt で判定してはいけない。
	// パルスは 10Hz で更新されるのに UpdateWithPulse は 20Hz で呼ばれる
	// ため、変化した回の差分は 100ms 分の距離を持つのに dt は 50ms しか
	// 無い。dt を基準にすると 100km/h 付近から正しい差分を弾き始める。
	lastPulseAt time.Time
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
// fuelRateLH は燃料消費レート (L/h)。0以下の場合は積算しない。
//
// 距離は車速の積分で求める。距離パルスが使える場合は UpdateWithPulse を使うこと。
func (t *Tracker) Update(speedKmh, fuelRateLH float64) {
	t.UpdateWithPulse(speedKmh, fuelRateLH, 0, false)
}

// UpdateWithPulse は距離パルスによる累積距離を使ってトリップを更新する。
//
// pulseKm は起動からの累積距離 (km)、pulseValid はその値が信頼できるか。
// 前回値との差分を距離に加算するため、車速の積分と違って誤差が蓄積しない。
//
//	車速積分   実測で -0.25% の系統誤差
//	パルス計数 実測で ±0.02% (10km境界17区間)
//
// pulseValid が false の場合、または差分が負・過大な場合は車速積分に退避する。
// 通信断からの復帰直後など、累積値が飛ぶ可能性があるため。
func (t *Tracker) UpdateWithPulse(speedKmh, fuelRateLH, pulseKm float64, pulseValid bool) {
	t.updateWithPulseAt(speedKmh, fuelRateLH, pulseKm, pulseValid, time.Now())
}

// updateWithPulseAt は時刻を外から与える版。テストで生産10Hz・消費20Hz の
// ずれを再現するために要る。実時間に依存すると、その条件を確実には作れない。
func (t *Tracker) updateWithPulseAt(speedKmh, fuelRateLH, pulseKm float64, pulseValid bool, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// 初回
	if t.lastTimestamp.IsZero() {
		t.lastTimestamp = now
		t.current.StartTime = now
		t.current.TripID = fmt.Sprintf("trip_%d", now.Unix())
		// パルスの基準値もここで押さえる。次回から差分が取れる。
		if pulseValid {
			t.lastPulseKm = pulseKm
			t.lastPulseValid = true
			t.lastPulseAt = now
		}
		return
	}

	dt := now.Sub(t.lastTimestamp).Seconds()
	if dt <= 0 || dt > 10 { // 10秒以上の空白はスキップ（接続断等）
		t.lastTimestamp = now
		return
	}

	// 走行距離: 距離パルスがあれば計数、無ければ車速の積分にフォールバック
	//
	// 上限判定は「パルスが前回変化してからの経過時間」で行う。呼び出し側の
	// dt を使ってはいけない。パルス (CAN 0x420 B1) は 10Hz で更新されるのに
	// UpdateWithPulse は poll_interval_ms (既定50ms) 周期で呼ばれるため、
	// 変化した回の差分は 100ms 分の距離を持つのに dt は 50ms しかない。
	//
	// この取り違えで 2026-08 に実害が出た。790km の照合でトリップが
	// オドメーター比 -17.6% になり、しかも速度が上がるほど悪化した
	// (低速 0.984 → 高速 0.602)。100km/h を超えると正しい差分が上限を
	// 超えて棄却され、車速積分 (dt=50ms 分) にフォールバックするため、
	// 実際に進んだ 100ms 分の半分しか数えていなかった。
	distanceDelta := (speedKmh / 3600.0) * dt
	if pulseValid && t.lastPulseValid {
		d := pulseKm - t.lastPulseKm
		switch {
		case d > 0:
			elapsed := now.Sub(t.lastPulseAt).Seconds()
			if elapsed <= 0 {
				elapsed = dt
			}
			// 通信断からの復帰で累積値が飛んだ場合だけ弾く。
			if d <= (maxPlausibleSpeedKmh/3600.0)*elapsed {
				distanceDelta = d
			}
			t.lastPulseAt = now
		case d == 0:
			// まだ次のパルス更新が来ていない。進んだ分は次回まとめて入る。
			//
			// ただし更新が止まったまま走り続けている場合は距離を落とす。
			// 一定時間変化が無ければ車速積分に戻す。
			if now.Sub(t.lastPulseAt).Seconds() < pulseStaleSec {
				distanceDelta = 0
			}
		default:
			// 負の差分 = 累積値のリセット。車速積分を使い、基準を取り直す。
			t.lastPulseAt = now
		}
	}
	if pulseValid {
		t.lastPulseKm = pulseKm
		if !t.lastPulseValid {
			t.lastPulseAt = now
		}
		t.lastPulseValid = true
	} else {
		t.lastPulseValid = false
	}
	t.current.DistanceKm += distanceDelta

	// 燃料消費量を積算 (L)
	if fuelRateLH > 0 {
		t.current.FuelConsumptionL += (fuelRateLH / 3600.0) * dt
	}

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

	// 距離ベースで状態を保存（0.1km=100mごと）
	if t.current.DistanceKm-t.lastSavedKm >= 0.1 {
		t.saveState()
		t.lastSavedKm = t.current.DistanceKm
	}
}

// ManualReset はトリップを手動リセットする
func (t *Tracker) ManualReset() *TripData {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.finalize()
}

// SetDistance はトリップ走行距離を指定値に補正する（GASダッシュボードからの補正用）
// 燃料消費量も距離の比率に応じて補正する。
func (t *Tracker) SetDistance(km float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if km < 0 {
		km = 0
	}

	// 燃料消費量を距離の比率で補正
	if t.current.DistanceKm > 0 {
		ratio := km / t.current.DistanceKm
		t.current.FuelConsumptionL *= ratio
	}

	t.current.DistanceKm = km
	t.lastSavedKm = km
	t.saveState()
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

// AvgFuelEconomy は給油間の平均燃費 (km/L) を返す。
// データ不足の場合は 0 を返す。
func (t *Tracker) AvgFuelEconomy() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current.FuelConsumptionL < 0.05 {
		return 0
	}
	return t.current.DistanceKm / t.current.FuelConsumptionL
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

// saveState は現在のトリップ状態をJSONファイルにアトミックに書き出す。
// 一時ファイルに書き込んでからrenameすることで、電源断時にファイルが壊れるのを防ぐ。
func (t *Tracker) saveState() {
	state := persistedState{
		Current:       t.current,
		LastTimestamp: t.lastTimestamp.Unix(),
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	if err := atomicfile.Write(t.statePath, data, 0644); err != nil {
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
	t.lastSavedKm = t.current.DistanceKm
	if state.LastTimestamp > 0 {
		t.lastTimestamp = time.Unix(state.LastTimestamp, 0)
	}

	if t.current.DistanceKm > 0 {
		fmt.Printf("前回のトリップ状態を復元: %.1f km\n", t.current.DistanceKm)
	}
}
