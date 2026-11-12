//go:build windows
// +build windows

package services

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// getDiskStats 获取 Windows 系统的磁盘统计信息
func getDiskStats(path string) (totalBytes int64, freeBytes int64, err error) {
    absPath, err := filepath.Abs(path)
    if err != nil {
        return 0, 0, fmt.Errorf("abs path failed: %w", err)
    }

    volume := filepath.VolumeName(absPath)
    if volume == "" {
        volume = "C:"
    }
    volume = volume + string(filepath.Separator)

    var free, total, totalFree uint64
    err = windows.GetDiskFreeSpaceEx(windows.StringToUTF16Ptr(volume), &free, &total, &totalFree)
    if err != nil {
        return 0, 0, fmt.Errorf("GetDiskFreeSpaceEx failed: %w", err)
    }
    return int64(total), int64(free), nil
}
