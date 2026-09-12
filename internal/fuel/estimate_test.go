package fuel

import (
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	estTankL = 46.0
	estTick  = 0.05 // 本番の周期 (poll_interval_ms 50)
)

// estRun は 50ms 周期で n 回回す。level は周期ごとの燃料計の生値 (pt)。
func estRun(e *Estimator, n int, level func(i int) float64, burnPerTick, settledL float64) {
	for i := 0; i < n; i++ {
		e.Update(level(i), burnPerTick, estTick, settledL)
	}
}

func constLevel(pt float64) func(int) float64 { return func(int) float64 { return pt } }

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.3f, want %.3f ±%.3f", name, got, want, tol)
	}
}

func TestEstimator_落ち着く前は0(t *testing.T) {
	e := NewEstimator(filepath.Join(t.TempDir(), "est.json"), estTankL)
	estRun(e, 100, constLevel(80), 0.001, 0)
	if got := e.Liters(); got != 0 {
		t.Errorf("保存値も落ち着いた値も無いのに %.3f L", got)
	}
}

func TestEstimator_初回は起動時の落ち着いた値で始まる(t *testing.T) {
	e := NewEstimator(filepath.Join(t.TempDir(), "est.json"), estTankL)
	e.Update(80, 0, estTick, 36.0)
	near(t, "Liters", e.Liters(), 36.0, 0.001)
}

func TestEstimator_使った燃料の分だけ減る(t *testing.T) {
	e := NewEstimator(filepath.Join(t.TempDir(), "est.json"), estTankL)
	e.Update(0, 0, estTick, 30.0)
	estRun(e, 1000, constLevel(0), 0.001, 30.0) // 燃料計なしで 1L 使う
	near(t, "Liters", e.Liters(), 29.0, 1e-6)
}

// 本題。停車時のスロッシングで航続距離が跳ねないこと。
// 旧方式では停車直後に航続距離換算で 10〜25km 振れた。
func TestEstimator_スロッシングで跳ねない(t *testing.T) {
	e := NewEstimator(filepath.Join(t.TempDir(), "est.json"), estTankL)
	const truthL = 30.0
	truthPt := truthL / LitersPerPoint
	e.Update(truthPt, 0, estTick, truthL)

	// 周期2秒・振幅12pt の揺れに白色雑音を重ねる。実測の停車直後は 71〜86pt
	// (±7pt) 程度なので、それより厳しい。
	rng := rand.New(rand.NewSource(1))
	level := func(i int) float64 {
		return truthPt + 12*math.Sin(2*math.Pi*float64(i)*estTick/2.0) + rng.Float64()*8 - 4
	}
	const eco = 10.2 // km/L
	prev := e.Liters()
	maxStepKm := 0.0
	for s := 0; s < 600; s++ { // 10分
		estRun(e, 20, func(i int) float64 { return level(s*20 + i) }, 0, 0)
		step := math.Abs(e.Liters()-prev) * eco
		if step > maxStepKm {
			maxStepKm = step
		}
		prev = e.Liters()
	}
	if maxStepKm > 0.1 {
		t.Errorf("1秒で航続距離が最大 %.2f km 動いた。揺れが表示に出ている", maxStepKm)
	}
	near(t, "10分後の残量", e.Liters(), truthL, 0.2)
}

// 燃料積算の誤差は燃料計がゆっくり引き戻す。時定数1つ分で差は 1/e になる。
func TestEstimator_燃料計へゆっくり寄る(t *testing.T) {
	e := NewEstimator(filepath.Join(t.TempDir(), "est.json"), estTankL)
	e.Update(0, 0, estTick, 30.0)
	n := int(EstimateTauSec / estTick)
	estRun(e, n, constLevel(32.0/LitersPerPoint), 0, 0)
	near(t, "時定数後の残量", e.Liters(), 32.0-2.0/math.E, 0.01)
}

// 走行中や信号待ちで再起動しても、保存値から続く。
func TestEstimator_再起動しても保存値から続く(t *testing.T) {
	path := filepath.Join(t.TempDir(), "est.json")
	e1 := NewEstimator(path, estTankL)
	e1.Update(0, 0, estTick, 30.0)
	estRun(e1, 2000, constLevel(0), 0.001, 30.0) // 2L 使う
	e1.Save()

	e2 := NewEstimator(path, estTankL)
	near(t, "再起動直後", e2.Liters(), 28.0, 1e-6)
}

// 信号待ちで再起動すると、最初に落ち着いた値は揺れている。
// 差が給油の閾値に満たなければ保存値を使い続ける。
func TestEstimator_再起動直後の揺れた値では合わせ直さない(t *testing.T) {
	path := filepath.Join(t.TempDir(), "est.json")
	e1 := NewEstimator(path, estTankL)
	e1.Update(0, 0, estTick, 28.0)
	e1.Save()

	e2 := NewEstimator(path, estTankL)
	e2.Update(0, 0, estTick, 30.5) // 揺れで 2.5L 高く出た
	near(t, "Liters", e2.Liters(), 28.0, 1e-6)
}

func TestEstimator_給油したら燃料計の値に合わせる(t *testing.T) {
	path := filepath.Join(t.TempDir(), "est.json")
	e1 := NewEstimator(path, estTankL)
	e1.Update(0, 0, estTick, 10.0)
	e1.Save()

	e2 := NewEstimator(path, estTankL)
	e2.Update(0, 0, estTick, 40.0)
	near(t, "給油後", e2.Liters(), 40.0, 1e-6)

	// 保存もされている。直後に電源が落ちても給油前に戻らない。
	e3 := NewEstimator(path, estTankL)
	near(t, "給油後に再起動", e3.Liters(), 40.0, 1e-6)
}

// 突き合わせは起動につき1回。その後の停車で落ち着いた値は揺れているので使わない。
func TestEstimator_突き合わせは起動につき1回(t *testing.T) {
	path := filepath.Join(t.TempDir(), "est.json")
	e1 := NewEstimator(path, estTankL)
	e1.Update(0, 0, estTick, 28.0)
	e1.Save()

	e2 := NewEstimator(path, estTankL)
	e2.Update(0, 0, estTick, 28.5)
	e2.Update(0, 0, estTick, 36.0) // 閾値を超える差だが、起動時ではない
	near(t, "Liters", e2.Liters(), 28.0, 1e-6)
}

func TestEstimator_範囲外に出ない(t *testing.T) {
	e := NewEstimator(filepath.Join(t.TempDir(), "est.json"), estTankL)
	e.Update(0, 0, estTick, 60.0) // センダーのクリップや換算誤差でタンク超過
	near(t, "上限", e.Liters(), estTankL, 1e-9)
	e.Update(0, 100, estTick, 0)
	near(t, "下限", e.Liters(), 0, 1e-9)
}

// 通信断などで周期が空いたとき、その瞬間の燃料計の値へ大きく寄せない。
func TestEstimator_長い空白では寄せない(t *testing.T) {
	e := NewEstimator(filepath.Join(t.TempDir(), "est.json"), estTankL)
	e.Update(0, 0, estTick, 30.0)
	e.Update(90, 0, 60, 0)
	near(t, "Liters", e.Liters(), 30.0, 1e-9)
}

// 保存は 30秒かつ 0.05L 以上の変化があったときだけ。毎周期書くと SD を傷める。
func TestEstimator_保存を間引く(t *testing.T) {
	path := filepath.Join(t.TempDir(), "est.json")
	clk := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	e := NewEstimator(path, estTankL)
	e.now = func() time.Time { return clk }
	e.Update(0, 0, estTick, 30.0) // 初回は保存する

	saved := func() float64 {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("読めない: %v", err)
		}
		var s estimateState
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatalf("壊れている: %v", err)
		}
		return s.Liters
	}

	estRun(e, 100, constLevel(0), 0.001, 0) // 0.1L 使う。時刻は進めない
	near(t, "30秒たつ前の保存値", saved(), 30.0, 1e-9)

	clk = clk.Add(31 * time.Second)
	e.Update(0, 0.001, estTick, 0)
	near(t, "30秒後の保存値", saved(), 29.899, 1e-6)
}

// App の初期化順序やテストで nil が来てもメーターごと落とさない。
func TestEstimator_nilで落ちない(t *testing.T) {
	var e *Estimator
	e.Update(50, 0.1, estTick, 30)
	e.Save()
	if got := e.Liters(); got != 0 {
		t.Errorf("nil: %.2f, want 0", got)
	}
}
