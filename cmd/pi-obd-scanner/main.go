// pi-obd-scanner は車両の対応PIDスキャン・リアルタイムデータ確認・DTC読み取りを行う診断ツール。
// メインアプリ (pi-obd-meter) の導入前に、車両との通信互換性を確認するために使用する。
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/hashimoto/pi-obd-meter/internal/obd"
)

// PID名マッピング
var pidNames = map[byte]string{
	0x04: "エンジン負荷 (%)",
	0x05: "冷却水温度 (℃)",
	0x06: "短期燃料トリム Bank1 (%)",
	0x07: "長期燃料トリム Bank1 (%)",
	0x0B: "インマニ絶対圧 (kPa)",
	0x0C: "エンジン回転数 (rpm)",
	0x0D: "車速 (km/h)",
	0x0E: "点火時期 (° BTDC)",
	0x0F: "吸気温度 (℃)",
	0x10: "MAFエアフロー (g/s)",
	0x11: "スロットル位置 (%)",
	0x1C: "OBD準拠規格",
	0x1F: "エンジン始動後時間 (s)",
	0x21: "MILオン後の走行距離 (km)",
	0x2F: "燃料タンクレベル (%)",
	0x33: "大気圧 (kPa)",
	0x46: "外気温 (℃)",
	0x5C: "エンジンオイル温度 (℃)",
	0x5E: "燃料消費レート (L/h)",
}

func main() {
	portName := flag.String("port", "/dev/ttyUSB0", "ELM327のシリアルポート")
	protocol := flag.String("protocol", "6", "OBDプロトコル番号 (0=自動, 6=CAN 11bit 500kbaud)")
	flag.Parse()

	fmt.Println("========================================")
	fmt.Println("  DYデミオ OBD-2 PIDスキャナー")
	fmt.Println("========================================")
	fmt.Printf("ポート: %s\n\n", *portName)

	elm := obd.NewELM327(*portName, *protocol)
	if err := elm.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "接続エラー: %v\n", err)
		os.Exit(1)
	}
	defer elm.Close()
	fmt.Println("✓ ELM327接続完了")

	fmt.Println("対応PIDをスキャン中...")
	supported, err := elm.ScanSupportedPIDs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "スキャンエラー: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n対応PID: %d 個\n", len(supported))
	fmt.Println(strings.Repeat("-", 50))

	essential := map[byte]bool{
		0x0C: true, // RPM
		0x0D: true, // 車速
		0x04: true, // エンジン負荷
		0x11: true, // スロットル
		0x05: true, // 冷却水温度
	}

	for _, pid := range supported {
		name, known := pidNames[pid]
		marker := "  "
		if essential[pid] {
			marker = "★"
		}
		if !known {
			name = "(不明)"
		}
		fmt.Printf("  %s PID 0x%02X: %s\n", marker, pid, name)
	}

	fmt.Println(strings.Repeat("-", 50))

	// リアルタイムテスト
	fmt.Println("\nリアルタイムデータテスト（Ctrl+Cで終了）:")
	reader := obd.NewReader(elm)
	_ = reader.DetectCapabilities()

	data, err := reader.ReadAll()
	if err != nil {
		fmt.Printf("読み取りエラー: %v\n", err)
		return
	}

	fmt.Printf("  RPM:    %.0f rpm\n", data.RPM)
	fmt.Printf("  車速:   %.0f km/h\n", data.SpeedKmh)
	fmt.Printf("  負荷:   %.1f %%\n", data.EngineLoad)
	fmt.Printf("  冷却水: %.0f ℃\n", data.CoolantTemp)
	fmt.Printf("  スロットル: %.1f %%\n", data.ThrottlePos)

	// === 故障コード（DTC）読み取り ===
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("  故障コード (DTC) チェック")
	fmt.Println(strings.Repeat("=", 50))

	dtcResult, err := elm.ReadDTCs()
	if err != nil {
		fmt.Printf("  DTC読み取りエラー: %v\n", err)
		return
	}

	if dtcResult.MIL {
		fmt.Println("  ⚠ チェックランプ: 点灯中")
	} else {
		fmt.Println("  ✓ チェックランプ: 消灯")
	}
	fmt.Printf("  記録コード数: %d\n", dtcResult.DTCCount)

	if len(dtcResult.Codes) == 0 {
		fmt.Println("  ✓ 故障コードなし — 正常です")
	} else {
		fmt.Println()
		for _, dtc := range dtcResult.Codes {
			icon := "⚠"
			if dtc.Severity == "critical" {
				icon = "🔴"
			} else if dtc.Severity == "info" {
				icon = "ℹ"
			}
			fmt.Printf("  %s %s: %s\n", icon, dtc.Code, dtc.Description)
		}
		fmt.Println("\n  ※ コードのクリアはディーラーまたはTorqueアプリで行えます")
	}
}
