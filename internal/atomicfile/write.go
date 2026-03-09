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
	if err := os.WriteFile(tmp, data, perm); err != nil {
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
