//go:build !windows

package store

import (
	"os"
	"syscall"
)

// lockFileFD 在 Unix 平台使用 syscall.Flock 对打开的文件句柄加排他锁。
// 使用 LOCK_EX 阻塞直到获取成功，为跨进程写入提供内核层原子保障。
func lockFileFD(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

// tryLockFileFD 尝试非阻塞加排他锁，如果已被持有则由内核返回 EWOULDBLOCK。
func tryLockFileFD(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// unlockFileFD 显式释放 Unix 上的 flock 锁。
func unlockFileFD(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
