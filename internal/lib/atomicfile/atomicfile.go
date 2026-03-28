// Package atomicfile provides atomic file write operations using write-to-temp-then-rename.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write atomically writes data to the target path. It writes to a temporary file
// in the same directory and renames it into place, ensuring the target is never
// left in a partially-written state. If perm is 0, the file permissions are not
// explicitly set (the OS default applies).
func Write(target string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(target)

	tmpFile, err := os.CreateTemp(dir, filepath.Base(target)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return err
	}

	if err := tmpFile.Close(); err != nil {
		return err
	}

	if perm != 0 {
		if err := os.Chmod(tmpName, perm); err != nil {
			return err
		}
	}

	return os.Rename(tmpName, target)
}
