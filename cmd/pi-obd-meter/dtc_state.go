package main

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hashimoto/pi-obd-meter/internal/obd"
)

// dtcStore は読み取った故障コードを保持する。ゼロ値から使える。
//
// 故障コードは始動時に1回しか読まないので、リアルタイムデータには
// 載せず /api/dtc から取る。毎秒の JSON に同じ配列を積むのは無駄で、
// WebSocket の帯域も食う。
type dtcStore struct {
	mu      sync.RWMutex
	read    bool
	readAt  time.Time
	stored  []obd.DTC
	pending []obd.DTC
}

// DTCResponse は /api/dtc のレスポンス。
type DTCResponse struct {
	// Read が false なら「まだ読んでいない」。Stored が空でも
	// 異常なしとは限らないので、この2つを必ず区別すること。
	Read     bool      `json:"read"`
	ReadAt   string    `json:"read_at,omitempty"`
	Stored   []obd.DTC `json:"stored"`
	Pending  []obd.DTC `json:"pending"`
	MIL      bool      `json:"mil"`
	DTCCount int       `json:"dtc_count"`
}

// Update は読み取り結果を取り込む。中身が変わったときだけログに残す。
//
// stored が nil なら未読とみなして何もしない。空スライスは
// 「読んで 0 件」なので取り込む。
func (s *dtcStore) Update(stored, pending []obd.DTC) {
	if stored == nil && pending == nil {
		return
	}
	s.mu.Lock()
	changed := !s.read || !sameCodes(s.stored, stored) || !sameCodes(s.pending, pending)
	if stored != nil {
		s.stored = stored
	}
	if pending != nil {
		s.pending = pending
	}
	s.read = true
	s.readAt = time.Now()
	cur, pend := s.stored, s.pending
	s.mu.Unlock()

	if !changed {
		return
	}
	switch {
	case len(cur) == 0 && len(pend) == 0:
		slog.Info("故障コードなし")
	default:
		slog.Warn("故障コード検出",
			"確定", codeList(cur),
			"保留", codeList(pend))
	}
}

// Snapshot は現在の故障コードを返す。mil と count は PID 0x01 由来で、
// 呼び出し側がリアルタイムデータから渡す。
func (s *dtcStore) Snapshot(mil bool, count int) DTCResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resp := DTCResponse{
		Read:     s.read,
		Stored:   s.stored,
		Pending:  s.pending,
		MIL:      mil,
		DTCCount: count,
	}
	if resp.Stored == nil {
		resp.Stored = []obd.DTC{}
	}
	if resp.Pending == nil {
		resp.Pending = []obd.DTC{}
	}
	if s.read {
		resp.ReadAt = s.readAt.Format(time.RFC3339)
	}
	return resp
}

// sameCodes はコードの並びが同じかを返す。
func sameCodes(a, b []obd.DTC) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Code != b[i].Code {
			return false
		}
	}
	return true
}

// codeList はログ用にコードを並べる。空なら "なし"。
func codeList(codes []obd.DTC) string {
	if len(codes) == 0 {
		return "なし"
	}
	parts := make([]string, 0, len(codes))
	for _, c := range codes {
		parts = append(parts, c.Code+"("+c.Description+")")
	}
	return strings.Join(parts, ", ")
}

// aux22Map は未同定 Mode 22 PID の生値を JSON 向けに直す。
// キーは PID の4桁16進 ("1678" など)。
func aux22Map(m map[uint16]uint32) map[string]uint32 {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]uint32, len(m))
	for k, v := range m {
		out[fmt.Sprintf("%04X", k)] = v
	}
	return out
}
