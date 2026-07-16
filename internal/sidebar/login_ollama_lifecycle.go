package sidebar

import "sync"

// ollamaLoginLifecycle owns validation goroutines that may outlive the window's
// event loop. closeAndWait must complete before the native WebView is destroyed.
type ollamaLoginLifecycle struct {
	mu       sync.Mutex
	closed   bool
	inflight bool
	cookie   string
	wg       sync.WaitGroup
}

func newOllamaLoginLifecycle() *ollamaLoginLifecycle {
	return &ollamaLoginLifecycle{}
}

func (l *ollamaLoginLifecycle) startValidation() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.inflight || l.cookie != "" {
		return false
	}
	l.inflight = true
	l.wg.Add(1)
	return true
}

// finishValidation invokes onValid while holding the lifecycle lock. This
// keeps closeAndWait from allowing native teardown between the open check and
// dispatching termination to the WebView event loop.
func (l *ollamaLoginLifecycle) finishValidation(cookie string, onValid func()) {
	l.mu.Lock()
	l.inflight = false
	if !l.closed && cookie != "" && l.cookie == "" {
		l.cookie = cookie
		onValid()
	}
	l.mu.Unlock()
	l.wg.Done()
}

func (l *ollamaLoginLifecycle) closeAndWait() {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	l.wg.Wait()
}

func (l *ollamaLoginLifecycle) cookieValue() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cookie
}
