//go:build !windows
// +build !windows

package services

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// getDiskStats 获取 Unix/Linux/macOS 系统的磁盘统计信息
func getDiskStats(path string) (totalBytes int64, freeBytes int64, err error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return 0, 0, fmt.Errorf("abs path failed: %w", err)
	}

	var stat unix.Statfs_t
	if err := unix.Statfs(absPath, &stat); err != nil {
		return 0, 0, fmt.Errorf("statfs failed: %w", err)
	}

	totalBytes = int64(stat.Blocks) * int64(stat.Bsize)
	freeBytes = int64(stat.Bavail) * int64(stat.Bsize)
	return totalBytes, freeBytes, nil
}
