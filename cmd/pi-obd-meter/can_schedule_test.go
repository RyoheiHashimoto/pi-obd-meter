package main

import "testing"

// 応答と要求の対応づけ。
//
// タイムアウトのあとに前の要求の遅い応答が届いたとき、それで門を開けると
// 今の要求を待たずに次を送ってしまう。鍵が食い違えば開かないことを確かめる。
func TestReplyKeyMatching(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
		want    uint32
		ok      bool
	}{
		{"Mode01 MAF", []byte{0x41, 0x10, 0x01, 0x0B}, replyKey01(0x10), true},
		{"Mode01 電圧", []byte{0x41, 0x42, 0x34, 0xCF}, replyKey01(0x42), true},
		{"Mode22 ATF", []byte{0x62, 0x17, 0xB3, 0x50}, replyKey22(0x17B3), true},
		{"Mode22 ブレーキ", []byte{0x62, 0x11, 0x01, 0x01}, replyKey22(0x1101), true},
		{"確定した故障コード", []byte{0x43, 0x00}, replyKeyMode(0x43), true},
		{"保留の故障コード", []byte{0x47, 0x00}, replyKeyMode(0x47), true},
		{"否定応答は鍵を作れない", []byte{0x7F, 0x01, 0x12}, 0, false},
		{"空", nil, 0, false},
		{"Mode01 で PID が欠けている", []byte{0x41}, 0, false},
		{"Mode22 で PID が欠けている", []byte{0x62, 0x17}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := replyKey(c.payload)
			if ok != c.ok || (ok && got != c.want) {
				t.Errorf("replyKey(%X) = %06X,%v want %06X,%v", c.payload, got, ok, c.want, c.ok)
			}
		})
	}
}

// 3 つの鍵空間が重ならないこと。重なると別の応答で門が開く。
func TestReplyKeySpacesDoNotCollide(t *testing.T) {
	seen := map[uint32]string{}
	add := func(k uint32, what string) {
		if prev, dup := seen[k]; dup {
			t.Errorf("鍵 %06X が %s と %s で衝突", k, prev, what)
		}
		seen[k] = what
	}
	// Mode 01 は PID 全域、Mode 22 は実際に使うもの、Mode 03/07。
	for pid := 0; pid <= 0xFF; pid++ {
		add(replyKey01(byte(pid)), "Mode01")
	}
	for _, pid := range []uint16{0x17B3, 0x1101, 0x1103, 0x3201, 0x1678, 0x1104} {
		add(replyKey22(pid), "Mode22")
	}
	add(replyKeyMode(0x43), "Mode03")
	add(replyKeyMode(0x47), "Mode07")

	// 鍵 0 は「送らない」の印なので、誰も 0 を返してはいけない。
	if _, used := seen[0]; used {
		t.Error("鍵 0 が使われている。0 は送信しない印に予約している")
	}
}

// 送信スケジュールの配分を固定する。
//
// この表は「1 tick に 1 要求」を守るための唯一の仕掛けで、うっかり
// 1 行足すと周期が変わる。しかも壊れても例外は出ず、値の更新が遅くなる
// だけなので気づきにくい。意図した回数をここに書き留めておく。
func TestSendScheduleComposition(t *testing.T) {
	// intervalMs=50 のとき、20 スロットでちょうど 1 秒になる。
	if len(sendSchedule) != 20 {
		t.Fatalf("スロット数 = %d, want 20 (intervalMs=50 で 1 秒)", len(sendSchedule))
	}

	count := map[int]int{}
	for _, s := range sendSchedule {
		count[s]++
	}

	want := map[int]struct {
		n     int
		label string
	}{
		slotMAF:   {5, "MAF (200ms 周期)"},
		slotMAP:   {5, "MAP (200ms 周期)"},
		slotBrake: {4, "ブレーキ (250ms 周期)"},
		slotGrade: {1, "勾配 (1秒周期)"},
		slotAC:    {1, "エアコン (1秒周期)"},
		slotAux:   {2, "機関系 10個 → 5秒周期"},
		slotProbe: {1, "未同定 6個 → 6秒周期"},
		slotSlow:  {1, "電圧/ATF/DTC → 3秒周期"},
	}

	for slot, w := range want {
		if count[slot] != w.n {
			t.Errorf("%s: %d 回, want %d 回", w.label, count[slot], w.n)
		}
	}

	// 表に一度も出てこないスロットがあれば、その値は永久に取れない。
	// スロットを足して表に入れ忘れる事故を捕まえる。
	for slot := slotMAF; slot <= slotSlow; slot++ {
		if count[slot] == 0 {
			t.Errorf("スロット %d が表に無い。この要求は一度も送信されない", slot)
		}
	}

	// 合計が長さと一致しないなら、未知の値が紛れている。
	total := 0
	for _, n := range count {
		total += n
	}
	if total != len(sendSchedule) {
		t.Errorf("合計 %d, want %d", total, len(sendSchedule))
	}
}

// MAF と MAP は燃費計算の両輪なので、片方だけ間引かれていないか見る。
func TestSendScheduleFuelInputsBalanced(t *testing.T) {
	var maf, mapc int
	for _, s := range sendSchedule {
		switch s {
		case slotMAF:
			maf++
		case slotMAP:
			mapc++
		}
	}
	if maf != mapc {
		t.Errorf("MAF %d 回 / MAP %d 回。どちらかだけ間引くと燃費が偏る", maf, mapc)
	}
	// 燃費の入力が全体の半分を割ると、加減速時の追従が目に見えて鈍る。
	if maf+mapc < len(sendSchedule)/2 {
		t.Errorf("MAF+MAP が %d 回しかない (全 %d)。診断値を詰め込みすぎ", maf+mapc, len(sendSchedule))
	}
}
