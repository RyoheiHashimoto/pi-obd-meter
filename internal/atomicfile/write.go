// Package atomicfile はファイルのアトミック書き込みを提供する。
// 一時ファイルに書き込み→rename→fsync の3ステップで電源断時のファイル破損を防ぐ。
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write はdataをpathにアトミックに書き込む。
// tmp+rename+dir fsync で電源断時にも安全。
func Write(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	// ディレクトリの fsync で rename のメタデータをディスクに反映
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		dir.Close()
	}
	return nil
}
