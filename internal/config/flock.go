package config

import (
	"os"
	"path/filepath"
	"sync"
)

// Cross-process file locking. The in-process configWriteMu serializes writers
// inside ONE process (the web server's card refresh vs window-save), but the
// CLI, the web server, and open-page run as SEPARATE processes that share the
// same config file. Without a cross-process lock, two processes doing
// Load→Mutate→Save concurrently can overwrite each other's freshly-rotated
// token with a stale snapshot. The file lock (flock on Unix) makes the
// Load→Save critical section transactional across processes too.

// lockPath returns the path of the global config write-lock file. It lives
// in the config directory (same filesystem as config.json so flock is
// enforced on the same mount; flock is per-inode, not per-path, but co-
// locating keeps it obvious). The file need not pre-exist — the lock helper
// creates it.
func lockPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.lock"), nil
}

// accountLockPath returns the per-account cross-process lock file for a Kimi
// account, serializing reload→refresh→persist for that account across
// processes (so two `quota-kimi <name>` runs, or a CLI + web request, cannot
// race the RefreshToken endpoint / double-rotate).
func accountLockPath(name string) (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	// Sanitize the name into a safe filename component so an account name
	// cannot escape the config dir or inject path separators.
	safe := sanitizeLockName(name)
	return filepath.Join(dir, "kimi-refresh-"+safe+".lock"), nil
}

func sanitizeLockName(name string) string {
	out := make([]byte, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, byte(r))
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		out = []byte("anon")
	}
	return string(out)
}

// fileLock is a held cross-process exclusive lock acquired via flock on Unix.
// On Windows it falls back to a shared-os.Rename-style open-exclusive (a best
// effort; the atomic-rename save is the primary guard there). Close releases
// the lock and removes the lock file best-effort.
type fileLock struct {
	f    *os.File
	path string
}

// acquireLock takes an exclusive cross-process lock on the given path. It
// blocks until the lock is available. The lock file (and its parent dir) is
// created if absent.
func acquireLock(path string) (*fileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockFileFD(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &fileLock{f: f, path: path}, nil
}

// tryLock attempts an exclusive cross-process lock without blocking; ok is
// false if another process holds it.
func tryLock(path string) (*fileLock, bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, err
	}
	if err := tryLockFileFD(f); err != nil {
		_ = f.Close()
		return nil, false, nil // locked by another process
	}
	return &fileLock{f: f, path: path}, true, nil
}

func (l *fileLock) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	// Release the flock and close the fd. Do NOT remove the lock file: a waiter
	// in another process may already be blocked in acquireLock on the SAME
	// inode; removing the file here would unlink that inode, and the waiter's
	// own OpenFile would create a NEW inode it then flocks — two separate locks,
	// no mutual exclusion (the classic flock inode race). The lock file is a
	// tiny persistent sentinel in the config dir; it is never large and is
	// recreated on demand if manually deleted.
	unlockFileFD(l.f)
	return l.f.Close()
}

// kimiAccountInProcLocks holds one in-process mutex per Kimi account name so
// concurrent refresh requests for the SAME account within ONE process
// serialize before touching the cross-process file lock. The map is guarded by
// kimiAccountInProcMu.
var (
	kimiAccountInProcMu    sync.Mutex
	kimiAccountInProcLocks = make(map[string]*sync.Mutex)
)

func getKimiAccountInProcLock(name string) *sync.Mutex {
	kimiAccountInProcMu.Lock()
	defer kimiAccountInProcMu.Unlock()
	mu, ok := kimiAccountInProcLocks[name]
	if !ok {
		mu = &sync.Mutex{}
		kimiAccountInProcLocks[name] = mu
	}
	return mu
}

// WithConfigLock runs fn while holding the global cross-process config write
// lock. It ALSO holds the in-process configWriteMu so a single process's
// concurrent writers serialize too. fn runs Load→mutate→Save inside; the
// caller must NOT call Mutate recursively from fn (it would self-deadlock on
// the in-process mutex — use the bare Load/Save inside fn instead).
func WithConfigLock(fn func(c *Config) error) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	lp, err := lockPath()
	if err != nil {
		return err
	}
	lk, err := acquireLock(lp)
	if err != nil {
		return err
	}
	defer lk.Close()
	c := Load()
	if err := fn(c); err != nil {
		return err
	}
	return c.Save()
}

// AcquireKimiAccountLock takes a cross-process exclusive lock for one Kimi
// account, serializing reload→refresh→persist for that account across
// processes (CLI quota-kimi vs web request vs open-page). The returned release
// func drops the lock; it must be called. It also holds an in-process
// per-account mutex so concurrent requests within ONE process serialize too.
func AcquireKimiAccountLock(name string) (release func(), err error) {
	// In-process per-account serialization first (fast path, no file I/O for
	// the common single-process web case).
	mu := getKimiAccountInProcLock(name)
	mu.Lock()
	lp, err := accountLockPath(name)
	if err != nil {
		mu.Unlock()
		return nil, err
	}
	lk, err := acquireLock(lp)
	if err != nil {
		mu.Unlock()
		return nil, err
	}
	return func() {
		_ = lk.Close()
		mu.Unlock()
	}, nil
}
