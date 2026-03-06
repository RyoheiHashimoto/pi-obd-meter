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
	flag.Parse()

	fmt.Println("========================================")
	fmt.Println("  DYデミオ OBD-2 PIDスキャナー")
	fmt.Println("========================================")
	fmt.Printf("ポート: %s\n\n", *portName)

	elm := obd.NewELM327(*portName)
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
		0x10: true, // MAF
		0x0B: true, // MAP
		0x04: true, // エンジン負荷
		0x0F: true, // 吸気温度
		0x2F: true, // 燃料レベル
		0x5E: true, // 燃料消費レート
	}

	hasMAF := false
	hasMAP := false
	hasFuelRate := false

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

		if pid == 0x10 {
			hasMAF = true
		}
		if pid == 0x0B {
			hasMAP = true
		}
		if pid == 0x5E {
			hasFuelRate = true
		}
	}

	fmt.Println(strings.Repeat("-", 50))
	fmt.Println("\n燃費計算方式の判定:")

	if hasFuelRate {
		fmt.Println("  ✓ 燃料消費レート(PID 0x5E)が使えます → 最も正確")
	} else if hasMAF {
		fmt.Println("  ✓ MAFセンサーが使えます → MAF方式で燃費計算")
	} else if hasMAP {
		fmt.Println("  ✓ MAPセンサーが使えます → Speed-Density方式で燃費計算（推定）")
	} else {
		fmt.Println("  ✗ MAF/MAPどちらも取得できません。燃費計算が困難です。")
	}

	// リアルタイムテスト
	fmt.Println("\nリアルタイムデータテスト（Ctrl+Cで終了）:")
	reader := obd.NewReader(elm, obd.EngineConfig{})
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
	if data.HasMAF {
		fmt.Printf("  MAF:    %.2f g/s\n", data.MAF)
	} else {
		fmt.Printf("  MAP:    %.0f kPa\n", data.IntakeManifold)
		fmt.Printf("  吸気温: %.0f ℃\n", data.IntakeAirTemp)
	}
	fmt.Printf("  燃料率: %.2f L/h\n", data.CalcFuelRateLph())
	fmt.Printf("  タンク: %.0f %%\n", data.FuelTankLevel)

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
