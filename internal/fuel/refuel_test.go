package fuel

import (
	"os"
	"path/filepath"
	"testing"
)

func feed(d *Detector, pt float64, n int) {
	for i := 0; i < n; i++ {
		d.Update(pt, true)
	}
}

// TestDetector_Refuel は 2026-08-20 の実測値で給油が検出されることを確認する。
// 8.6% (クリップ下限) から 95.3% (クリップ上限) へ 86.7ポイント跳躍した。
func TestDetector_Refuel(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fuel.json")

	// 前回停車時: 空に近い
	d1 := NewDetector(p)
	feed(d1, 8.6, settleSamples)
	if d1.Event() != nil {
		t.Fatal("初回起動で給油を誤検出した")
	}

	// 給油後に再起動
	d2 := NewDetector(p)
	feed(d2, 95.3, settleSamples)
	ev := d2.Event()
	if ev == nil {
		t.Fatal("給油が検出されなかった")
	}
	if diff := ev.DeltaPt - 86.7; diff > 0.1 || diff < -0.1 {
		t.Errorf("跳躍量 = %.1f, want 86.7", ev.DeltaPt)
	}
	if want := 86.7 * LitersPerPoint; ev.AmountL < want-0.1 || ev.AmountL > want+0.1 {
		t.Errorf("給油量 = %.1f L, want %.1f L", ev.AmountL, want)
	}
	if !ev.FullTank {
		t.Error("95.3%% は満タンと判定されるべき")
	}
}

// TestDetector_NoFalsePositive は起動をまたぐ通常のばらつきで誤検出しないことを確認する。
// 32回の起動境界を実測した結果、給油以外の最大変化は +3.5ポイントだった。
func TestDetector_NoFalsePositive(t *testing.T) {
	for _, jump := range []float64{3.5, 5.0, 9.9} {
		p := filepath.Join(t.TempDir(), "fuel.json")
		d1 := NewDetector(p)
		feed(d1, 50.0, settleSamples)

		d2 := NewDetector(p)
		feed(d2, 50.0+jump, settleSamples)
		if ev := d2.Event(); ev != nil {
			t.Errorf("+%.1fポイントで誤検出した (跳躍 %.1f)", jump, ev.DeltaPt)
		}
	}
}

// TestDetector_PartialRefuel は継ぎ足し給油も検出されることを確認する。
// 満タン判定は「条件」ではなく「フラグ」であり、満タンでなくても記録する。
func TestDetector_PartialRefuel(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fuel.json")
	d1 := NewDetector(p)
	feed(d1, 40.0, settleSamples)

	d2 := NewDetector(p)
	feed(d2, 70.0, settleSamples) // 30ポイント = 約15L
	ev := d2.Event()
	if ev == nil {
		t.Fatal("継ぎ足し給油が検出されなかった")
	}
	if ev.FullTank {
		t.Error("70%% は満タンではない")
	}
	if ev.AmountL < 15.0 || ev.AmountL > 15.5 {
		t.Errorf("給油量 = %.1f L, want 約15.3 L", ev.AmountL)
	}
}

// TestDetector_IgnoresMoving は走行中の値を平均に入れないことを確認する。
// 走行中はスロッシングで 24〜33ポイント振れるため、判定に使えない。
func TestDetector_IgnoresMoving(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fuel.json")
	d := NewDetector(p)
	for i := 0; i < settleSamples*2; i++ {
		d.Update(80.0, false) // 走行中
	}
	if d.Settled() {
		t.Error("走行中のサンプルで落ち着いたと判定した")
	}
}

// 保存値は「起動後の全平均」ではなく「直近の残量」でなければならない。
//
// 当初は累積平均だったため、走行中に燃料が減ると保存値が実際より高く出て、
// 次回給油時の跳躍が小さく算出されていた。
func TestDetector_BaselineIsRecentNotSessionAverage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.json")
	d := NewDetector(p)

	// 90pt で停車 (出発前)
	for i := 0; i < settleSamples; i++ {
		d.Update(90, true)
	}
	if got := d.current; got < 89.9 || got > 90.1 {
		t.Fatalf("出発前の値 = %.2f, want 90", got)
	}

	// 走って燃料を消費し、40pt で停車 (到着後)
	for i := 0; i < settleSamples; i++ {
		d.Update(40, true)
	}
	if got := d.current; got < 39.9 || got > 40.1 {
		t.Errorf("到着後の値 = %.2f, want 40 (累積平均なら65付近になってしまう)", got)
	}
}

// 停車中に毎サンプル保存すると SD を痛める。書き込みを間引くこと。
func TestDetector_DoesNotWriteEverySample(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.json")
	d := NewDetector(p)

	// 窓を埋めて最初の保存を起こす
	for i := 0; i < settleSamples; i++ {
		d.Update(50, true)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("最初の保存が行われていない: %v", err)
	}
	first := st.ModTime()

	// 50ms 周期で 10秒ぶん = 200回。値も変わらない。
	for i := 0; i < 200; i++ {
		d.Update(50, true)
	}
	st2, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !st2.ModTime().Equal(first) {
		t.Error("値が変わっていないのに再保存された。SDへの書き込みを間引けていない")
	}
}

// 窓が埋まるまでは判定も保存もしない。
func TestDetector_NeedsFullWindow(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.json")
	d := NewDetector(p)
	for i := 0; i < settleSamples-1; i++ {
		d.Update(50, true)
	}
	if d.Settled() {
		t.Error("窓が埋まる前に settled になっている")
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("窓が埋まる前に保存された")
	}
	d.Update(50, true)
	if !d.Settled() {
		t.Error("窓が埋まったのに settled にならない")
	}
}

// 走行中のサンプルは窓に入れない (スロッシングで振れるため)。
func TestDetector_IgnoresMovingSamplesInWindow(t *testing.T) {
	dir := t.TempDir()
	d := NewDetector(filepath.Join(dir, "s.json"))
	for i := 0; i < 100; i++ {
		d.Update(20, false) // 走行中の暴れた値
	}
	if d.Settled() {
		t.Fatal("走行中のサンプルで settled になった")
	}
	for i := 0; i < settleSamples; i++ {
		d.Update(50, true)
	}
	if got := d.current; got < 49.9 || got > 50.1 {
		t.Errorf("値 = %.2f, want 50 (走行中の20が混ざってはいけない)", got)
	}
}
