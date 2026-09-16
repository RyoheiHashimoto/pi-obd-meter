package can

import (
	"reflect"
	"testing"
)

func frame(id uint32, b ...byte) Frame {
	f := Frame{ID: id, DLC: uint8(len(b))}
	copy(f.Data[:], b)
	return f
}

// 単一フレーム: DTC 1件の Mode 03 応答。
func TestReassemblerSingleFrame(t *testing.T) {
	var r Reassembler
	payload, needFC := r.Push(frame(IDOBDResponse, 0x04, 0x43, 0x01, 0x01, 0x20, 0, 0, 0))
	if needFC {
		t.Fatal("単一フレームで Flow Control を求めた")
	}
	want := []byte{0x43, 0x01, 0x01, 0x20}
	if !reflect.DeepEqual(payload, want) {
		t.Errorf("payload = %X, want %X", payload, want)
	}
}

// 複数フレーム: DTC 4件。First Frame で Flow Control を求め、
// Consecutive Frame 2本で組み上がる。
func TestReassemblerMultiFrame(t *testing.T) {
	var r Reassembler

	// 全長 14 バイト: 43 06 + DTC 6件分 12 バイト。
	// First Frame が 6 バイト、Consecutive が 7 バイトずつ運ぶので
	// 連続フレームが 2 本必要になる長さ。
	payload, needFC := r.Push(frame(IDOBDResponse, 0x10, 0x0E, 0x43, 0x06, 0x01, 0x20, 0x03, 0x40))
	if !needFC {
		t.Fatal("First Frame で Flow Control を求めなかった。これを返さないと続きが来ない")
	}
	if payload != nil {
		t.Fatal("First Frame だけで payload を返した")
	}

	payload, _ = r.Push(frame(IDOBDResponse, 0x21, 0x01, 0x71, 0x04, 0x20, 0x03, 0x00, 0x01))
	if payload != nil {
		t.Fatal("まだ足りないのに payload を返した")
	}

	payload, _ = r.Push(frame(IDOBDResponse, 0x22, 0x35, 0, 0, 0, 0, 0, 0))
	want := []byte{
		0x43, 0x06,
		0x01, 0x20, 0x03, 0x40, 0x01, 0x71, 0x04, 0x20, 0x03, 0x00, 0x01, 0x35,
	}
	if !reflect.DeepEqual(payload, want) {
		t.Fatalf("payload = %X, want %X", payload, want)
	}
	// 申告した長さちょうどで切る。最後のフレームのパディングを
	// 混ぜると、末尾に P0000 のような幽霊コードが生える。
	if len(payload) != 14 {
		t.Errorf("長さ = %d, want 14", len(payload))
	}
}

// 連番が飛んだら組み立てを捨てる。詰めて返すと存在しないコードを作る。
func TestReassemblerSequenceGap(t *testing.T) {
	var r Reassembler
	r.Push(frame(IDOBDResponse, 0x10, 0x0B, 0x43, 0x04, 0x01, 0x20, 0x03, 0x40))

	if payload, _ := r.Push(frame(IDOBDResponse, 0x22, 0x01, 0x71, 0x04, 0x20, 0, 0, 0)); payload != nil {
		t.Fatal("連番が飛んだのに組み立てを続けた")
	}
	// 捨てたあとに正しい続きが来ても復活しない
	if payload, _ := r.Push(frame(IDOBDResponse, 0x21, 0x01, 0x71, 0x04, 0x20, 0, 0, 0)); payload != nil {
		t.Fatal("捨てたはずの組み立てが復活した")
	}
}

// Reset 後は組み立て途中の状態が残らない。
func TestReassemblerReset(t *testing.T) {
	var r Reassembler
	r.Push(frame(IDOBDResponse, 0x10, 0x0B, 0x43, 0x04, 0x01, 0x20, 0x03, 0x40))
	r.Reset()
	if payload, _ := r.Push(frame(IDOBDResponse, 0x21, 0x01, 0x71, 0x04, 0x20, 0, 0, 0)); payload != nil {
		t.Fatal("Reset 後に続きを受け付けた")
	}
}

// 壊れたフレームは黙って捨てる。
func TestReassemblerBadFrames(t *testing.T) {
	cases := []struct {
		name string
		f    Frame
	}{
		{"長さ0のSF", frame(IDOBDResponse, 0x00, 0, 0, 0, 0, 0, 0, 0)},
		{"DLCを超えるSF", frame(IDOBDResponse, 0x07, 0x43)},
		{"短すぎるFF", frame(IDOBDResponse, 0x10, 0x0B, 0x43)},
		{"7バイト以下と申告したFF", frame(IDOBDResponse, 0x10, 0x05, 0x43, 0, 0, 0, 0, 0)},
		{"組み立て前のCF", frame(IDOBDResponse, 0x21, 0x01, 0, 0, 0, 0, 0, 0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var r Reassembler
			if payload, needFC := r.Push(c.f); payload != nil || needFC {
				t.Errorf("payload=%X needFC=%v、どちらも空であるべき", payload, needFC)
			}
		})
	}
}

func TestOBDRequestFrameMode(t *testing.T) {
	f := OBDRequestFrameMode(ModeStoredDTC)
	if f.ID != IDOBDRequest {
		t.Errorf("ID = %X, want %X", f.ID, IDOBDRequest)
	}
	if f.Data[0] != 0x01 || f.Data[1] != 0x03 {
		t.Errorf("Data = %X, want 01 03 で始まる", f.Data)
	}
}

func TestFlowControlFrame(t *testing.T) {
	f := FlowControlFrame()
	// ECU 個別アドレスへ返す。7DF (ブロードキャスト) では届かない。
	if f.ID != IDOBDRequestECU {
		t.Errorf("ID = %X, want %X", f.ID, IDOBDRequestECU)
	}
	if f.Data[0] != 0x30 || f.Data[1] != 0x00 {
		t.Errorf("Data = %X, want 30 00 (ContinueToSend, BS=0)", f.Data)
	}
}

func TestDecodeRaw22(t *testing.T) {
	cases := []struct {
		data []byte
		want uint32
		ok   bool
	}{
		{[]byte{0x12}, 0x12, true},
		{[]byte{0x12, 0x34}, 0x1234, true},
		{[]byte{0x12, 0x34, 0x56, 0x78}, 0x12345678, true},
		{nil, 0, false},
		{[]byte{1, 2, 3, 4, 5}, 0, false},
	}
	for _, c := range cases {
		got, ok := DecodeRaw22(c.data)
		if got != c.want || ok != c.ok {
			t.Errorf("DecodeRaw22(%X) = %X,%v want %X,%v", c.data, got, ok, c.want, c.ok)
		}
	}
}

func TestIsProbe22(t *testing.T) {
	if !IsProbe22(PID22ShiftRangeA) {
		t.Error("未同定 PID が対象外になっている")
	}
	// 同定済みの PID は生値側に流さない。専用のデコーダがある。
	if IsProbe22(PID22ATFTemp) {
		t.Error("同定済みの ATF 油温が未同定扱いになっている")
	}
}
