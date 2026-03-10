package main

import "math"

// obdFilter はOBD値のスパイク除去 + EMA平滑化フィルター。
// 通信ノイズで値が飛ぶのを防ぐ。
type obdFilter struct {
	alpha       float64 // EMA係数（0-1、大きいほど追従が速い）
	maxDelta    float64 // 1サイクルの最大許容変動量
	value       float64 // 現在のスムーズ値
	valid       bool    // 初期化済みか
	rejectCount int     // 連続リジェクト回数
}

// 連続リジェクト上限。超えたら値が本当に変わったとみなして受入
const maxRejects = 3

func newOBDFilter(alpha, maxDelta float64) *obdFilter {
	return &obdFilter{alpha: alpha, maxDelta: maxDelta}
}

// Update は新しい値をフィルタリングして返す
func (f *obdFilter) Update(raw float64) float64 {
	if !f.valid {
		f.value = raw
		f.valid = true
		f.rejectCount = 0
		return raw
	}

	delta := math.Abs(raw - f.value)

	// スパイク除去: 変動が大きすぎたら拒否（連続上限超えで強制受入）
	if delta > f.maxDelta && f.rejectCount < maxRejects {
		f.rejectCount++
		return f.value
	}

	// EMA平滑化
	f.rejectCount = 0
	f.value = f.alpha*raw + (1-f.alpha)*f.value
	return f.value
}

// Reset はフィルターをリセットする（OBD再接続時に使用）
func (f *obdFilter) Reset() {
	f.valid = false
	f.rejectCount = 0
}
