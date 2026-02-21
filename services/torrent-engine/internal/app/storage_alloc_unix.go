//go:build !windows

package app

import (
	"os"
	"syscall"
)

func fileAllocatedBytes(fileInfo os.FileInfo) int64 {
	if fileInfo == nil {
		return 0
	}
	stat, ok := fileInfo.Sys().(*syscall.Stat_t)
	if ok && stat != nil {
		blocks := int64(stat.Blocks)
		if blocks > 0 {
			return blocks * 512
		}
	}
	size := fileInfo.Size()
	if size > 0 {
		return size
	}
	return 0
}
