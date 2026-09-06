package trip

import (
	"encoding/json"
	"os"
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

// --- SetDistance ---

func TestTrackerSetDistance(t *testing.T) {
	tr := newTestTracker(t)
	feedWithFuel(tr, 60, 6.0, 20)

	origDist := tr.DistanceKm()
	origFuel := tr.GetCurrent().FuelConsumptionL
	if origDist <= 0 {
		t.Fatal("expected positive distance before SetDistance")
	}

	// 距離を半分に補正
	tr.SetDistance(origDist / 2)

	if tr.DistanceKm() != origDist/2 {
		t.Errorf("DistanceKm: got %.6f, want %.6f", tr.DistanceKm(), origDist/2)
	}

	// 燃料消費量も半分に補正される
	newFuel := tr.GetCurrent().FuelConsumptionL
	expectedFuel := origFuel / 2
	diff := newFuel - expectedFuel
	if diff < 0 {
		diff = -diff
	}
	if diff > expectedFuel*0.01 {
		t.Errorf("FuelConsumptionL: got %.6f, want ~%.6f", newFuel, expectedFuel)
	}
}

func TestTrackerSetDistance_Zero(t *testing.T) {
	tr := newTestTracker(t)
	feed(tr, 60, 20)

	tr.SetDistance(0)
	if tr.DistanceKm() != 0 {
		t.Errorf("DistanceKm: got %.6f, want 0", tr.DistanceKm())
	}
}

func TestTrackerSetDistance_Negative(t *testing.T) {
	tr := newTestTracker(t)
	feed(tr, 60, 20)

	tr.SetDistance(-100)
	if tr.DistanceKm() != 0 {
		t.Errorf("negative should clamp to 0: got %.6f", tr.DistanceKm())
	}
}

func TestTrackerSetDistance_Increase(t *testing.T) {
	tr := newTestTracker(t)
	feed(tr, 60, 10)

	tr.SetDistance(500)
	if tr.DistanceKm() != 500 {
		t.Errorf("DistanceKm: got %.6f, want 500", tr.DistanceKm())
	}
}

func TestTrackerSetDistance_Persistence(t *testing.T) {
	dir := t.TempDir()
	statePath := dir + "/trip_state.json"

	tr1 := NewTracker(TrackerConfig{StatePath: statePath})
	feed(tr1, 60, 20)
	tr1.SetDistance(123.4)

	// 新しいトラッカーで復元
	tr2 := NewTracker(TrackerConfig{StatePath: statePath})
	diff := tr2.DistanceKm() - 123.4
	if diff < 0 {
		diff = -diff
	}
	if diff > 0.01 {
		t.Errorf("restored DistanceKm: got %.1f, want 123.4", tr2.DistanceKm())
	}
}

func TestTrackerDistanceKm(t *testing.T) {
	tr := newTestTracker(t)
	if tr.DistanceKm() != 0 {
		t.Errorf("initial DistanceKm: got %.6f, want 0", tr.DistanceKm())
	}

	feed(tr, 60, 20)
	if tr.DistanceKm() <= 0 {
		t.Error("expected positive DistanceKm after driving")
	}

	// GetCurrent と一致する
	cur := tr.GetCurrent()
	if tr.DistanceKm() != cur.DistanceKm {
		t.Errorf("DistanceKm() and GetCurrent().DistanceKm differ: %.6f vs %.6f",
			tr.DistanceKm(), cur.DistanceKm)
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

	// トラッカー1: データを蓄積して強制保存
	tr1 := NewTracker(TrackerConfig{StatePath: statePath})
	feed(tr1, 60, 65)
	tr1.SaveState()

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
	// feedWithFuel で 0.05L の閾値を超えるには 600L/h のような
	// 非現実的なレートが要り、その結果 (0.1km/L) は妥当性の門番に
	// 弾かれる。ここでは現実的な値を直接置いて比だけを確かめる。
	tr := newTestTracker(t)
	tr.current.DistanceKm = 100
	tr.current.FuelConsumptionL = 10

	if avg := tr.AvgFuelEconomy(); avg < 9.9 || avg > 10.1 {
		t.Errorf("expected 10 km/L, got %.3f", avg)
	}
}

// TestTrackerAvgFuelEconomy_Implausible は 2026-09-06 の障害の再現。
//
// SSD 未マウントで状態が読めず距離0/燃料0から始まり、GAS 復元が距離だけを
// 98.8km にした。その後の走行で 0.741L だけ積算され 133km/L となり、
// 航続可能距離が 46L × 133 = 6,114km と表示された。範囲外は 0 を返す。
func TestTrackerAvgFuelEconomy_Implausible(t *testing.T) {
	tr := newTestTracker(t)
	tr.current.DistanceKm = 98.8
	tr.current.FuelConsumptionL = 0.741 // 133 km/L

	if avg := tr.AvgFuelEconomy(); avg != 0 {
		t.Errorf("ありえない燃費は 0 を返すべき: %.3f", avg)
	}

	tr.current.FuelConsumptionL = 98.8 // 1 km/L も同様に棄却する
	if avg := tr.AvgFuelEconomy(); avg != 0 {
		t.Errorf("低すぎる燃費も 0 を返すべき: %.3f", avg)
	}
}

// TestTrackerAvgFuelEconomy_FuelInvalid は、燃料の実測が無い区間で
// 平均燃費を返さないことを確かめる。
func TestTrackerAvgFuelEconomy_FuelInvalid(t *testing.T) {
	tr := newTestTracker(t)
	tr.current.DistanceKm = 100
	tr.current.FuelConsumptionL = 10
	tr.current.FuelInvalid = true

	if avg := tr.AvgFuelEconomy(); avg != 0 {
		t.Errorf("燃料が較正に使えない区間は 0 を返すべき: %.3f", avg)
	}
}

// TestRestoreDistanceIfEmpty_Empty は実測が無いとき距離が入ることを確かめる。
func TestRestoreDistanceIfEmpty_Empty(t *testing.T) {
	tr := newTestTracker(t)

	if !tr.RestoreDistanceIfEmpty(58.13) {
		t.Fatal("実測が無いのに復元されなかった")
	}
	if got := tr.DistanceKm(); got != 58.13 {
		t.Errorf("距離 58.13 を期待、got %.2f", got)
	}
	if !tr.GetCurrent().FuelInvalid {
		t.Error("燃料を伴わない距離には FuelInvalid が立つべき")
	}
	if avg := tr.AvgFuelEconomy(); avg != 0 {
		t.Errorf("燃料が無いので平均燃費は 0 のはず: %.3f", avg)
	}
}

// TestRestoreDistanceIfEmpty_KeepsMeasured は 2026-09-06 の較正破壊の再現。
//
// 実測があるのに GAS のオドメーター由来の距離で上書きすると、SetDistance が
// 燃料も同じ比率で書き換える。障害対応で約20回再起動したため、給油〜給油の
// 燃料積算が20回歪んだ。実測がある限り触らないことを確かめる。
func TestRestoreDistanceIfEmpty_KeepsMeasured(t *testing.T) {
	tr := newTestTracker(t)
	tr.current.DistanceKm = 94.56
	tr.current.FuelConsumptionL = 6.726

	if tr.RestoreDistanceIfEmpty(58.13) {
		t.Fatal("実測があるのに上書きされた")
	}
	if got := tr.DistanceKm(); got != 94.56 {
		t.Errorf("距離が変わった: %.2f", got)
	}
	if got := tr.GetCurrent().FuelConsumptionL; got != 6.726 {
		t.Errorf("燃料が変わった: %.4f", got)
	}
}

// TestDegraded_NeverSaves は、保存先が使えないときに書き込まないことを確かめる。
//
// 2026-09-06 は SSD 未マウントのまま起動し、ゼロから数え直した値を
// SSD 復帰後に正しい記録の上へ保存して失った。
func TestDegraded_NeverSaves(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir", "trip_state.json")
	tr := NewTracker(TrackerConfig{StatePath: missing})

	if !tr.degraded {
		t.Fatal("保存先が無いのに degraded になっていない")
	}

	tr.SetDistance(123.4)
	if got := tr.DistanceKm(); got != 0 {
		t.Errorf("degraded 中は距離補正を無視すべき: %.2f", got)
	}
	if tr.RestoreDistanceIfEmpty(58.13) {
		t.Error("degraded 中は GAS 復元も受け付けないべき")
	}

	feedWithFuel(tr, 60, 6.0, 20)
	tr.SaveState()
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("degraded 中にファイルを作ってはいけない: %v", err)
	}
}

// TestSaveTriggers_Idle は、停車中でも燃料の積算が保存されることを確かめる。
//
// 従来は距離 0.1km だけが引き金だった。アイドリングは燃料を使うのに距離が
// 増えないため、停車中の燃料は保存されず電源断で失われていた。
func TestSaveTriggers_Idle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trip_state.json")
	tr := NewTracker(TrackerConfig{StatePath: path})

	// 速度0・燃料レートありで回す。距離は増えない。
	feedWithFuel(tr, 0, 3600.0, 30)

	if tr.GetCurrent().FuelConsumptionL <= 0 {
		t.Fatal("燃料が積算されていない")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("停車中でも保存されるべき: %v", err)
	}
	var st persistedState
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("保存内容が壊れている: %v", err)
	}
	if st.Current.FuelConsumptionL <= 0 {
		t.Errorf("保存に燃料が入っていない: %.4f", st.Current.FuelConsumptionL)
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
	feedWithFuel(tr1, 60, 6.0, 65)
	tr1.SaveState()

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

// TestUpdateWithPulse_PrefersPulse は距離パルスがある場合に
// 車速の積分ではなくパルスの差分が使われることを検証する。
func TestUpdateWithPulse_PrefersPulse(t *testing.T) {
	tr := newTestTracker(t)

	// 初回は基準値の記録のみ
	tr.UpdateWithPulse(60, 0, 10.000, true)
	time.Sleep(120 * time.Millisecond)

	// 車速は60km/hだがパルスは 10.000 → 10.001 km しか進んでいない。
	// パルス側が採用されれば、距離は 0.001km になる。
	tr.UpdateWithPulse(60, 0, 10.001, true)

	got := tr.DistanceKm()
	if got < 0.0009 || got > 0.0011 {
		t.Errorf("距離 = %.6f km、パルス差分 0.001km が使われていない", got)
	}
}

// TestUpdateWithPulse_FallsBackToSpeed は
// パルスが無効な場合に車速の積分に退避することを検証する。
func TestUpdateWithPulse_FallsBackToSpeed(t *testing.T) {
	tr := newTestTracker(t)

	tr.UpdateWithPulse(36, 0, 0, false)
	time.Sleep(200 * time.Millisecond)
	tr.UpdateWithPulse(36, 0, 0, false)

	// 36km/h = 0.01km/s。0.2秒で 0.002km 前後になるはず。
	got := tr.DistanceKm()
	if got <= 0 {
		t.Errorf("距離が積算されていない: %.6f", got)
	}
	if got > 0.01 {
		t.Errorf("距離が過大: %.6f", got)
	}
}

// TestUpdateWithPulse_RejectsJump は通信断からの復帰などで
// 累積値が飛んだ場合に、その差分を採用しないことを検証する。
func TestUpdateWithPulse_RejectsJump(t *testing.T) {
	tr := newTestTracker(t)

	tr.UpdateWithPulse(0, 0, 100.0, true)
	time.Sleep(120 * time.Millisecond)

	// 0.12秒で 50km 進むことはありえない → 車速積分(0km/h)に退避する
	tr.UpdateWithPulse(0, 0, 150.0, true)

	got := tr.DistanceKm()
	if got > 0.001 {
		t.Errorf("異常な差分を採用した: %.6f km", got)
	}
}

// 距離パルスは 10Hz で更新されるのに UpdateWithPulse は 20Hz で呼ばれる。
// 上限判定を呼び出し側の dt で行うと、100km/h 付近から正しい差分を棄却して
// 距離を半分しか数えなくなる。
//
// 2026-08 の実走 790km で、トリップがオドメーター比 -17.6% になり、
// 速度が上がるほど悪化した (低速 0.984 → 高速 0.602)。
func TestTracker_PulseFasterConsumerThanProducer(t *testing.T) {
	const (
		pulseHz = 10.0
		pollMs  = 50.0
		hours   = 0.25
	)
	for _, speed := range []float64{40, 80, 100, 120, 140} {
		dir := t.TempDir()
		tr := NewTracker(TrackerConfig{StatePath: filepath.Join(dir, "trip.json")})

		dt := pollMs / 1000.0
		pulsePeriod := 1.0 / pulseHz
		cumPulse := 0.0
		nextPulse := 0.0
		steps := int(hours * 3600 / dt)

		for i := 0; i < steps; i++ {
			now := float64(i) * dt
			if now >= nextPulse {
				cumPulse += speed / 3600.0 * pulsePeriod
				nextPulse += pulsePeriod
			}
			tr.updateWithPulseAt(speed, 0, cumPulse, true,
				time.Unix(0, 0).Add(time.Duration(now*float64(time.Second))))
		}

		want := speed * hours
		got := tr.DistanceKm()
		ratio := got / want
		if ratio < 0.97 || ratio > 1.03 {
			t.Errorf("%.0f km/h: 距離 %.2f km, 真値 %.2f km (比 %.3f)。"+
				"上限判定が呼び出し側の dt を使っていないか確認すること",
				speed, got, want, ratio)
		}
	}
}
