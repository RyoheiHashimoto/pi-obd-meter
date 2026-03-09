package atomicfile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWrite_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	data := []byte(`{"key": "value"}`)

	if err := Write(path, data, 0644); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

func TestWrite_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	oldData := []byte(`{"old": true}`)
	if err := Write(path, oldData, 0644); err != nil {
		t.Fatalf("first Write failed: %v", err)
	}

	newData := []byte(`{"new": true, "extra": 123}`)
	if err := Write(path, newData, 0644); err != nil {
		t.Fatalf("second Write failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !bytes.Equal(got, newData) {
		t.Errorf("got %q, want %q", got, newData)
	}
}

func TestWrite_Permission(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.dat")

	if err := Write(path, []byte("hello"), 0600); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	// ファイルパーミッションの下位9ビットを比較（umask の影響を除外）
	got := info.Mode().Perm()
	if got != 0600 {
		t.Errorf("permission: got %o, want 0600", got)
	}
}

func TestWrite_NoTmpLeftover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	if err := Write(path, []byte("data"), 0644); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf(".tmp file should not exist after successful write, got err=%v", err)
	}
}

func TestWrite_NonExistentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no", "such", "dir", "file.json")

	err := Write(path, []byte("data"), 0644)
	if err == nil {
		t.Fatal("expected error for non-existent directory, got nil")
	}
}

func TestWrite_EmptyData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.dat")

	if err := Write(path, []byte{}, 0644); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(got))
	}
}

func TestWrite_LargeData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.dat")

	data := bytes.Repeat([]byte("A"), 1024*1024) // 1MB
	if err := Write(path, data, 0644); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("data mismatch: got %d bytes, want %d bytes", len(got), len(data))
	}
}
