package sdlui

import "math"

const (
	LerpSpeed     = 0.35
	LerpThreshold = 0.05
	LerpStop      = 0.01
	lerpRefDt     = 1.0 / 60.0 // 基準フレーム時間 (60fps)
)

// Lerp は現在値をターゲットに向けて補間する（1フレーム分、固定フレームレート用）
func Lerp(current, target, speed float64) float64 {
	delta := target - current
	if math.Abs(delta) < LerpThreshold {
		return target
	}
	return current + delta*speed
}

// LerpDt は delta time ベースで補間する（フレームレート非依存）
// speed は 60fps 基準の係数、dt は実フレーム時間（秒）
func LerpDt(current, target, speed, dt float64) float64 {
	delta := target - current
	if math.Abs(delta) < LerpThreshold {
		return target
	}
	// 60fps で speed=0.35 と同じ動きを任意の dt で再現
	// 1フレームで (1-speed) 残る → t秒で (1-speed)^(t/refDt) 残る
	factor := 1 - math.Pow(1-speed, dt/lerpRefDt)
	return current + delta*factor
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
