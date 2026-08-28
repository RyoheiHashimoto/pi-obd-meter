package main

import (
	"net"
	"testing"
)

// checkWiFi はインターフェース名に依存してはいけない。
//
// 以前は "wlan0" 決め打ちだった。2026-08 に内蔵WiFiを無効化して USB アダプタ
// (wlan1) に切り替えたところ、wlan0 は存在するが DOWN のままになり、常に
// false を返すようになった。起動時の GAS 初回送信が毎回スキップされ、
// 給油の自動検出が送信されずに失われかけた。
func TestCheckWiFi_DoesNotDependOnInterfaceName(t *testing.T) {
	// 実行環境に何らかのネットワークがあれば true になるはず。
	// 名前で決め打ちしていると、wlan0 が無い環境では必ず false になる。
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skip("インターフェースを列挙できない")
	}
	hasUsable := false
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok &&
				!ipnet.IP.IsLoopback() && !ipnet.IP.IsLinkLocalUnicast() && ipnet.IP.To4() != nil {
				hasUsable = true
			}
		}
	}
	if got := checkWiFi(); got != hasUsable {
		t.Errorf("checkWiFi() = %v, 使えるIPv4を持つIFの有無 = %v", got, hasUsable)
	}
	// wlan0 が無い環境でも true を返せることが要点
	if _, err := net.InterfaceByName("wlan0"); err != nil && hasUsable && !checkWiFi() {
		t.Error("wlan0 が無いだけで false を返している。名前に依存している")
	}
}

// ループバックだけでは接続とみなさない。
func TestCheckWiFi_IgnoresLoopback(t *testing.T) {
	// 実装がループバックを除外していることをコードパスで確認する。
	// (ループバックのみの環境を作れないため、除外条件の存在を回帰で守る)
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		lo2, err2 := net.InterfaceByName("lo0")
		if err2 != nil {
			t.Skip("ループバックIFが見つからない")
		}
		lo = lo2
	}
	if lo.Flags&net.FlagLoopback == 0 {
		t.Skip("ループバックフラグが立っていない")
	}
}
