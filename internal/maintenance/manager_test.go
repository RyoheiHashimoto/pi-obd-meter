package maintenance

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tempManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	return NewManager(filepath.Join(dir, "maintenance.json"))
}

func TestInitDefaults(t *testing.T) {
	m := tempManager(t)
	m.InitDefaults(nil) // ハードコードデフォルト

	all := m.GetAll()
	if len(all) != 5 {
		t.Fatalf("expected 5 reminders, got %d", len(all))
	}

	// IDで検索
	found := false
	for _, r := range all {
		if r.ID == "oil_change" {
			found = true
			if r.IntervalKm != 3000 {
				t.Errorf("oil_change interval: got %.0f, want 3000", r.IntervalKm)
			}
		}
	}
	if !found {
		t.Error("oil_change not found in defaults")
	}
}

func TestInitDefaultsFromConfig(t *testing.T) {
	m := tempManager(t)
	m.InitDefaults([]Reminder{
		{ID: "custom", Name: "カスタム", Type: TypeDistance, IntervalKm: 5000, WarningPct: 0.8},
	})

	all := m.GetAll()
	if len(all) != 1 {
		t.Fatalf("expected 1 reminder, got %d", len(all))
	}
	if all[0].ID != "custom" {
		t.Errorf("got ID %q, want custom", all[0].ID)
	}
}

func TestDistanceProgress(t *testing.T) {
	m := tempManager(t)
	m.InitDefaults([]Reminder{
		{ID: "oil", Name: "オイル", Type: TypeDistance, IntervalKm: 3000, WarningPct: 0.8},
	})

	// 2400km走行 → 80% → ちょうど警告閾値
	m.UpdateTotalKm(2400)
	statuses := m.CheckAll()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}

	s := statuses[0]
	if s.CurrentKm != 2400 {
		t.Errorf("CurrentKm: got %.0f, want 2400", s.CurrentKm)
	}
	if s.RemainingKm != 600 {
		t.Errorf("RemainingKm: got %.0f, want 600", s.RemainingKm)
	}
	if !s.NeedsAlert {
		t.Error("expected NeedsAlert at 80%")
	}
	if s.IsOverdue {
		t.Error("should not be overdue at 80%")
	}
}

func TestDistanceOverdue(t *testing.T) {
	m := tempManager(t)
	m.InitDefaults([]Reminder{
		{ID: "oil", Name: "オイル", Type: TypeDistance, IntervalKm: 3000, WarningPct: 0.8},
	})

	m.UpdateTotalKm(3500)
	s := m.CheckAll()[0]

	if !s.IsOverdue {
		t.Error("expected overdue at 3500km")
	}
	if s.RemainingKm >= 0 {
		t.Errorf("RemainingKm should be negative, got %.0f", s.RemainingKm)
	}
}

func TestDateProgress(t *testing.T) {
	past := time.Now().Add(-100 * 24 * time.Hour)
	m := tempManager(t)
	m.InitDefaults([]Reminder{
		{ID: "shaken", Name: "車検", Type: TypeDate,
			IntervalDays: 730, WarningPct: 0.9, LastResetAt: past},
	})

	s := m.CheckAll()[0]
	if s.DaysElapsed < 99 || s.DaysElapsed > 101 {
		t.Errorf("DaysElapsed: got %d, want ~100", s.DaysElapsed)
	}
	if s.DaysLeft < 629 || s.DaysLeft > 631 {
		t.Errorf("DaysLeft: got %d, want ~630", s.DaysLeft)
	}
	if s.NeedsAlert {
		t.Error("should not need alert at ~14% progress")
	}
}

func TestResetReminder(t *testing.T) {
	m := tempManager(t)
	m.InitDefaults([]Reminder{
		{ID: "oil", Name: "オイル", Type: TypeDistance, IntervalKm: 3000, WarningPct: 0.8},
	})

	m.UpdateTotalKm(2500)
	if !m.ResetReminder("oil") {
		t.Fatal("ResetReminder returned false")
	}

	// リセット後は0kmから
	s := m.CheckAll()[0]
	if s.CurrentKm != 0 {
		t.Errorf("after reset, CurrentKm: got %.0f, want 0", s.CurrentKm)
	}
	if s.NeedsAlert {
		t.Error("should not need alert after reset")
	}
}

func TestResetNonexistent(t *testing.T) {
	m := tempManager(t)
	m.InitDefaults(nil)

	if m.ResetReminder("nonexistent") {
		t.Error("expected false for nonexistent reminder")
	}
}

func TestGetAlerts(t *testing.T) {
	m := tempManager(t)
	m.InitDefaults([]Reminder{
		{ID: "oil", Name: "オイル", Type: TypeDistance, IntervalKm: 3000, WarningPct: 0.8},
		{ID: "atf", Name: "ATF", Type: TypeDistance, IntervalKm: 40000, WarningPct: 0.9},
	})

	// oilは警告域、atfは未到達
	m.UpdateTotalKm(2500)
	alerts := m.GetAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Reminder.ID != "oil" {
		t.Errorf("alert ID: got %q, want oil", alerts[0].Reminder.ID)
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "maintenance.json")

	// 作成して保存
	m1 := NewManager(path)
	m1.InitDefaults([]Reminder{
		{ID: "oil", Name: "オイル", Type: TypeDistance, IntervalKm: 3000, WarningPct: 0.8},
	})
	m1.UpdateTotalKm(1000)
	if !m1.ResetReminder("oil") {
		t.Fatal("ResetReminder returned false")
	}

	// 新しいManagerで読み込み
	m2 := NewManager(path)
	all := m2.GetAll()
	if len(all) != 1 {
		t.Fatalf("loaded %d reminders, want 1", len(all))
	}
	if all[0].LastResetKm != 1000 {
		t.Errorf("LastResetKm: got %.0f, want 1000", all[0].LastResetKm)
	}
}

func TestPersistenceFileNotFound(t *testing.T) {
	m := NewManager("/nonexistent/path/maintenance.json")
	m.InitDefaults(nil)
	// save失敗してもクラッシュしない
	all := m.GetAll()
	if len(all) != 5 {
		t.Fatalf("expected 5 defaults even with no file, got %d", len(all))
	}
}

func TestInitDefaultsPreservesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "maintenance.json")

	// 最初にoilを作成してリセット
	m1 := NewManager(path)
	m1.InitDefaults([]Reminder{
		{ID: "oil", Name: "オイル", Type: TypeDistance, IntervalKm: 3000, WarningPct: 0.8},
	})
	m1.UpdateTotalKm(1000)
	m1.ResetReminder("oil") // LastResetKm = 1000

	// 再読み込みしてInitDefaults → 既存のoilは上書きされない
	m2 := NewManager(path)
	m2.InitDefaults([]Reminder{
		{ID: "oil", Name: "オイル", Type: TypeDistance, IntervalKm: 3000, WarningPct: 0.8},
	})
	all := m2.GetAll()
	if all[0].LastResetKm != 1000 {
		t.Errorf("InitDefaults overwrote existing: LastResetKm got %.0f, want 1000", all[0].LastResetKm)
	}
}

func TestSaveFileCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "maintenance.json")

	m := NewManager(path)
	m.InitDefaults([]Reminder{
		{ID: "oil", Name: "オイル", Type: TypeDistance, IntervalKm: 3000, WarningPct: 0.8},
	})

	// ファイルが作成されたか確認
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("maintenance.json was not created")
	}
}
