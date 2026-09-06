//go:build windows

package store

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// lockFileFD 在 Windows 平台使用 LockFileEx 获取独占文件范围锁。
// LOCKFILE_EXCLUSIVE_LOCK 且不指定 FAIL_IMMEDIATELY 会在锁被持有时阻塞，提供跨进程互斥能力。
func lockFileFD(f *os.File) error {
	const flags = windows.LOCKFILE_EXCLUSIVE_LOCK
	var ol syscall.Overlapped
	if err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &windows.Overlapped{
		Internal:     ol.Internal,
		InternalHigh: ol.InternalHigh,
		Offset:       ol.Offset,
		OffsetHigh:   ol.OffsetHigh,
		HEvent:       windows.Handle(ol.HEvent),
	}); err != nil {
		return err
	}
	return nil
}

// tryLockFileFD 尝试以非阻塞方式获取 Windows 独占锁。
func tryLockFileFD(f *os.File) error {
	const flags = windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY
	var ol syscall.Overlapped
	if err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &windows.Overlapped{
		Internal:     ol.Internal,
		InternalHigh: ol.InternalHigh,
		Offset:       ol.Offset,
		OffsetHigh:   ol.OffsetHigh,
		HEvent:       windows.Handle(ol.HEvent),
	}); err != nil {
		return err
	}
	return nil
}

// unlockFileFD 释放 Windows 平台上的文件锁。
func unlockFileFD(f *os.File) {
	var ol syscall.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{
		Internal:     ol.Internal,
		InternalHigh: ol.InternalHigh,
		Offset:       ol.Offset,
		OffsetHigh:   ol.OffsetHigh,
		HEvent:       windows.Handle(ol.HEvent),
	})
}
