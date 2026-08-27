// Package fuel は CAN 燃料残量 (0x430 B0) からの給油自動検出を提供する。
package fuel

import (
	"encoding/json"
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

	sum, n  float64 // 今回起動後の停車中サンプルの平均を取る
	current float64
	settled bool

	checked bool   // 判定は起動につき1回だけ
	event   *Event // 検出した給油 (無ければ nil)
}

// NewDetector は不揮発の状態を読み込んで検出器を作る。
func NewDetector(statePath string) *Detector {
	d := &Detector{statePath: statePath}
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

	d.sum += levelPt
	d.n++
	d.current = d.sum / d.n

	if d.n >= settleSamples {
		d.settled = true
	}
	if d.settled && !d.checked {
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
	// 落ち着いた後は、常に最新の落ち着いた値を保存しておく
	// (次回起動時の比較対象になる)
	if d.settled {
		d.save(d.current)
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
