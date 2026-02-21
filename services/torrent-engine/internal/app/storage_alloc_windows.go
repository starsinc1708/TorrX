//go:build windows

package app

import "os"

func fileAllocatedBytes(fileInfo os.FileInfo) int64 {
	if fileInfo == nil {
		return 0
	}
	size := fileInfo.Size()
	if size > 0 {
		return size
	}
	return 0
}
