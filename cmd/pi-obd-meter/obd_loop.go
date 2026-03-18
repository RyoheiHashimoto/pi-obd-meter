package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/hashimoto/pi-obd-meter/internal/obd"
)

// OBDEvent は goroutine からメインループに送信されるOBDデータイベント
type OBDEvent struct {
	Data      *obd.OBDData // nil = 未接続/エラー
	IsFull    bool         // ReadAll の結果か
	Connected bool         // OBD 接続状態
	HasMAF    bool
	HasMAP    bool
	ReadAt    time.Time // 実際の読み取り時刻（dtSec 計算用）
}

// obdReaderLoop はOBD読み取りを専用 goroutine で実行する。
// 接続・再接続・ポーリング・エラーカウントをすべて内部管理し、
// 結果を ch に送信する。ctx がキャンセルされると ch を閉じて終了する。
func obdReaderLoop(ctx context.Context, elm *obd.ELM327, intervalMs int, ch chan<- OBDEvent) {
	defer close(ch)

	if intervalMs <= 0 {
		intervalMs = 150
	}

	const (
		fullEveryN         = 5
		maxConsecErrors    = 10
		reconnectInterval  = 10 * time.Second
		disconnectedPollMs = 1000 // 未接続時のステータス送信間隔
	)

	var (
		reader      *obd.Reader
		connected   bool
		hasMAF      bool
		hasMAP      bool
		errCount    int
		sampleCount int
	)

	// tryConnect はELM327への接続を試みる
	tryConnect := func() bool {
		slog.Info("ELM327接続試行中...")
		if err := elm.Connect(); err != nil {
			slog.Warn("ELM327接続失敗", "error", err)
			return false
		}
		r := obd.NewReader(elm)
		if err := r.DetectCapabilities(); err != nil {
			slog.Warn("OBDケイパビリティ検出失敗", "error", err)
			_ = elm.Close()
			return false
		}
		reader = r
		hasMAF = r.HasMAF()
		hasMAP = r.HasMAP()
		connected = true
		errCount = 0
		sampleCount = 0
		slog.Info("ELM327接続完了")
		return true
	}

	// 初回接続
	if !tryConnect() {
		slog.Warn("OBD未接続、メーター表示のみで起動（バックグラウンドでリトライ）")
	}

	ticker := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
	defer ticker.Stop()

	reconnectTicker := time.NewTicker(reconnectInterval)
	defer reconnectTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			if connected {
				_ = elm.Close()
			}
			return

		case <-ticker.C:
			if !connected {
				continue
			}

			sampleCount++
			isFull := sampleCount%fullEveryN == 0

			var data *obd.OBDData
			var err error
			if isFull {
				data, err = reader.ReadAll()
			} else {
				data, err = reader.ReadFast()
			}

			now := time.Now()

			if err != nil {
				errCount++
				if errCount >= maxConsecErrors {
					slog.Warn("OBD接続ロスト、リトライ待ち", "consecutive_errors", errCount)
					connected = false
					_ = elm.Close()
					errCount = 0
					// 切断通知を送信
					select {
					case ch <- OBDEvent{Connected: false, ReadAt: now}:
					case <-ctx.Done():
						return
					}
				}
				continue
			}
			errCount = 0

			select {
			case ch <- OBDEvent{
				Data:      data,
				IsFull:    isFull,
				Connected: true,
				HasMAF:    hasMAF,
				HasMAP:    hasMAP,
				ReadAt:    now,
			}:
			case <-ctx.Done():
				_ = elm.Close()
				return
			}

		case <-reconnectTicker.C:
			if connected {
				continue
			}
			if tryConnect() {
				// 再接続通知を送信
				select {
				case ch <- OBDEvent{
					Connected: true,
					HasMAF:    hasMAF,
					HasMAP:    hasMAP,
					ReadAt:    time.Now(),
				}:
				case <-ctx.Done():
					_ = elm.Close()
					return
				}
			} else {
				// 未接続ステータス更新
				select {
				case ch <- OBDEvent{Connected: false, ReadAt: time.Now()}:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}
