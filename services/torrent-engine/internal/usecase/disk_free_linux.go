//go:build linux || darwin

package usecase

import "syscall"

// diskFreeBytes returns the number of free bytes available on the filesystem
// containing the given path. Uses syscall.Statfs on Linux and macOS.
func diskFreeBytes(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	// Bavail * Bsize = free space available to unprivileged users.
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}
