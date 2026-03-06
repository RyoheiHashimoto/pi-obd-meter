package trip

import (
	"path/filepath"
	"sync/atomic"
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

// feed は time.Sleep で dt を確保しつつ Update を呼ぶ
func feed(tr *Tracker, speed, fuel float64, n int) {
	for i := 0; i < n; i++ {
		tr.Update(speed, fuel)
		time.Sleep(15 * time.Millisecond)
	}
}

// --- Update ---

func TestTrackerUpdate_FirstCall(t *testing.T) {
	tr := newTestTracker(t)
	tr.Update(60, 3.0)
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
	feed(tr, 60, 3.0, 20) // 60km/h, 3.0L/h, 20 samples

	cur := tr.GetCurrent()
	if cur.DistanceKm <= 0 {
		t.Errorf("expected positive distance, got %.6f", cur.DistanceKm)
	}
	if cur.FuelUsedL <= 0 {
		t.Errorf("expected positive fuel used, got %.6f", cur.FuelUsedL)
	}
	if cur.Samples == 0 {
		t.Error("expected samples > 0")
	}
}

func TestTrackerUpdate_IdleVsDriving(t *testing.T) {
	tr := newTestTracker(t)

	// Drive
	feed(tr, 60, 3.0, 5)
	cur := tr.GetCurrent()
	if cur.DrivingTimeSec <= 0 {
		t.Error("expected positive DrivingTimeSec for speed > 1")
	}

	// Idle
	tr2 := newTestTracker(t)
	feed(tr2, 0.5, 1.0, 5) // speed <= 1.0 = idle
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
	feed(tr, 40, 2.0, 3)
	feed(tr, 100, 5.0, 3)
	feed(tr, 60, 3.0, 3)

	cur := tr.GetCurrent()
	if cur.MaxSpeedKmh != 100 {
		t.Errorf("expected MaxSpeedKmh=100, got %.1f", cur.MaxSpeedKmh)
	}
}

// --- ManualReset ---

func TestTrackerManualReset(t *testing.T) {
	tr := newTestTracker(t)
	feed(tr, 60, 3.0, 10)

	completed := tr.ManualReset()
	if completed == nil {
		t.Fatal("expected completed TripData, got nil")
	}
	if completed.DistanceKm <= 0 {
		t.Error("completed trip should have positive distance")
	}
	if completed.FuelUsedL <= 0 {
		t.Error("completed trip should have positive fuel")
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

func TestTrackerManualReset_AvgCalculations(t *testing.T) {
	tr := newTestTracker(t)
	feed(tr, 60, 3.0, 20)

	completed := tr.ManualReset()
	if completed == nil {
		t.Fatal("expected completed TripData")
	}
	if completed.AvgFuelEconKm <= 0 {
		t.Error("expected positive AvgFuelEconKm")
	}
	if completed.AvgSpeedKmh <= 0 {
		t.Error("expected positive AvgSpeedKmh")
	}
}

// --- Callback ---

func TestTrackerOnTripComplete(t *testing.T) {
	var called int32
	tr := newTestTracker(t, func(cfg *TrackerConfig) {
		cfg.OnTripComplete = func(data TripData) {
			atomic.StoreInt32(&called, 1)
		}
	})

	feed(tr, 60, 3.0, 5)
	tr.ManualReset()

	// コールバックは goroutine で呼ばれるので少し待つ
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&called) != 1 {
		t.Error("OnTripComplete callback was not called")
	}
}

// --- Persistence ---

func TestTrackerPersistence(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "trip_state.json")

	// トラッカー1: データを蓄積
	tr1 := NewTracker(TrackerConfig{StatePath: statePath})
	feed(tr1, 60, 3.0, 65) // 60超でsaveStateが呼ばれる（Samples%60==0）

	cur1 := tr1.GetCurrent()

	// トラッカー2: 同じパスから復元
	tr2 := NewTracker(TrackerConfig{StatePath: statePath})
	cur2 := tr2.GetCurrent()

	if cur2.DistanceKm == 0 {
		t.Error("expected restored distance > 0")
	}
	// 復元値は完全一致ではないが近い値であるべき
	diff := cur1.DistanceKm - cur2.DistanceKm
	if diff < 0 {
		diff = -diff
	}
	// saveState は Samples%60==0 でのみ保存。最大59サンプル分のずれを許容
	if diff > cur1.DistanceKm*0.5 {
		t.Errorf("restored distance too different: original=%.6f, restored=%.6f", cur1.DistanceKm, cur2.DistanceKm)
	}
}
