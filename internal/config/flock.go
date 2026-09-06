package config

import (
	"path/filepath"
	"sync"

	"foundry-quota-sentinel/pkg/sdk/store"
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

type fileLock = store.FileLock

func acquireLock(path string) (*fileLock, error) {
	return store.AcquireLock(path)
}

func tryLock(path string) (*fileLock, bool, error) {
	return store.TryLock(path)
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
