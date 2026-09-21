package main

import (
	"testing"
	"time"

	"github.com/hashimoto/pi-obd-meter/internal/fuel"
)

func TestRefuelUIProgression(t *testing.T) {
	app := &App{}
	ev := &fuel.Event{DetectedAt: time.Now(), FullTank: true, DeltaPt: 74.2}

	if !app.noteRefuelDetected(ev) {
		t.Fatal("初回の検出で true が返らない")
	}
	if app.noteRefuelDetected(ev) {
		t.Fatal("同じ給油で2回 true が返った（毎サンプル送信してしまう）")
	}

	got := app.refuelUISnapshot(0)
	if got == nil || got.Stage != refuelStageDetected {
		t.Fatalf("検出直後の stage が違う: %+v", got)
	}
	if !got.FullTank || got.AmountL != 0 {
		t.Fatalf("満タンなのに量を出している: %+v", got)
	}

	app.noteRefuelRecorded()
	if got := app.refuelUISnapshot(0); got.Stage != refuelStageRecorded {
		t.Fatalf("記録後の stage が違う: %+v", got)
	}

	app.noteRefuelTripReset(432.1, 13.5)
	got = app.refuelUISnapshot(0)
	if got.Stage != refuelStageTripReset {
		t.Fatalf("リセット後の stage が違う: %+v", got)
	}
	if got.PrevTripKm != 432.1 || got.PrevEcoKmpl != 13.5 {
		t.Fatalf("前のタンクの値が入っていない: %+v", got)
	}
}

func TestRefuelUIPartialKeepsAmount(t *testing.T) {
	app := &App{}
	app.noteRefuelDetected(&fuel.Event{DetectedAt: time.Now(), AmountL: 11.7, DeltaPt: 26.1})

	got := app.refuelUISnapshot(0)
	if got.FullTank || got.AmountL != 11.7 {
		t.Fatalf("部分給油の量が落ちている: %+v", got)
	}
}

func TestRefuelUIHiddenWhileMoving(t *testing.T) {
	app := &App{}
	app.noteRefuelDetected(&fuel.Event{DetectedAt: time.Now(), FullTank: true})

	if got := app.refuelUISnapshot(refuelUIHideSpeedKmh); got != nil {
		t.Fatalf("走行中に出している: %+v", got)
	}
	// 走り出しても状態は捨てない。次に停まったらまた出る。
	if got := app.refuelUISnapshot(0); got == nil {
		t.Fatal("停車したのに出なくなった")
	}
}

func TestRefuelUIExpiresAfterDone(t *testing.T) {
	app := &App{}
	app.noteRefuelDetected(&fuel.Event{DetectedAt: time.Now(), FullTank: true})
	app.noteRefuelRecorded()
	app.noteRefuelTripReset(100, 12)

	app.refuelUIMu.Lock()
	app.refuelUI.doneAt = time.Now().Add(-refuelUIHoldAfterDone - time.Second)
	app.refuelUIMu.Unlock()

	if got := app.refuelUISnapshot(0); got != nil {
		t.Fatalf("完了から時間が経っても消えない: %+v", got)
	}
}

// agePastLimit は「最後に段階が進んでから上限を過ぎた」状態にする。
func agePastLimit(app *App) {
	app.refuelUIMu.Lock()
	defer app.refuelUIMu.Unlock()
	app.refuelUI.lastProgressAt = time.Now().Add(-refuelUIMaxAge - time.Minute)
}

// 送信できないまま抱え続けたら、いつかは引っ込める。
// WiFi が繋がらなければ次の起動に持ち越すので、出し続けても運転者にできることは無い。
func TestRefuelUIExpiresWhenNeverSent(t *testing.T) {
	app := &App{}
	app.noteRefuelDetected(&fuel.Event{DetectedAt: time.Now(), FullTank: true})
	agePastLimit(app)

	if got := app.refuelUISnapshot(0); got != nil {
		t.Fatalf("未送信のまま上限を過ぎても消えない: %+v", got)
	}
}

// 前回の起動から持ち越した給油イベントは、検出時刻が古くても今回の起動で出す。
// 「給油した。動いてる？」に答えるのがこのダイアログの役目で、
// 持ち越し中こそ答えが要る。
func TestRefuelUICarriedOverEventIsShown(t *testing.T) {
	app := &App{}
	app.noteRefuelDetected(&fuel.Event{DetectedAt: time.Now().Add(-8 * time.Hour), FullTank: true})

	got := app.refuelUISnapshot(0)
	if got == nil || got.Stage != refuelStageDetected {
		t.Fatalf("持ち越したイベントが出てこない: %+v", got)
	}
}

// 期限切れのあと、同じ給油で状態が作り直されないこと。
//
// refuelUISnapshot が状態を捨てても、未送信のイベントは app.refuel に残る
// (送信に成功するまで消えない)。作り直しを許すと、obdProcessingLoop が
// 50ms ごとに「作成 → 破棄 → 無駄な送信」を繰り返す。
// 2026-09-21 のクロスレビュー (agy) の指摘。
func TestRefuelUIExpired_DoesNotRearm(t *testing.T) {
	app := &App{}
	ev := &fuel.Event{DetectedAt: time.Now(), FullTank: true}

	if !app.noteRefuelDetected(ev) {
		t.Fatal("初回の検出で true が返らない")
	}
	agePastLimit(app)
	if got := app.refuelUISnapshot(0); got != nil {
		t.Fatalf("上限を過ぎても出している: %+v", got)
	}

	// ここで true が返ると、未送信の間ずっと送信が繰り返される
	if app.noteRefuelDetected(ev) {
		t.Fatal("期限切れのあとに同じ給油で作り直した（送信が繰り返される）")
	}
	if got := app.refuelUISnapshot(0); got != nil {
		t.Fatalf("作り直した状態を出している: %+v", got)
	}

	// 送信できたら、また出す。「動いてる？」に答えるのがこのダイアログの役目。
	app.noteRefuelRecorded()
	got := app.refuelUISnapshot(0)
	if got == nil || got.Stage != refuelStageRecorded {
		t.Fatalf("送信できたのに出てこない: %+v", got)
	}
}

func TestRefuelUISnapshotNilWithoutEvent(t *testing.T) {
	app := &App{}
	if got := app.refuelUISnapshot(0); got != nil {
		t.Fatalf("検出していないのに出している: %+v", got)
	}
	if app.noteRefuelDetected(nil) {
		t.Fatal("nil イベントで true が返った")
	}
}
