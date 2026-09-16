package can

import "testing"

// raw は km/h を 0x4B0 の生値に直す。
func rawSpeed(kmh float64) (hi, lo byte) {
	v := int(kmh*100) + 10000
	return byte(v >> 8), byte(v)
}

func wheelFrame(fl, fr, rl, rr float64) [8]byte {
	var d [8]byte
	d[0], d[1] = rawSpeed(fl)
	d[2], d[3] = rawSpeed(fr)
	d[4], d[5] = rawSpeed(rl)
	d[6], d[7] = rawSpeed(rr)
	return d
}

// 4輪が正しい位置に割り当たること。取り違えると前後差の符号が逆になり、
// ホイールスピンを「後輪が速い」と読んでしまう。
func TestDecodeWheelSpeed4Positions(t *testing.T) {
	fl, fr, rl, rr := DecodeWheelSpeed4(wheelFrame(10, 20, 30, 40))
	if fl != 10 || fr != 20 || rl != 30 || rr != 40 {
		t.Errorf("FL/FR/RL/RR = %.2f/%.2f/%.2f/%.2f, want 10/20/30/40", fl, fr, rl, rr)
	}
}

// 2026-09-16 の走行ログで実際に出た値をそのまま通す。
func TestDecodeWheelSpeed4RealSample(t *testing.T) {
	// 高速巡航中の定常状態。前輪がわずかに速い。
	fl, fr, rl, rr := DecodeWheelSpeed4(wheelFrame(120.94, 120.88, 119.82, 119.76))
	front := (fl + fr) / 2
	rear := (rl + rr) / 2
	d := front - rear
	if d < 0.5 || d > 1.5 {
		t.Errorf("前後差 = %.2f km/h, 実測の定常状態 0.5〜1.0 から外れている", d)
	}
}

// 分解能 0.01 km/h を保つこと。丸めるとスピンの立ち上がりが潰れる。
func TestDecodeWheelSpeed4Resolution(t *testing.T) {
	fl, _, _, _ := DecodeWheelSpeed4(wheelFrame(73.06, 73.06, 73.06, 73.06))
	if fl != 73.06 {
		t.Errorf("FL = %.4f, want 73.06", fl)
	}
}

// 生値が 10000 未満（負の車速）になったら 0 に倒す。
func TestDecodeWheelSpeed4ClipsNegative(t *testing.T) {
	var d [8]byte // 全部 0x0000 = raw 0 → -100 km/h
	fl, fr, rl, rr := DecodeWheelSpeed4(d)
	if fl != 0 || fr != 0 || rl != 0 || rr != 0 {
		t.Errorf("負の車速が 0 に倒れていない: %.2f/%.2f/%.2f/%.2f", fl, fr, rl, rr)
	}
}

// 平均は 4輪の単純平均のまま。車速として使っているので変えてはいけない。
func TestDecodeWheelSpeedStillAverages(t *testing.T) {
	d := wheelFrame(10, 20, 30, 40)
	if got := DecodeWheelSpeed(d); got != 25 {
		t.Errorf("平均 = %.2f, want 25", got)
	}
	// 個別と平均が食い違わないこと
	fl, fr, rl, rr := DecodeWheelSpeed4(d)
	if want := (fl + fr + rl + rr) / 4; DecodeWheelSpeed(d) != want {
		t.Errorf("平均 %.4f と個別の平均 %.4f が一致しない", DecodeWheelSpeed(d), want)
	}
}
