package sidebar

import (
	"testing"
	"time"
)

func TestOllamaLoginLifecycleCloseWaitsAndPreventsDispatch(t *testing.T) {
	lifecycle := newOllamaLoginLifecycle()
	if !lifecycle.startValidation() {
		t.Fatal("startValidation() = false, want true")
	}

	release := make(chan struct{})
	dispatched := make(chan struct{}, 1)
	go func() {
		<-release
		lifecycle.finishValidation("session=value", func() { dispatched <- struct{}{} })
	}()

	closed := make(chan struct{})
	go func() {
		lifecycle.closeAndWait()
		close(closed)
	}()

	select {
	case <-closed:
		t.Fatal("closeAndWait() returned before validation finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-closed

	select {
	case <-dispatched:
		t.Fatal("dispatch ran after lifecycle closed")
	default:
	}
	if got := lifecycle.cookieValue(); got != "" {
		t.Fatalf("cookieValue() = %q, want empty after close", got)
	}
}
