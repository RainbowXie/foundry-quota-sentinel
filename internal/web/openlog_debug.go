//go:build debuglog

package web

// openLogCapture enables the /api/open subprocess stdout/stderr capture
// (os.TempDir()/fqs-open-<session>.log) in debuglog builds only.
const openLogCapture = true
