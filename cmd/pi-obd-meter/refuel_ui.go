package main

import (
	"time"

	"github.com/hashimoto/pi-obd-meter/internal/fuel"
)

// 給油ダイアログ (#120)。自動検出が最後まで届いたかを画面で見せる。
//
// 検出から記録までは1回の送信で終わるが、途中で止まっても今までは
// 画面から分からなかった。分かるのは最後にトリップが 0 に戻ることだけで、
// 「給油した。動いてる？」に答える手段が SSH しかなかった (#178)。
const (
	refuelStageDetected  = 1 // 燃料計の跳躍から検出した
	refuelStageRecorded  = 2 // GAS に記録できた
	refuelStageTripReset = 3 // トリップのリセットまで戻ってきた

	// refuelUIHideSpeedKmh 以上で走り出したら出さない。
	// 停車中に一瞥するためのもので、走行中に読ませるものではない。
	refuelUIHideSpeedKmh = 2.0

	// トリップのリセットまで終わったあと、画面に残す時間。
	refuelUIHoldAfterDone = 20 * time.Second

	// 段階が進まないまま出し続ける上限。
	//
	// WiFi が繋がらなければ給油イベントは次の起動に持ち越される
	// (fuel.state の PendingEvent)。その間ずっと停車のたびに出しても
	// 運転者にできることは無いので、この時間で引っ込める。
	// 数えるのは検出時刻からではなく、最後に段階が進んだ時刻から
	// (refuelUIState.lastProgressAt)。前回の起動から持ち越したイベントも、
	// 今回の起動で一度は出す必要があるため。
	refuelUIMaxAge = 15 * time.Minute
)

// RefuelUI は給油ダイアログに渡す状態。検出していなければ nil。
//
// ポイント (燃料センダーの生値) は画面に出さない。運転者にとって意味のある
// 単位ではないうえ、センダーは両端でクリップするので「95pt」は「上限に
// 達した」以上のことを言えない。満タンなら満タンとだけ、部分給油なら
// 推定であることを添えてリットルで出す。
type RefuelUI struct {
	Stage       int     `json:"stage"`                   // 1=検出 2=記録 3=トリップリセット
	FullTank    bool    `json:"full_tank"`               // 満タン。量は出せない
	AmountL     float64 `json:"amount_l,omitempty"`      // 部分給油の推定量 (L)
	PrevTripKm  float64 `json:"prev_trip_km,omitempty"`  // 前のタンクで走った距離
	PrevEcoKmpl float64 `json:"prev_eco_kmpl,omitempty"` // 前のタンクの平均燃費 (Pi 計算)
}

// refuelUIState は給油ダイアログの進み具合を持つ。
type refuelUIState struct {
	detectedAt     time.Time // 給油イベントの検出時刻 (同じ給油かどうかの判定に使う)
	lastProgressAt time.Time // 最後に段階が進んだ時刻 (表示を引っ込める判断に使う)
	stage          int
	fullTank       bool
	amountL        float64
	prevTripKm     float64
	prevEcoKmpl    float64
	doneAt         time.Time // トリップのリセットまで終わった時刻
}

// noteRefuelDetected は検出した給油を取り込む。
// 新しい給油なら true を返す。呼び出し側はそこで即時送信をかける。
//
// 検出した瞬間に送らないと、次の定期送信まで最大5分かかる。その間
// ダイアログは「送信待ち」で止まったままになり、動いていないのか
// 待っているだけなのかが運転者に区別できない。
func (app *App) noteRefuelDetected(ev *fuel.Event) bool {
	if ev == nil {
		return false
	}
	app.refuelUIMu.Lock()
	defer app.refuelUIMu.Unlock()

	if app.refuelUI != nil && app.refuelUI.detectedAt.Equal(ev.DetectedAt) {
		return false // 同じ給油を見ているだけ
	}
	app.refuelUI = &refuelUIState{
		detectedAt:     ev.DetectedAt,
		lastProgressAt: time.Now(),
		stage:          refuelStageDetected,
		fullTank:       ev.FullTank,
		amountL:        ev.AmountL,
	}
	return true
}

// noteRefuelRecorded は GAS への記録が済んだことを記録する。
//
// 送信までに時間がかかって一度引っ込めていても、ここでまた出す。
// 「送れたのか、まだ待っているのか」を見せるのがこのダイアログの役目なので、
// 送れた事実こそ出す価値がある。
func (app *App) noteRefuelRecorded() {
	app.refuelUIMu.Lock()
	defer app.refuelUIMu.Unlock()
	if app.refuelUI != nil && app.refuelUI.stage < refuelStageRecorded {
		app.refuelUI.stage = refuelStageRecorded
		app.refuelUI.lastProgressAt = time.Now()
	}
}

// noteRefuelTripReset はトリップのリセットが返ってきたことを記録する。
// prevTripKm / prevEcoKmpl はリセット前のトリップの値を渡すこと。
func (app *App) noteRefuelTripReset(prevTripKm, prevEcoKmpl float64) {
	app.refuelUIMu.Lock()
	defer app.refuelUIMu.Unlock()
	if app.refuelUI == nil {
		return
	}
	app.refuelUI.stage = refuelStageTripReset
	app.refuelUI.prevTripKm = prevTripKm
	app.refuelUI.prevEcoKmpl = prevEcoKmpl
	app.refuelUI.doneAt = time.Now()
	app.refuelUI.lastProgressAt = app.refuelUI.doneAt
}

// refuelUISnapshot は画面に渡す状態を返す。出さないときは nil。
//
// 走行中は nil を返すが状態は捨てない。送信が終わっていない給油を
// 抱えたまま走り出しても、次に停まったときにまた出る。
func (app *App) refuelUISnapshot(speedKmh float64) *RefuelUI {
	app.refuelUIMu.Lock()
	defer app.refuelUIMu.Unlock()

	s := app.refuelUI
	if s == nil {
		return nil
	}

	now := time.Now()
	switch {
	case s.stage >= refuelStageTripReset && now.Sub(s.doneAt) > refuelUIHoldAfterDone:
		app.refuelUI = nil
		return nil

	case s.stage == refuelStageRecorded && now.Sub(s.lastProgressAt) > refuelUIMaxAge:
		// 記録が済んだ給油イベントは検出器から消えている (ClearEvent)。
		// 捨てても作り直されない。
		app.refuelUI = nil
		return nil

	case s.stage == refuelStageDetected && now.Sub(s.lastProgressAt) > refuelUIMaxAge:
		// まだ送れていない。イベントは検出器に残っている (fuel.state の
		// PendingEvent)。ここで状態を捨てると、次のループで
		// noteRefuelDetected が「新しい給油」と見なして作り直し、
		// 50ms ごとに作成・破棄と無駄な送信を繰り返す。
		// 表示だけ引っ込め、状態は残す (2026-09-21 のクロスレビューの指摘)。
		return nil
	}

	if speedKmh >= refuelUIHideSpeedKmh {
		return nil
	}

	return &RefuelUI{
		Stage:       s.stage,
		FullTank:    s.fullTank,
		AmountL:     s.amountL,
		PrevTripKm:  s.prevTripKm,
		PrevEcoKmpl: s.prevEcoKmpl,
	}
}
