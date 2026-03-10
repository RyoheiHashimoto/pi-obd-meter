package main

import "testing"

func TestOBDFilter_FirstValue(t *testing.T) {
	f := newOBDFilter(20)
	got := f.Update(100)
	if got != 100 {
		t.Errorf("first value: got %f, want 100", got)
	}
}

func TestOBDFilter_SpikeRejection(t *testing.T) {
	f := newOBDFilter(20)
	f.Update(60) // 初期値

	// 急に200に飛ぶ → リジェクトされて60のまま
	got := f.Update(200)
	if got != 60 {
		t.Errorf("spike should be rejected: got %f, want 60", got)
	}
}

func TestOBDFilter_SpikeForceAccept(t *testing.T) {
	f := newOBDFilter(20)
	f.Update(60)

	// maxRejects(3)回リジェクトされた後、4回目で受入
	f.Update(200) // reject 1
	f.Update(200) // reject 2
	f.Update(200) // reject 3
	got := f.Update(200) // force accept
	if got != 200 {
		t.Errorf("should force accept after %d rejects: got %f, want 200", maxRejects, got)
	}
}

func TestOBDFilter_NormalDriving(t *testing.T) {
	f := newOBDFilter(20)

	// 60 → 65 → 70 → 75 : 通常加速、全部通る
	f.Update(60)
	v1 := f.Update(65)
	v2 := f.Update(70)
	v3 := f.Update(75)

	if v1 != 65 || v2 != 70 || v3 != 75 {
		t.Errorf("normal values should pass through: %f, %f, %f", v1, v2, v3)
	}
}

func TestOBDFilter_Reset(t *testing.T) {
	f := newOBDFilter(20)
	f.Update(100)
	f.Reset()

	// リセット後は新しい値をそのまま受入
	got := f.Update(200)
	if got != 200 {
		t.Errorf("after reset: got %f, want 200", got)
	}
}

func TestOBDFilter_RejectCounterReset(t *testing.T) {
	f := newOBDFilter(20)
	f.Update(60)

	// スパイク1回 → 正常値 → スパイク
	f.Update(200)        // reject 1
	f.Update(65)         // accept → rejectCount = 0
	got := f.Update(200) // reject 1 (カウンターリセット済み)
	if got != 65 {
		t.Errorf("reject counter should have reset: got %f, want 65", got)
	}
}
