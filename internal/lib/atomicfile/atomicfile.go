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

	// Flush file contents to disk before the rename so a power loss cannot leave
	// a renamed-but-empty target (a real risk on the Raspberry Pi deployment).
	if err := tmpFile.Sync(); err != nil {
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

	if err := os.Rename(tmpName, target); err != nil {
		return err
	}

	// Persist the directory entry so the rename itself survives a power loss.
	return fsyncDir(dir)
}
