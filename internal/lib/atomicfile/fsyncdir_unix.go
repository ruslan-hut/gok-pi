//go:build !windows

package atomicfile

import "os"

// fsyncDir flushes a directory entry to disk. A failure to open the directory is
// ignored (some filesystems do not permit it); a sync failure is returned.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer d.Close()
	return d.Sync()
}
