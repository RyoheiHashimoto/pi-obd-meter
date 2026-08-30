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
	//
	// ただし給油の跳躍量に掛けると、レシートに対して3回とも過大に出る。
	//
	//   給油              推定     レシート   乖離     含意 L/pt
	//   2026-08-28       40.69L    35.29L   +15.3%     0.442
	//   2026-08-30 15:41 19.98L    18.80L    +6.3%     0.480
	//   2026-08-30 21:41 29.70L    24.70L   +20.2%     0.424
	//
	// 含意値が 0.424〜0.480 と13%ばらつくので、定数を平均の 0.449 に替えても
	// ±6% は残る。センダーが両端でクリップし途中も直線でないためで、単一の
	// 係数では詰められない。ここを弄る前に部分給油の実測を貯めること。
	//
	// なお上の3件はいずれも満タン給油で、FullTankPt を 90.0 に下げた今は
	// どれも量を出さない側に入る。この定数が効くのは部分給油だけになった。
	//
	// --- 2026-08-31 に 0.51 → 0.45 へ改訂 ---
	//
	// 独立した2つの経路が同じ答えを出した。
	//
	//  (a) 給油の跳躍量 ÷ レシート   0.442 / 0.480 / 0.424   平均 0.449
	//  (b) 給油区間での燃料計の減少 ÷ 次の給油量
	//        区間1  77.7pt → 35.29L   0.454
	//        区間2  40.4pt → 18.80L   0.465
	//        区間3  59.2pt → 24.70L   0.417   平均 0.445
	//
	// (a) は給油の瞬間、(b) は数百kmの消費という別の現象を見ているのに
	// 0.445 と 0.449 で一致する。元の 0.51 は 14% 高い。
	//
	// 旧根拠 (アプリの燃料積算からの逆算) が外れていたのは、その積算自体が
	// レシート比で 22% 過大だったため。同じ誤差が定数に転写されていた。
	// 今回はレシートの実数だけを基準にしている。
	LitersPerPoint = 0.45

	// DetectThresholdPt は給油と判定する跳躍量。
	//
	// 32回の起動境界で実測した結果、給油は +86.7ポイント、それ以外の最大は +3.5ポイント
	// だった。25倍の開きがあり、+10 なら誤差最大値に対して約3倍の余裕がある。
	// 4L 未満の給油は取り逃すが実害はない。
	DetectThresholdPt = 10.0

	// FullTankPt は「満タン」と判定する到達値。
	//
	// センダーは raw 243 (95.3pt) でクリップするため、それ以上は区別できない。
	// 満タン時の読みは「上限に達した」という以上の情報を持たない。
	//
	// 当初 93.0 に置いていたが、これが実測のばらつきの真ん中を切っていた。
	// 満タンにした給油の直後に落ち着く値は4回の実測で 90.59 / 92.94 / 93.98 /
	// 95.29pt とばらつく。フロートの止まり位置と車体の傾きで数ポイント動く。
	//
	// 2026-08-30 21:41 の給油はこの線の 0.06pt 下 (92.94) に落ちた。満タンに
	// したのに部分給油として扱われ、29.70L という量を公表した。レシートは
	// 24.70L で +20.2% の過大だった。0.06pt の差で「何も言わない」から
	// 「20%外した値を言う」に変わってしまう。
	//
	// 90.0 なら実測4回すべてが満タン側に入る。センダーがクリップに近づいて
	// 分解能を失うのは 90pt あたりからで、そこから上は「満タン」以上のことを
	// 言えないという事実とも合う。
	//
	// 満タン法による燃費検算は満タンでなければ成立しないため、この区別だけを持つ。
	FullTankPt = 90.0

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

	// 未送信の給油イベント。
	//
	// 検出しても GAS へ送れるとは限らない。起動直後は WiFi が繋がるまで
	// 時間がかかり、実際 2026-08-28 の給油では「WiFi接続待ちタイムアウト」で
	// 初回送信がスキップされた。そのままエンジンを切っていれば、給油記録は
	// 永久に失われていた。last_settled_pt は既に給油後の値に更新されており、
	// 次回起動では跳躍が検出できないからである。
	//
	// 送信が成功するまでファイルに残し、再起動をまたいで持ち越す。
	PendingEvent *Event `json:"pending_event,omitempty"`
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
		if json.Unmarshal(b, &s) == nil {
			if s.LastSettledPt > 0 {
				d.prev = s.LastSettledPt
				d.prevValid = true
			}
			// 前回送れなかった給油を引き継ぐ
			d.event = s.PendingEvent
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
				// 前回の給油をまだ送れていなければ、そちらの給油前残量を使う。
				// 2回分をまとめて1件として記録すれば取りこぼさない。
				before := d.prev
				if d.event != nil {
					before = d.event.BeforePt
				}
				full := d.current >= FullTankPt
				ev := &Event{
					DetectedAt: time.Now(),
					BeforePt:   before,
					AfterPt:    d.current,
					DeltaPt:    d.current - before,
					FullTank:   full,
				}
				// 満タンのときは給油量を出さない。
				//
				// センダーが上限に張り付いているので、給油後の残量が実際に
				// どこまで行ったか分からない。跳躍量は「最低これだけ」で
				// あって真の値ではなく、そこから計算した量には根拠が無い。
				// 2026-08-28 の給油では 40.69L と算出したが実際は 35.29L で、
				// 15%過大だった。
				//
				// 分からないものは出さない。満タン法の燃費計算にはレシートの
				// 実数が要るので実害は無く、トリップのリセットは変わらず働く。
				if !full {
					ev.AmountL = ev.DeltaPt * LitersPerPoint
				}
				d.event = ev
				// 検出は間引かず即座に保存する。ここで電源が落ちても失わない。
				d.save(d.current)
				d.lastSaved = d.current
				d.lastSaveAt = time.Now()
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
	// 送信できたので不揮発からも消す。次回起動で二重に記録しない。
	if d.settled {
		d.save(d.current)
	} else if d.prevValid {
		d.save(d.prev)
	}
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
	b, err := json.Marshal(state{
		LastSettledPt: pt,
		SavedAt:       time.Now(),
		PendingEvent:  d.event,
	})
	if err != nil {
		return
	}
	_ = atomicfile.Write(d.statePath, b, 0644)
}
