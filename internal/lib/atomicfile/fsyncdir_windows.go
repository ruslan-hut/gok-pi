//go:build windows

package atomicfile

// fsyncDir is a no-op on Windows. Flushing a directory handle is not supported
// there — FlushFileBuffers on a directory fails with ERROR_ACCESS_DENIED — so
// attempting it would turn every atomic write into an error. The file contents
// are already fsynced before the rename, and NTFS journals the rename itself,
// so nothing is lost by skipping this step.
func fsyncDir(string) error { return nil }
