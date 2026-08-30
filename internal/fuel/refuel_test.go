package fuel

import (
	"math"
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
	// 満タンなので給油量は出さない。センダーが上限に張り付いており、
	// 給油後の残量が実際どこまで行ったか分からないため根拠が無い。
	if ev.AmountL != 0 {
		t.Errorf("満タンなのに給油量 %.1f L を出した", ev.AmountL)
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
	feed(d2, 70.0, settleSamples) // 30ポイントの継ぎ足し
	ev := d2.Event()
	if ev == nil {
		t.Fatal("継ぎ足し給油が検出されなかった")
	}
	if ev.FullTank {
		t.Error("70%% は満タンではない")
	}
	// 期待値は定数から導く。数値を直書きすると、較正のたびにテストが
	// 「壊れた」ように見えてしまう (2026-08-31 の 0.51→0.45 で実際に起きた)。
	want := 30 * LitersPerPoint
	if math.Abs(ev.AmountL-want) > 0.05 {
		t.Errorf("給油量 = %.2f L, want %.2f L (30pt × %.2f L/pt)", ev.AmountL, want, LitersPerPoint)
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

// 満タン時は給油量を出さない。センダーが上限に張り付いていて根拠が無い。
//
// 2026-08-28 の給油では 40.69L と算出したが実際は 35.29L で15%過大だった。
// 給油直後から11分間 95.29 に張り付き、燃料が減って測定範囲に戻ってから
// ようやく 90.6 という実測値になった。満タン時の読みは「上限に達した」
// という以上の情報を持たない。
func TestDetector_NoAmountWhenFullTank(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.json")
	feed(NewDetector(p), 15.5, settleSamples)

	d := NewDetector(p)
	feed(d, 95.29, settleSamples) // センダーの上限
	ev := d.Event()
	if ev == nil {
		t.Fatal("検出できていない")
	}
	if !ev.FullTank {
		t.Error("満タンと判定されていない")
	}
	if ev.AmountL != 0 {
		t.Errorf("満タンなのに給油量 %.2fL を出した。根拠が無い値は出さない", ev.AmountL)
	}
	// 跳躍量は残す。較正の材料になる。
	if ev.DeltaPt < 79 || ev.DeltaPt > 80 {
		t.Errorf("跳躍 = %.2f, want 79.8 付近", ev.DeltaPt)
	}
}

// 部分給油なら量を出す。上限に達していないので測れている。
func TestDetector_AmountWhenPartialRefuel(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.json")
	feed(NewDetector(p), 20, settleSamples)

	d := NewDetector(p)
	feed(d, 60, settleSamples)
	ev := d.Event()
	if ev == nil {
		t.Fatal("検出できていない")
	}
	if ev.FullTank {
		t.Error("満タンと誤判定した")
	}
	want := 40 * LitersPerPoint
	if ev.AmountL < want-0.2 || ev.AmountL > want+0.2 {
		t.Errorf("給油量 = %.2fL, want %.2f", ev.AmountL, want)
	}
}

// 送信できなかった給油イベントは再起動をまたいで保持すること。
//
// 2026-08-28 の給油では起動時に WiFi が間に合わず初回送信がスキップされた。
// イベントがメモリ上にしか無かったため、そのままエンジンを切っていれば
// 給油記録は永久に失われていた。last_settled_pt は既に給油後の値に
// 更新されており、次回起動では跳躍が検出できないからである。
func TestDetector_EventSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.json")

	// 前回: 残量 15pt で終了
	feed(NewDetector(p), 15, settleSamples)

	// 給油して再起動。95pt を検出する。
	d := NewDetector(p)
	feed(d, 95, settleSamples)
	ev := d.Event()
	if ev == nil {
		t.Fatal("給油を検出できていない")
	}
	if ev.DeltaPt < 79 || ev.DeltaPt > 81 {
		t.Fatalf("跳躍 = %.1f, want 80 付近", ev.DeltaPt)
	}

	// 送信できないまま電源が落ちた。次の起動で引き継げること。
	d2 := NewDetector(p)
	ev2 := d2.Event()
	if ev2 == nil {
		t.Fatal("未送信のイベントが失われた")
	}
	if ev2.DeltaPt != ev.DeltaPt || ev2.AmountL != ev.AmountL {
		t.Errorf("引き継いだ内容が違う: %+v vs %+v", ev2, ev)
	}
}

// 送信できたら不揮発からも消す。次回起動で二重記録しない。
func TestDetector_ClearedEventDoesNotSurvive(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.json")
	d := NewDetector(p)
	feed(d, 15, settleSamples)
	d = NewDetector(p)
	feed(d, 95, settleSamples)
	if d.Event() == nil {
		t.Fatal("検出できていない")
	}
	d.ClearEvent()

	if d2 := NewDetector(p); d2.Event() != nil {
		t.Error("送信済みのイベントが復活した。二重記録になる")
	}
}

// 送れないうちに2回給油したら、まとめて1件にする。
func TestDetector_MergesUnsentRefuels(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.json")
	d := NewDetector(p)
	feed(d, 20, settleSamples)

	// 1回目の給油: 20 → 60 (未送信のまま電源断)
	d = NewDetector(p)
	feed(d, 60, settleSamples)
	if d.Event() == nil {
		t.Fatal("1回目を検出できていない")
	}

	// 2回目の給油: 60 → 95
	d = NewDetector(p)
	feed(d, 95, settleSamples)
	ev := d.Event()
	if ev == nil {
		t.Fatal("2回目を検出できていない")
	}
	if ev.BeforePt < 19 || ev.BeforePt > 21 {
		t.Errorf("給油前 = %.1f, want 20 (1回目の給油前を引き継ぐ)", ev.BeforePt)
	}
	if ev.DeltaPt < 74 || ev.DeltaPt > 76 {
		t.Errorf("跳躍 = %.1f, want 75 (2回分の合計)", ev.DeltaPt)
	}
}

// 2026-08-30 21:41 の実データ。満タンにしたのに 92.94pt で落ち着き、
// 旧しきい値 93.0 を 0.06pt 下回って部分給油と判定された。その結果
// 29.70L と公表したが、レシートは 24.70L で +20.2% の過大だった。
func TestDetector_FullTankAt9294(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.json")
	feed(NewDetector(p), 34.70, settleSamples)

	d := NewDetector(p)
	feed(d, 92.94, settleSamples)
	ev := d.Event()
	if ev == nil {
		t.Fatal("検出できていない")
	}
	if !ev.FullTank {
		t.Fatalf("92.94pt が満タンと判定されていない (しきい値 %.1f)", FullTankPt)
	}
	if ev.AmountL != 0 {
		t.Errorf("給油量 %.2fL を出した。実測は 24.70L で、この推定は +20%% 外す", ev.AmountL)
	}
}

// 満タン給油の直後に落ち着いた値は実測4回で 90.59〜95.29pt にばらつく。
// しきい値はこの全部を満タン側に入れなければならない。
func TestDetector_ObservedFullTankReadings(t *testing.T) {
	for _, pt := range []float64{90.59, 92.94, 93.98, 95.29} {
		dir := t.TempDir()
		p := filepath.Join(dir, "s.json")
		feed(NewDetector(p), 30.0, settleSamples)

		d := NewDetector(p)
		feed(d, pt, settleSamples)
		ev := d.Event()
		if ev == nil {
			t.Fatalf("%.2fpt: 検出できていない", pt)
		}
		if !ev.FullTank || ev.AmountL != 0 {
			t.Errorf("%.2fpt: full=%v amount=%.2fL, want full=true amount=0", pt, ev.FullTank, ev.AmountL)
		}
	}
}
