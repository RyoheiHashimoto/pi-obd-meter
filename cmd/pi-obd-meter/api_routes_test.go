package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testApp(t *testing.T) *App {
	t.Helper()
	cfg := Config{MaintenancePath: t.TempDir() + "/m.json"}
	return newApp(cfg)
}

// ルートの二重登録は起動時パニックになる。
//
// #137 で GET /api/health を既存と重複して登録し、実機にデプロイされて
// 初めて起動時パニックが判明した。auto-update のロールバックで救われたが、
// 検出がデプロイ後だったのは遅すぎる。
//
// net/http は登録時に panic するので、mux を組むだけで検出できる。
func TestBuildMux_NoRouteConflict(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ルート登録でパニックした: %v", r)
		}
	}()
	if mux := testApp(t).buildMux(); mux == nil {
		t.Fatal("mux が nil")
	}
}

// /api/health が Pi 本体の健全性を含むこと。
// 専用エンドポイントを足すのではなく既存の応答に含める方針 (#137 の反省)。
func TestHealthEndpoint_IncludesPiStatus(t *testing.T) {
	h := testApp(t).buildMux()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"pi"`, `"soc_temp_c"`, `"unclean_shutdowns"`} {
		if !strings.Contains(body, want) {
			t.Errorf("応答に %s が無い: %s", want, body)
		}
	}
}

// 主要なエンドポイントが登録されていること。
func TestBuildMux_CoreRoutes(t *testing.T) {
	h := testApp(t).buildMux()
	for _, path := range []string{"/api/realtime", "/api/health", "/api/config"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s が登録されていない", path)
		}
	}
}
