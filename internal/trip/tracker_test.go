package trip

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestTracker(t *testing.T, opts ...func(*TrackerConfig)) *Tracker {
	t.Helper()
	cfg := TrackerConfig{
		StatePath: filepath.Join(t.TempDir(), "trip_state.json"),
	}
	for _, o := range opts {
		o(&cfg)
	}
	return NewTracker(cfg)
}

// feed は time.Sleep で dt を確保しつつ Update を呼ぶ（fuelRateLH=0）
func feed(tr *Tracker, speed float64, n int) {
	for i := 0; i < n; i++ {
		tr.Update(speed, 0)
		time.Sleep(15 * time.Millisecond)
	}
}

// feedWithFuel は燃料消費レート付きで Update を呼ぶ
func feedWithFuel(tr *Tracker, speed, fuelRateLH float64, n int) {
	for i := 0; i < n; i++ {
		tr.Update(speed, fuelRateLH)
		time.Sleep(15 * time.Millisecond)
	}
}

// --- Update ---

func TestTrackerUpdate_FirstCall(t *testing.T) {
	tr := newTestTracker(t)
	tr.Update(60, 0)
	cur := tr.GetCurrent()
	if cur.Samples != 0 {
		t.Errorf("first call should not increment Samples, got %d", cur.Samples)
	}
	if cur.StartTime.IsZero() {
		t.Error("StartTime should be set after first call")
	}
	if cur.TripID == "" {
		t.Error("TripID should be set after first call")
	}
}

func TestTrackerUpdate_Accumulation(t *testing.T) {
	tr := newTestTracker(t)
	feed(tr, 60, 20)

	cur := tr.GetCurrent()
	if cur.DistanceKm <= 0 {
		t.Errorf("expected positive distance, got %.6f", cur.DistanceKm)
	}
	if cur.Samples == 0 {
		t.Error("expected samples > 0")
	}
}

func TestTrackerUpdate_IdleVsDriving(t *testing.T) {
	tr := newTestTracker(t)

	// Drive
	feed(tr, 60, 5)
	cur := tr.GetCurrent()
	if cur.DrivingTimeSec <= 0 {
		t.Error("expected positive DrivingTimeSec for speed > 1")
	}

	// Idle
	tr2 := newTestTracker(t)
	feed(tr2, 0.5, 5)
	cur2 := tr2.GetCurrent()
	if cur2.IdleTimeSec <= 0 {
		t.Error("expected positive IdleTimeSec for speed <= 1")
	}
	if cur2.DrivingTimeSec > 0 {
		t.Error("expected zero DrivingTimeSec for idle")
	}
}

func TestTrackerUpdate_MaxSpeed(t *testing.T) {
	tr := newTestTracker(t)
	feed(tr, 40, 3)
	feed(tr, 100, 3)
	feed(tr, 60, 3)

	cur := tr.GetCurrent()
	if cur.MaxSpeedKmh != 100 {
		t.Errorf("expected MaxSpeedKmh=100, got %.1f", cur.MaxSpeedKmh)
	}
}

// --- ManualReset ---

func TestTrackerManualReset(t *testing.T) {
	tr := newTestTracker(t)
	feed(tr, 60, 10)

	completed := tr.ManualReset()
	if completed == nil {
		t.Fatal("expected completed TripData, got nil")
	}
	if completed.DistanceKm <= 0 {
		t.Error("completed trip should have positive distance")
	}
	if completed.EndTime.IsZero() {
		t.Error("EndTime should be set")
	}

	// After reset, GetCurrent should be a fresh trip
	cur := tr.GetCurrent()
	if cur.Samples != 0 {
		t.Errorf("expected 0 samples after reset, got %d", cur.Samples)
	}
	if cur.DistanceKm != 0 {
		t.Errorf("expected 0 distance after reset, got %.6f", cur.DistanceKm)
	}
}

func TestTrackerManualReset_NoData(t *testing.T) {
	tr := newTestTracker(t)
	completed := tr.ManualReset()
	if completed != nil {
		t.Errorf("expected nil for empty trip, got %+v", completed)
	}
}

func TestTrackerManualReset_AvgSpeed(t *testing.T) {
	tr := newTestTracker(t)
	feed(tr, 60, 20)

	completed := tr.ManualReset()
	if completed == nil {
		t.Fatal("expected completed TripData")
	}
	if completed.AvgSpeedKmh <= 0 {
		t.Error("expected positive AvgSpeedKmh")
	}
}

// --- Persistence ---

func TestTrackerPersistence(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "trip_state.json")

	// トラッカー1: データを蓄積
	tr1 := NewTracker(TrackerConfig{StatePath: statePath})
	feed(tr1, 60, 65) // 60超でsaveStateが呼ばれる（Samples%60==0）

	cur1 := tr1.GetCurrent()

	// トラッカー2: 同じパスから復元
	tr2 := NewTracker(TrackerConfig{StatePath: statePath})
	cur2 := tr2.GetCurrent()

	if cur2.DistanceKm == 0 {
		t.Error("expected restored distance > 0")
	}
	diff := cur1.DistanceKm - cur2.DistanceKm
	if diff < 0 {
		diff = -diff
	}
	if diff > cur1.DistanceKm*0.5 {
		t.Errorf("restored distance too different: original=%.6f, restored=%.6f", cur1.DistanceKm, cur2.DistanceKm)
	}
}

// --- FuelConsumption ---

func TestTrackerFuelAccumulation(t *testing.T) {
	tr := newTestTracker(t)
	// 60 km/h、燃料消費 6 L/h で走行
	feedWithFuel(tr, 60, 6.0, 20)

	cur := tr.GetCurrent()
	if cur.FuelConsumptionL <= 0 {
		t.Errorf("expected positive fuel consumption, got %.6f", cur.FuelConsumptionL)
	}
	if cur.DistanceKm <= 0 {
		t.Error("expected positive distance")
	}
}

func TestTrackerAvgFuelEconomy(t *testing.T) {
	tr := newTestTracker(t)
	// 60 km/h、600 L/h（大きいレートで閾値を素早く超える）
	// 600/3600*0.015 ≈ 0.0025 L/tick → 4回で 0.01L を超える
	// 期待平均燃費 = 60/600 = 0.1 km/L
	feedWithFuel(tr, 60, 600.0, 20)

	avg := tr.AvgFuelEconomy()
	if avg < 0.05 || avg > 0.5 {
		t.Errorf("expected avg fuel economy around 0.1 km/L, got %.3f", avg)
	}
}

func TestTrackerAvgFuelEconomy_NoFuel(t *testing.T) {
	tr := newTestTracker(t)
	feed(tr, 60, 10) // fuelRateLH=0

	avg := tr.AvgFuelEconomy()
	if avg != 0 {
		t.Errorf("expected 0 avg fuel economy with no fuel data, got %.1f", avg)
	}
}

func TestTrackerManualReset_FuelConsumption(t *testing.T) {
	tr := newTestTracker(t)
	feedWithFuel(tr, 60, 6.0, 20)

	completed := tr.ManualReset()
	if completed == nil {
		t.Fatal("expected completed TripData")
	}
	if completed.FuelConsumptionL <= 0 {
		t.Errorf("completed trip should have positive fuel consumption, got %.6f", completed.FuelConsumptionL)
	}

	// リセット後は0
	cur := tr.GetCurrent()
	if cur.FuelConsumptionL != 0 {
		t.Errorf("expected 0 fuel consumption after reset, got %.6f", cur.FuelConsumptionL)
	}
}

func TestTrackerPersistence_FuelConsumption(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "trip_state.json")

	tr1 := NewTracker(TrackerConfig{StatePath: statePath})
	feedWithFuel(tr1, 60, 6.0, 65) // 60超でsaveState

	cur1 := tr1.GetCurrent()

	tr2 := NewTracker(TrackerConfig{StatePath: statePath})
	cur2 := tr2.GetCurrent()

	if cur2.FuelConsumptionL == 0 {
		t.Error("expected restored fuel consumption > 0")
	}
	diff := cur1.FuelConsumptionL - cur2.FuelConsumptionL
	if diff < 0 {
		diff = -diff
	}
	if diff > cur1.FuelConsumptionL*0.5 {
		t.Errorf("restored fuel consumption too different: original=%.6f, restored=%.6f",
			cur1.FuelConsumptionL, cur2.FuelConsumptionL)
	}
}
