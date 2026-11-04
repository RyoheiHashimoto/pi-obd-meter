package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 配っている設定テンプレートが、バイナリの設定と食い違っていないことを確かめる。
//
// セットアップ手順は configs/config.json.example をそのまま複製させる
// (docs/setup-guide.md)。テンプレートに古い値や消えた項目が残っていると、
// SD を作り直したときにそれが車へ入る。2026-11-04 の時点で実際に2つ残っていた:
//
//   - fuel_rate_correction: 1.3。8/31 に「出荷する config.json からキーを外す」と
//     決めた (TestLoadConfig_FuelCorrectionDefaultsWhenAbsent) のに、テンプレート
//     には届いていなかった。複製するとバイナリの既定 (1.12) を上書きし、燃料が
//     約16%多く出る
//   - brightness: 輝度の自動制御は 6/19 に削除済みだが、mac 用に残っていた
//
// 知らない項目は json.Unmarshal が黙って捨てるので、読み込みでは気づけない。
var configTemplates = []string{
	"config.json.example",
	"config.mac.json.example",
}

func readConfigTemplate(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "configs", name))
	if err != nil {
		t.Fatalf("テンプレートを読めない: %v", err)
	}
	return b
}

// テンプレートの項目はすべて Config に存在すること。
// 消した機能の設定がテンプレートに残るのを防ぐ。
func TestConfigTemplates_NoUnknownKeys(t *testing.T) {
	for _, name := range configTemplates {
		t.Run(name, func(t *testing.T) {
			dec := json.NewDecoder(bytes.NewReader(readConfigTemplate(t, name)))
			dec.DisallowUnknownFields()
			var cfg Config
			if err := dec.Decode(&cfg); err != nil {
				t.Errorf("Config に無い項目がある (消した機能の設定が残っていないか): %v", err)
			}
		})
	}
}

// 燃料モデルの一部である係数をテンプレートに書かないこと。
// 書くとバイナリの既定を上書きし、モデルを直しても車に届かない。
func TestConfigTemplates_NoFuelRateCorrection(t *testing.T) {
	for _, name := range configTemplates {
		t.Run(name, func(t *testing.T) {
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(readConfigTemplate(t, name), &raw); err != nil {
				t.Fatalf("JSON として読めない: %v", err)
			}
			if v, ok := raw["fuel_rate_correction"]; ok {
				t.Errorf("fuel_rate_correction (%s) がある。バイナリの既定 %.2f を上書きしてしまう",
					v, defaultFuelRateCorrection)
			}
		})
	}
}
