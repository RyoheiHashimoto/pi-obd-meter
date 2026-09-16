package can

// ISO-TP (ISO 15765-2) の受信。
//
// Mode 01 と Mode 22 の応答は 7 バイトに収まるので単一フレームで済むが、
// Mode 03 (故障コード) は件数しだいで複数フレームに分かれる。DTC が 2 件
// までなら単一フレーム、3 件以上で分割される。分割された応答は
//
//	1本目 (First Frame)      10 LL <data 6 bytes>     LL = 全体長
//	  ← こちらが Flow Control を返すまで ECU は続きを送らない
//	2本目以降 (Consecutive)  2N <data 7 bytes>        N = 1,2,...,F,0,1,...
//
// という形で来る。Flow Control を返さないと 2 本目が永久に来ないため、
// 「DTC が 2 件までは読めるのに 3 件以上だと無言」という分かりにくい
// 壊れ方をする。

// maxISOTPLen は組み立てを許す最大長 (ISO-TP の仕様上限)。
// これを超える長さを申告するフレームは壊れているとみなして捨てる。
const maxISOTPLen = 4095

// isotpSTmin は Flow Control で指定する連続フレームの最小間隔 (ミリ秒)。
// 0 (間隔なし) でも規格上は正しいが、受信を取りこぼしたときに
// 原因の切り分けが難しくなるので 10ms 空けさせる。
// DTC は起動時に1回しか読まないので、遅くても困らない。
const isotpSTmin = 0x0A

// Reassembler は1つの応答アドレスについて ISO-TP の応答を組み立てる。
// ゼロ値から使える。goroutine 安全ではない。
type Reassembler struct {
	buf     []byte
	want    int
	nextSeq byte
	active  bool
}

// Push は応答フレームを1つ食わせる。
//
// payload は組み上がったときだけ返る (単一フレームならその場で返る)。
// needFC が true のときは、呼び出し側が FlowControlFrame() を送らないと
// 続きのフレームが来ない。
func (r *Reassembler) Push(f Frame) (payload []byte, needFC bool) {
	if f.DLC < 1 || f.DLC > 8 {
		return nil, false
	}
	switch f.Data[0] >> 4 {
	case 0x0: // Single Frame
		n := int(f.Data[0] & 0x0F)
		if n == 0 || n > 7 || n+1 > int(f.DLC) {
			return nil, false
		}
		r.reset()
		out := make([]byte, n)
		copy(out, f.Data[1:1+n])
		return out, false

	case 0x1: // First Frame
		if f.DLC < 8 {
			return nil, false
		}
		n := int(f.Data[0]&0x0F)<<8 | int(f.Data[1])
		if n <= 7 || n > maxISOTPLen {
			return nil, false
		}
		r.buf = make([]byte, 0, n)
		r.buf = append(r.buf, f.Data[2:8]...)
		r.want = n
		r.nextSeq = 1
		r.active = true
		return nil, true

	case 0x2: // Consecutive Frame
		if !r.active {
			return nil, false
		}
		// 順番が飛んだら組み立てを捨てる。途中が欠けた列を
		// 詰めて返すと、存在しない故障コードを作ってしまう。
		if f.Data[0]&0x0F != r.nextSeq {
			r.reset()
			return nil, false
		}
		r.nextSeq = (r.nextSeq + 1) & 0x0F
		r.buf = append(r.buf, f.Data[1:f.DLC]...)
		if len(r.buf) >= r.want {
			out := r.buf[:r.want]
			r.reset()
			return out, false
		}
	}
	return nil, false
}

// Reset は組み立て中の状態を捨てる。CAN 再接続時に呼ぶ。
func (r *Reassembler) Reset() { r.reset() }

func (r *Reassembler) reset() {
	r.buf = nil
	r.want = 0
	r.nextSeq = 0
	r.active = false
}

// FlowControlFrame は「続きを全部送ってよい」と伝えるフレームを作る。
// BS=0 は分割なしで最後まで送る指示。
func FlowControlFrame() Frame {
	return Frame{
		ID:  IDOBDRequestECU,
		DLC: 8,
		Data: [8]byte{
			0x30,       // Flow Control / ContinueToSend
			0x00,       // BS = 0 (最後まで連続で送ってよい)
			isotpSTmin, // STmin
			0x00, 0x00, 0x00, 0x00, 0x00,
		},
	}
}

// OBD-2 のモード。Mode 01 と 22 は PID を取るので別関数を使う。
const (
	ModeStoredDTC  byte = 0x03 // 確定した故障コード
	ModePendingDTC byte = 0x07 // 保留中 (1 回目の検出で、まだ確定していない)
	ModeClearDTC   byte = 0x04 // 消去。メーターからは絶対に送らない
)

// OBDRequestFrameMode は PID を取らないモード (03/07) のリクエストを作る。
//
// Mode 04 (消去) はここから送れるが、送ってはいけない。整備の履歴が
// 消えるうえ、レディネスモニタが未完了に戻って車検が通らなくなる。
func OBDRequestFrameMode(mode byte) Frame {
	return Frame{
		ID:  IDOBDRequest,
		DLC: 8,
		Data: [8]byte{
			0x01, // データバイト数 (mode のみ)
			mode,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		},
	}
}
