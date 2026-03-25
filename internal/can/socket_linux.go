//go:build linux

package can

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// Socket はSocketCAN接続を管理する
type Socket struct {
	fd int
}

// Open は指定インターフェースでSocketCANを開く
func Open(ifname string) (*Socket, error) {
	fd, err := unix.Socket(unix.AF_CAN, unix.SOCK_RAW, unix.CAN_RAW)
	if err != nil {
		return nil, fmt.Errorf("CANソケット作成失敗: %w", err)
	}

	iface, err := net.InterfaceByName(ifname)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("インターフェース %s が見つかりません: %w", ifname, err)
	}

	addr := &unix.SockaddrCAN{Ifindex: iface.Index}
	if err := unix.Bind(fd, addr); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("CANソケットバインド失敗: %w", err)
	}

	// 読み取りタイムアウト: 1秒（CAN無通信検出用）
	tv := unix.Timeval{Sec: 1, Usec: 0}
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("受信タイムアウト設定失敗: %w", err)
	}

	return &Socket{fd: fd}, nil
}

// ReadFrame はCANフレームを1つ読み取る。
// タイムアウト時は ErrTimeout を返す。
func (s *Socket) ReadFrame() (Frame, error) {
	// CAN frame: 4(ID) + 1(DLC) + 3(pad) + 8(data) = 16 bytes
	var buf [16]byte
	n, err := unix.Read(s.fd, buf[:])
	if err != nil {
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			return Frame{}, ErrTimeout
		}
		if err == unix.EINTR {
			// シグナルで中断された場合はリトライ
			return Frame{}, ErrTimeout
		}
		return Frame{}, fmt.Errorf("CAN読み取りエラー: %w", err)
	}
	if n < 16 {
		return Frame{}, fmt.Errorf("不完全なCANフレーム: %d bytes", n)
	}

	var f Frame
	// CAN ID はリトルエンディアンで格納される
	f.ID = uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24
	f.ID &= 0x1FFFFFFF // EFF/RTR/ERR フラグを除去
	f.DLC = buf[4]
	copy(f.Data[:], buf[8:16])
	return f, nil
}

// WriteFrame はCANフレームを送信する（OBD-2クエリ用）
func (s *Socket) WriteFrame(f Frame) error {
	var buf [16]byte
	buf[0] = byte(f.ID)
	buf[1] = byte(f.ID >> 8)
	buf[2] = byte(f.ID >> 16)
	buf[3] = byte(f.ID >> 24)
	buf[4] = f.DLC
	copy(buf[8:16], f.Data[:])
	_, err := unix.Write(s.fd, buf[:])
	return err
}

// Close はソケットを閉じる
func (s *Socket) Close() error {
	return unix.Close(s.fd)
}
