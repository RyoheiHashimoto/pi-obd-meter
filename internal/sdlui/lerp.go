package sdlui

import "math"

const (
	LerpSpeed     = 0.35
	LerpThreshold = 0.05
	LerpStop      = 0.01
)

// Lerp は現在値をターゲットに向けて補間する（1フレーム分）
func Lerp(current, target, speed float64) float64 {
	delta := target - current
	if math.Abs(delta) < LerpThreshold {
		return target
	}
	return current + delta*speed
}

// LerpDone は補間が完了したかを返す
func LerpDone(current, target float64) bool {
	return math.Abs(current-target) < LerpStop
}

// Clamp は値を min-max 範囲に制限する
func Clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
