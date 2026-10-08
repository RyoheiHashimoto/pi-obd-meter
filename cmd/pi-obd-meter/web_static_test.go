package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 設定ファイルから Web UI の配信元を変えられないこと。
//
// 変えられると、そのディレクトリが古いままでも必ず優先される。Pi の
// config.json は overlayfs の上層 (再起動で中身が戻る) を指しており、
// OTA でバイナリだけ新しくなっても画面が古いままだった (2026-09-24 に実機で確認)。
// 設定ファイルは git 管理外で OTA からも直せないため、ここで受け取らない。
func TestLoadConfig_IgnoresWebStaticDirFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"web_static_dir":"/opt/pi-obd-meter/web/static","max_speed_kmh":180}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("設定ファイルを置けない: %v", err)
	}

	cfg := loadConfig(path)
	if cfg.WebStaticDir != "" {
		t.Errorf("設定ファイルの web_static_dir が効いている: %q", cfg.WebStaticDir)
	}
	if cfg.MaxSpeedKmh != 180 {
		t.Errorf("他の項目まで読めていない: max_speed_kmh = %d", cfg.MaxSpeedKmh)
	}
}

// 既定では埋め込みの UI を配ること。
func TestBuildMux_ServesEmbeddedUI(t *testing.T) {
	h := testApp(t).buildMux()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/js/refuel.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("埋め込みの refuel.js が配れていない: status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "updateRefuelDialog") {
		t.Error("配られた refuel.js の中身が違う")
	}
}

// meter.html には、配ったバイナリの版が埋め込まれること。
//
// 画面はこの版を元にして、auto-update で版が変わったら読み込み直す。
// 埋め込まれていないと、読み込み後 30 秒以内に入れ替わった版を元として
// 記録してしまい、古い画面のまま気づかない (2026-10-02 に実車で発生)。
func TestBuildMux_MeterHTMLCarriesVersion(t *testing.T) {
	prev := version
	version = "dev-test123"
	defer func() { version = prev }()

	h := testApp(t).buildMux()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/meter.html", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("meter.html が配れていない: status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<meta name="build-version" content="dev-test123">`) {
		t.Error("meter.html に版が埋め込まれていない")
	}
	if strings.Contains(body, buildVersionPlaceholder) {
		t.Error("目印が置き換えられずに残っている")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	if !strings.Contains(body, `src="js/main.js"`) {
		t.Error("meter.html の本体が欠けている")
	}
}
