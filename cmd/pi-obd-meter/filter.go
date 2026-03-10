package main

import "math"

// obdFilter はOBD値のスパイク除去フィルター。
// 通信ノイズで値が飛ぶのを防ぐ。
type obdFilter struct {
	maxDelta    float64 // 1サイクルの最大許容変動量
	value       float64 // 現在の値
	valid       bool    // 初期化済みか
	rejectCount int     // 連続リジェクト回数
}

// 連続リジェクト上限。超えたら値が本当に変わったとみなして受入
const maxRejects = 3

func newOBDFilter(maxDelta float64) *obdFilter {
	return &obdFilter{maxDelta: maxDelta}
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

	// スパイク通過 → 値を更新
	f.rejectCount = 0
	f.value = raw
	return f.value
}

// Reset はフィルターをリセットする（OBD再接続時に使用）
func (f *obdFilter) Reset() {
	f.valid = false
	f.rejectCount = 0
}
