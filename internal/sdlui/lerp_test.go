package sdlui

import "testing"

func TestLerp(t *testing.T) {
	// 0 → 100, speed=0.35 → 1フレーム後は 35
	got := Lerp(0, 100, 0.35)
	if got < 34.9 || got > 35.1 {
		t.Errorf("Lerp(0, 100, 0.35) = %f, want ~35", got)
	}

	// 閾値以下ならスナップ
	got = Lerp(99.97, 100, 0.35)
	if got != 100 {
		t.Errorf("Lerp(99.97, 100, 0.35) = %f, want 100 (snap)", got)
	}
}

func TestLerpDone(t *testing.T) {
	if !LerpDone(100, 100.005) {
		t.Error("LerpDone(100, 100.005) should be true")
	}
	if LerpDone(100, 101) {
		t.Error("LerpDone(100, 101) should be false")
	}
}

func TestClamp(t *testing.T) {
	if Clamp(-5, 0, 180) != 0 {
		t.Error("Clamp(-5, 0, 180) should be 0")
	}
	if Clamp(200, 0, 180) != 180 {
		t.Error("Clamp(200, 0, 180) should be 180")
	}
	if Clamp(100, 0, 180) != 100 {
		t.Error("Clamp(100, 0, 180) should be 100")
	}
}
