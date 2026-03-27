package maintenance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func tempManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	return NewManager(filepath.Join(dir, "maintenance.json"), DefaultOilConfig())
}

func TestOilStatusGreen(t *testing.T) {
	m := tempManager(t)
	m.UpdateTotalKm(2000)

	s := m.OilStatus()
	if s.Alert != AlertGreen {
		t.Errorf("alert: got %q, want green", s.Alert)
	}
	if s.CurrentKm != 2000 {
		t.Errorf("CurrentKm: got %.0f, want 2000", s.CurrentKm)
	}
	if s.RemainingKm != 1000 {
		t.Errorf("RemainingKm: got %.0f, want 1000", s.RemainingKm)
	}
}

func TestOilStatusYellow(t *testing.T) {
	m := tempManager(t)
	m.UpdateTotalKm(2500)

	s := m.OilStatus()
	if s.Alert != AlertYellow {
		t.Errorf("alert: got %q, want yellow", s.Alert)
	}
}

func TestOilStatusOrange(t *testing.T) {
	m := tempManager(t)
	m.UpdateTotalKm(3000)

	s := m.OilStatus()
	if s.Alert != AlertOrange {
		t.Errorf("alert: got %q, want orange at interval", s.Alert)
	}
}

func TestOilStatusRed(t *testing.T) {
	m := tempManager(t)
	m.UpdateTotalKm(4000)

	s := m.OilStatus()
	if s.Alert != AlertRed {
		t.Errorf("alert: got %q, want red at danger", s.Alert)
	}
}

func TestResetOil(t *testing.T) {
	m := tempManager(t)
	m.UpdateTotalKm(2500)

	m.ResetOil()
	s := m.OilStatus()
	if s.CurrentKm != 0 {
		t.Errorf("after reset, CurrentKm: got %.0f, want 0", s.CurrentKm)
	}
	if s.Alert != AlertGreen {
		t.Errorf("after reset, alert: got %q, want green", s.Alert)
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "maintenance.json")

	m1 := NewManager(path, DefaultOilConfig())
	m1.UpdateTotalKm(1000)
	m1.ResetOil()

	m2 := NewManager(path, DefaultOilConfig())
	if m2.TotalKm() != 1000 {
		t.Errorf("TotalKm: got %.0f, want 1000", m2.TotalKm())
	}
	if m2.LastResetKm() != 1000 {
		t.Errorf("LastResetKm: got %.0f, want 1000", m2.LastResetKm())
	}
}

func TestPersistenceFileNotFound(t *testing.T) {
	m := NewManager("/nonexistent/path/maintenance.json", DefaultOilConfig())
	// save失敗してもクラッシュしない
	s := m.OilStatus()
	if s.Alert != AlertGreen {
		t.Errorf("expected green with no data, got %q", s.Alert)
	}
}

func TestMigrationFromOldFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "maintenance.json")

	// 旧フォーマットのデータを書き込み
	old := map[string]interface{}{
		"total_km": 98500.0,
		"reminders": map[string]interface{}{
			"oil_change": map[string]interface{}{
				"id":            "oil_change",
				"name":          "エンジンオイル交換",
				"type":          "distance",
				"interval_km":   3000,
				"warning_pct":   0.8,
				"last_reset_km": 97000,
			},
		},
	}
	data, _ := json.MarshalIndent(old, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(path, DefaultOilConfig())
	if m.TotalKm() != 98500 {
		t.Errorf("TotalKm: got %.0f, want 98500", m.TotalKm())
	}
	if m.LastResetKm() != 97000 {
		t.Errorf("LastResetKm: got %.0f, want 97000", m.LastResetKm())
	}

	s := m.OilStatus()
	if s.CurrentKm != 1500 {
		t.Errorf("CurrentKm: got %.0f, want 1500", s.CurrentKm)
	}
}
