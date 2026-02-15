//go:build !linux && !darwin

package usecase

import "errors"

// diskFreeBytes is a stub for non-Linux platforms. The production Docker image
// runs on Linux where the real implementation (disk_free_linux.go) is used.
func diskFreeBytes(path string) (int64, error) {
	return 0, errors.New("disk space check not supported on this platform")
}
