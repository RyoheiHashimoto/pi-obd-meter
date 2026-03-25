//go:build !linux

package can

import "fmt"

// Socket はSocketCAN接続を管理する（非Linuxスタブ）
type Socket struct{}

// Open は非Linux環境ではエラーを返す
func Open(ifname string) (*Socket, error) {
	return nil, fmt.Errorf("CANソケットはLinuxのみ対応")
}

// ReadFrame は非Linux環境ではエラーを返す
func (s *Socket) ReadFrame() (Frame, error) {
	return Frame{}, fmt.Errorf("CANソケットはLinuxのみ対応")
}

// WriteFrame は非Linux環境ではエラーを返す
func (s *Socket) WriteFrame(f Frame) error {
	return fmt.Errorf("CANソケットはLinuxのみ対応")
}

// Close は非Linux環境では何もしない
func (s *Socket) Close() error {
	return nil
}
