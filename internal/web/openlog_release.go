//go:build !debuglog

package web

// openLogCapture keeps release builds free of open-page log files: the
// /api/open subprocess stdout/stderr capture is compiled out.
const openLogCapture = false
