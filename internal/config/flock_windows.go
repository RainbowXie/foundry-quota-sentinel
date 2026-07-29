//go:build windows

package config

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// lockFileFD takes an exclusive lock on the file using LockFileEx (Windows).
// It blocks until the lock is available. This is the real Windows cross-
// process lock, not a no-op: a concurrent process holding the lock makes this
// call block (LOCKFILE_EXCLUSIVE_LOCK, no LOCKFILE_FAIL_IMMEDIATELY).
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

// tryLockFileFD attempts an exclusive lock without blocking. Returns a non-nil
// error (so the caller treats it as "locked by another process") if the lock
// is held.
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

// unlockFileFD releases the Windows lock via UnlockFileEx. Close on the fd
// follows in fileLock.Close.
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
