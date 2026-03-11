package main

import (
	"context"
	"testing"
	"time"

	"github.com/hashimoto/pi-obd-meter/internal/obd"
)

// --- OBDEvent テスト ---

func TestOBDEvent_Connected(t *testing.T) {
	ev := OBDEvent{
		Data:      &obd.OBDData{SpeedKmh: 60, RPM: 2000},
		IsFull:    false,
		Connected: true,
		HasMAF:    false,
		HasMAP:    true,
		ReadAt:    time.Now(),
	}

	if !ev.Connected {
		t.Error("Connected should be true")
	}
	if ev.Data == nil {
		t.Fatal("Data should not be nil")
	}
	if ev.Data.SpeedKmh != 60 {
		t.Errorf("SpeedKmh: got %.0f, want 60", ev.Data.SpeedKmh)
	}
	if ev.IsFull {
		t.Error("IsFull should be false for fast read")
	}
	if ev.HasMAF {
		t.Error("HasMAF should be false")
	}
	if !ev.HasMAP {
		t.Error("HasMAP should be true")
	}
}

func TestOBDEvent_Disconnected(t *testing.T) {
	ev := OBDEvent{
		Connected: false,
		ReadAt:    time.Now(),
	}

	if ev.Connected {
		t.Error("Connected should be false")
	}
	if ev.Data != nil {
		t.Error("Data should be nil when disconnected")
	}
}

func TestOBDEvent_FullRead(t *testing.T) {
	ev := OBDEvent{
		Data: &obd.OBDData{
			SpeedKmh:    60,
			RPM:         2000,
			EngineLoad:  50,
			ThrottlePos: 25,
			CoolantTemp: 90,
			IntakeMAP:   80,
		},
		IsFull:    true,
		Connected: true,
		HasMAP:    true,
		ReadAt:    time.Now(),
	}

	if !ev.IsFull {
		t.Error("IsFull should be true for full read")
	}
	if ev.Data.CoolantTemp != 90 {
		t.Errorf("CoolantTemp: got %.0f, want 90", ev.Data.CoolantTemp)
	}
	if ev.Data.IntakeMAP != 80 {
		t.Errorf("IntakeMAP: got %.0f, want 80", ev.Data.IntakeMAP)
	}
}

// --- チャネル統合テスト ---

func TestOBDEventChannel_BasicFlow(t *testing.T) {
	// goroutine からチャネルにイベントが届くパターンをシミュレート
	ch := make(chan OBDEvent, 10)

	// 接続イベント
	ch <- OBDEvent{
		Connected: true,
		HasMAF:    false,
		HasMAP:    true,
		ReadAt:    time.Now(),
	}

	// データイベント（fast）
	ch <- OBDEvent{
		Data:      &obd.OBDData{SpeedKmh: 60, RPM: 2000, EngineLoad: 30, ThrottlePos: 15},
		IsFull:    false,
		Connected: true,
		HasMAP:    true,
		ReadAt:    time.Now(),
	}

	// データイベント（full）
	ch <- OBDEvent{
		Data:      &obd.OBDData{SpeedKmh: 65, RPM: 2100, CoolantTemp: 88, IntakeMAP: 60},
		IsFull:    true,
		Connected: true,
		HasMAP:    true,
		ReadAt:    time.Now(),
	}

	// 切断イベント
	ch <- OBDEvent{Connected: false, ReadAt: time.Now()}

	close(ch)

	var events []OBDEvent
	for ev := range ch {
		events = append(events, ev)
	}

	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}
	if !events[0].Connected {
		t.Error("first event should be connected")
	}
	if events[1].Data.SpeedKmh != 60 {
		t.Errorf("fast read speed: got %.0f, want 60", events[1].Data.SpeedKmh)
	}
	if !events[2].IsFull {
		t.Error("third event should be full read")
	}
	if events[3].Connected {
		t.Error("fourth event should be disconnected")
	}
}

func TestOBDEventChannel_ReconnectDetection(t *testing.T) {
	// メインループでの再接続検出ロジックをテスト
	ch := make(chan OBDEvent, 10)

	// 切断 → 再接続のシーケンス
	ch <- OBDEvent{Connected: false, ReadAt: time.Now()}
	ch <- OBDEvent{Connected: false, ReadAt: time.Now()}
	ch <- OBDEvent{Connected: true, HasMAP: true, ReadAt: time.Now()}
	ch <- OBDEvent{
		Data:      &obd.OBDData{SpeedKmh: 30},
		Connected: true,
		HasMAP:    true,
		ReadAt:    time.Now(),
	}

	close(ch)

	wasConnected := false
	reconnectCount := 0
	for ev := range ch {
		if ev.Connected && !wasConnected {
			reconnectCount++
		}
		wasConnected = ev.Connected
	}

	if reconnectCount != 1 {
		t.Errorf("reconnect count: got %d, want 1", reconnectCount)
	}
}

func TestOBDEventChannel_ContextCancel(t *testing.T) {
	// context キャンセルで goroutine が終了するパターン
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan OBDEvent, 1)

	go func() {
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case ch <- OBDEvent{
				Data:      &obd.OBDData{SpeedKmh: 60},
				Connected: true,
				ReadAt:    time.Now(),
			}:
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// いくつかイベントを受信
	received := 0
	timeout := time.After(200 * time.Millisecond)
loop:
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				break loop
			}
			received++
			if received >= 3 {
				cancel()
			}
		case <-timeout:
			break loop
		}
	}

	if received < 3 {
		t.Errorf("should receive at least 3 events before cancel, got %d", received)
	}

	// チャネルが閉じられるのを待つ
	time.Sleep(50 * time.Millisecond)
	_, ok := <-ch
	if ok {
		t.Error("channel should be closed after context cancel")
	}
}

func TestOBDEventChannel_DtSecCalculation(t *testing.T) {
	// dtSec の計算ロジックをテスト（メインループ側）
	now := time.Now()
	events := []OBDEvent{
		{Data: &obd.OBDData{SpeedKmh: 60}, Connected: true, ReadAt: now},
		{Data: &obd.OBDData{SpeedKmh: 62}, Connected: true, ReadAt: now.Add(150 * time.Millisecond)},
		{Data: &obd.OBDData{SpeedKmh: 65}, Connected: true, ReadAt: now.Add(320 * time.Millisecond)},
	}

	var lastReadAt time.Time
	defaultDtSec := 0.15 // 150ms

	for i, ev := range events {
		dtSec := defaultDtSec
		if !lastReadAt.IsZero() {
			dtSec = ev.ReadAt.Sub(lastReadAt).Seconds()
		}
		lastReadAt = ev.ReadAt

		switch i {
		case 0:
			// 初回はデフォルト値
			if dtSec != defaultDtSec {
				t.Errorf("event 0: dtSec=%.3f, want %.3f", dtSec, defaultDtSec)
			}
		case 1:
			// 2回目以降は実測
			if dtSec < 0.14 || dtSec > 0.16 {
				t.Errorf("event 1: dtSec=%.3f, expected ~0.15", dtSec)
			}
		case 2:
			// 3回目: 170msのはず
			if dtSec < 0.16 || dtSec > 0.18 {
				t.Errorf("event 2: dtSec=%.3f, expected ~0.17", dtSec)
			}
		}
	}
}

func TestOBDEventChannel_FilterResetOnReconnect(t *testing.T) {
	// 再接続時にフィルターがリセットされることを検証
	filters := newOBDFilters()

	// いくつかの値をフィルターに通す
	filters.speed.Update(60)
	filters.rpm.Update(2000)
	filters.load.Update(30)

	// 再接続検出でリセット（メインループのロジックを再現）
	wasConnected := true

	// 切断イベント
	ev := OBDEvent{Connected: false}
	if ev.Connected && !wasConnected {
		filters.ResetAll() // ここでは呼ばれない（false && true = false）
	}
	wasConnected = ev.Connected // false

	// 再接続イベント
	ev = OBDEvent{Connected: true, HasMAP: true}
	if ev.Connected && !wasConnected {
		filters.ResetAll() // ここで呼ばれる（true && !false = true）
	}
	_ = ev.Connected // wasConnected はこれ以降不要

	// リセット後は次の値がそのまま通る（スパイクフィルターなし）
	// 大きなジャンプもリジェクトされない
	result := filters.speed.Update(120)
	if result != 120 {
		t.Errorf("after reset, speed filter should accept 120: got %.0f", result)
	}
}
