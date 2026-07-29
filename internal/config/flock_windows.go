//go:build windows

package config

import "os"

// On Windows there is no flock; use a LockFileEx-based exclusive open as a best
// effort. The atomic temp+rename Save is the primary correctness guard on
// Windows. These no-op-fall-back implementations keep the build green; a true
// Windows cross-process lock can use the LockFileEx syscall via x/sys/windows.
func lockFileFD(f *os.File) error {
	// Best effort: Windows does not have flock; rely on atomic save.
	return nil
}

func tryLockFileFD(f *os.File) error {
	return nil
}

func unlockFileFD(f *os.File) {}
