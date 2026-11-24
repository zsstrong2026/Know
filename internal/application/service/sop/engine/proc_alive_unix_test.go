//go:build !windows

package engine_test

import (
	"errors"
	"strconv"
	"syscall"
)

// processAlive 用 kill -0 探测 pid：ESRCH 视为已退出。
func processAlive(_ interface{ Helper() }, pid string) bool {
	n, err := strconv.Atoi(pid)
	if err != nil || n <= 0 {
		return false
	}
	err = syscall.Kill(n, 0)
	if err == nil {
		return true
	}
	return !errors.Is(err, syscall.ESRCH)
}
