// Package fuel は CAN 燃料残量 (0x430 B0) からの給油自動検出を提供する。
package fuel

import (
	"encoding/json"
	"math"
	"os"
	"sync"
	"time"

	"github.com/hashimoto/pi-obd-meter/internal/atomicfile"
)

const (
	// LitersPerPoint は燃料残量1ポイント(raw/2.55)あたりの容量。
	//
	// 検証済みのアプリ燃料積算 (満タン法との誤差が11回で中央値 -0.6%) を基準に逆算した。
	//   142km区間  アプリ積算 15.511 L ÷ 燃料計の減少 30.6ポイント = 0.507 L/pt
	//   独立検算   タンク 45L ÷ センダーのスパン 86.7ポイント      = 0.519 L/pt
	// 2つの独立した経路が 0.51 で一致する。
	LitersPerPoint = 0.51

	// DetectThresholdPt は給油と判定する跳躍量。
	//
	// 32回の起動境界で実測した結果、給油は +86.7ポイント、それ以外の最大は +3.5ポイント
	// だった。25倍の開きがあり、+10 なら誤差最大値に対して約3倍の余裕がある。
	// 4L 未満の給油は取り逃すが実害はない。
	DetectThresholdPt = 10.0

	// FullTankPt は「満タン」と判定する到達値。
	//
	// センダーは raw 243 (95.3%) でクリップするため、それ以上は区別できない。
	// 満タン法による燃費検算は満タンでなければ成立しないため、この区別だけを持つ。
	FullTankPt = 93.0

	// settleSamples は「落ち着いた値」とみなすのに必要なサンプル数。
	// 走行中はスロッシングで 24〜33ポイント振れるため、停車時のみ採る。
	settleSamples = 20

	// saveInterval は状態ファイルを書く最短間隔。
	//
	// Update は 50ms 周期 (毎秒20回) で呼ばれる。停車のたびに毎回保存すると
	// アイドリング20分で 24,000 回 SD に書くことになる。atomicfile は
	// 一時ファイル作成→書込→fsync→rename を行うため1回が重く、車載機は
	// エンジン停止で毎回不正電断するため、書き込み量がそのまま破損リスクになる。
	//
	// 燃料残量は分単位でしか動かないので、30秒間隔で十分。
	saveInterval = 30 * time.Second

	// saveMinDeltaPt は保存するに値する変化量。これ未満なら書かない。
	saveMinDeltaPt = 0.5
)

// Event は検出した給油を表す。
type Event struct {
	DetectedAt time.Time `json:"detected_at"`
	BeforePt   float64   `json:"before_pt"`
	AfterPt    float64   `json:"after_pt"`
	DeltaPt    float64   `json:"delta_pt"`
	AmountL    float64   `json:"amount_l"`
	FullTank   bool      `json:"full_tank"`
}

type state struct {
	LastSettledPt float64   `json:"last_settled_pt"`
	SavedAt       time.Time `json:"saved_at"`
}

// Detector は起動時の燃料残量の跳躍から給油を検出する。
//
// 給油中はエンジンが止まっており Pi も落ちているため、走行中の監視では検出できない。
// 停車中の値を不揮発に保存しておき、次回起動時の停車中の値と比較する。
type Detector struct {
	mu        sync.Mutex
	statePath string

	prev      float64 // 前回起動時に保存された落ち着いた値
	prevValid bool

	// 直近 settleSamples 個の停車中サンプルを保持するリングバッファ。
	//
	// 当初は起動後の全停車サンプルの累積平均を使っていたが、走行中に燃料が
	// 減るため、平均は実際の残量より高く出る。長距離ほど乖離が大きく、
	// 90pt で出発し 40pt で到着した場合の保存値は約 65pt になり、満タン給油
	// 時の跳躍が 55pt ではなく 30pt と算出されてしまう (給油量が45%過少)。
	// 直近の窓だけを見れば、常に「今の残量」になる。
	window []float64
	wi     int
	filled bool

	current float64
	settled bool

	lastSaved  float64
	lastSaveAt time.Time

	checked bool   // 判定は起動につき1回だけ
	event   *Event // 検出した給油 (無ければ nil)
}

// NewDetector は不揮発の状態を読み込んで検出器を作る。
func NewDetector(statePath string) *Detector {
	d := &Detector{statePath: statePath, window: make([]float64, settleSamples)}
	if b, err := os.ReadFile(statePath); err == nil {
		var s state
		if json.Unmarshal(b, &s) == nil && s.LastSettledPt > 0 {
			d.prev = s.LastSettledPt
			d.prevValid = true
		}
	}
	return d
}

// Update は最新の燃料残量を取り込む。stopped は車両が停止しているか。
//
// 走行中はスロッシングで値が大きく振れるため、停車中のみ平均に加える。
// settleSamples 個たまった時点で「落ち着いた値」とみなし、給油判定を1回だけ行う。
func (d *Detector) Update(levelPt float64, stopped bool) {
	if d == nil || levelPt <= 0 || !stopped {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.window) != settleSamples {
		d.window = make([]float64, settleSamples)
	}
	d.window[d.wi] = levelPt
	d.wi++
	if d.wi >= settleSamples {
		d.wi = 0
		d.filled = true
	}
	if !d.filled {
		return // まだ窓が埋まっていない
	}

	var sum float64
	for _, v := range d.window {
		sum += v
	}
	d.current = sum / float64(settleSamples)
	d.settled = true

	// 給油判定は起動につき1回だけ。最初に落ち着いた値で判断する。
	if !d.checked {
		d.checked = true
		if d.prevValid {
			delta := d.current - d.prev
			if delta >= DetectThresholdPt {
				d.event = &Event{
					DetectedAt: time.Now(),
					BeforePt:   d.prev,
					AfterPt:    d.current,
					DeltaPt:    delta,
					AmountL:    delta * LitersPerPoint,
					FullTank:   d.current >= FullTankPt,
				}
			}
		}
	}

	// 次回起動時の比較対象を保存する。ただし書きすぎない。
	now := time.Now()
	if d.lastSaveAt.IsZero() ||
		(now.Sub(d.lastSaveAt) >= saveInterval && math.Abs(d.current-d.lastSaved) >= saveMinDeltaPt) {
		d.save(d.current)
		d.lastSaved = d.current
		d.lastSaveAt = now
	}
}

// Event は検出した給油を返す。無ければ nil。
func (d *Detector) Event() *Event {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.event
}

// ClearEvent は送信済みの給油イベントを消す。
func (d *Detector) ClearEvent() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.event = nil
}

// Settled は落ち着いた値が得られたかを返す。
func (d *Detector) Settled() bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.settled
}

func (d *Detector) save(pt float64) {
	if d.statePath == "" {
		return
	}
	b, err := json.Marshal(state{LastSettledPt: pt, SavedAt: time.Now()})
	if err != nil {
		return
	}
	_ = atomicfile.Write(d.statePath, b, 0644)
}
