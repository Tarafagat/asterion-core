//go:build !windows

package sysinfo

import "syscall"

// diskGB lee el uso de disco real vía syscall.Statfs (Linux y macOS).
func diskGB(path string) (usedGB, totalGB float64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	total := float64(stat.Blocks) * float64(stat.Bsize)
	free := float64(stat.Bavail) * float64(stat.Bsize)
	totalGB = total / 1024 / 1024 / 1024
	usedGB = (total - free) / 1024 / 1024 / 1024
	return usedGB, totalGB, nil
}
