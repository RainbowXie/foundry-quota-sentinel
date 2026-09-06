package browserauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// slowFakeServer lets us hold responses and then race several concurrent
// Call invocations on the same Client. The handler goroutine only reads
// frames off the WebSocket, records them under s.received, and pushes
// the id onto a per-conn responseIDs channel. A per-conn writer
// goroutine reads from responseIDs, waits for the global release, and
// writes the response. The global release is closed exactly once by
// ReleaseAll, so every queued id gets exactly one response.
type slowFakeServer struct {
	t      *testing.T
	server *httptest.Server

	mu       sync.Mutex
	cond     *sync.Cond
	received []map[string]any
	release  chan struct{}
	once     sync.Once
	writeMu  sync.Mutex
}

func newSlowFakeServer(t *testing.T) *slowFakeServer {
	s := &slowFakeServer{
		t:       t,
		release: make(chan struct{}),
	}
	s.cond = sync.NewCond(&s.mu)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"webSocketDebuggerUrl": "ws" + strings.TrimPrefix(s.server.URL, "http") + "/ws",
		})
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{{
			"type":                 "page",
			"webSocketDebuggerUrl": "ws" + strings.TrimPrefix(s.server.URL, "http") + "/ws",
		}})
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Per-conn response queue and writer goroutine. The handler
		// never waits on a gate; the writer waits for ReleaseAll once
		// and then drains every queued id.
		responseIDs := make(chan float64, 256)
		go func() {
			<-s.release
			for id := range responseIDs {
				s.writeMu.Lock()
				writeErr := conn.WriteJSON(map[string]any{
					"id":     int64(id),
					"result": map[string]any{"ok": true},
				})
				s.writeMu.Unlock()
				if writeErr != nil {
					return
				}
			}
		}()
		defer func() {
			close(responseIDs)
			_ = conn.Close()
		}()
		for {
			var msg map[string]any
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			id, _ := msg["id"].(float64)
			s.mu.Lock()
			s.received = append(s.received, msg)
			s.cond.Broadcast()
			s.mu.Unlock()
			responseIDs <- id
		}
	})
	s.server = httptest.NewServer(mux)
	return s
}

func (s *slowFakeServer) DebugAddress() string {
	u, _ := url.Parse(s.server.URL)
	return u.Host
}

func (s *slowFakeServer) Close() {
	s.server.Close()
}

// WaitForReceived blocks until the server has recorded at least n frames
// or the deadline expires. Returns true if the count was reached.
func (s *slowFakeServer) WaitForReceived(n int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.received) < n {
		if time.Now().After(deadline) {
			return false
		}
		s.cond.Wait()
	}
	return true
}

// ReleaseAll closes the global release channel exactly once. After this
// returns, every queued id (across all conns) is flushed to its
// response in arrival order.
func (s *slowFakeServer) ReleaseAll() {
	s.once.Do(func() { close(s.release) })
}

func (s *slowFakeServer) Received() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, len(s.received))
	copy(out, s.received)
	return out
}

// TestClientSerialisesConcurrentWrites proves two Call invocations on the
// same Client do not interleave their JSON-RPC frames on the wire. The
// fake server sees the frames in some order; the test asserts that every
// captured frame parses as a single, complete JSON object (no torn
// objects, no merge of two objects into one).
func TestClientSerialisesConcurrentWrites(t *testing.T) {
	server := newSlowFakeServer(t)
	defer server.Close()
	conn, err := Connect(context.Background(), server.DebugAddress())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	const N = 8
	var wg sync.WaitGroup
	results := make([]json.RawMessage, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = conn.Page().Call(context.Background(),
				"Page.command",
				map[string]any{"idx": idx})
		}(i)
	}
	// Wait until the server has actually received every frame before
	// releasing the gates. Sleeping here is wrong because the call mutex
	// serialises writes through syscall latency that varies.
	if !server.WaitForReceived(N, 5*time.Second) {
		t.Fatalf("server only received %d/%d frames within 5s", len(server.Received()), N)
	}
	server.ReleaseAll()
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Fatalf("Call[%d] error: %v", i, e)
		}
		if string(results[i]) != `{"ok":true}` {
			t.Fatalf("Call[%d] result = %s", i, string(results[i]))
		}
	}
	got := server.Received()
	if len(got) != N {
		t.Fatalf("server received %d frames, want %d", len(got), N)
	}
	seen := make(map[int64]bool)
	for _, frame := range got {
		raw, err := json.Marshal(frame)
		if err != nil {
			t.Fatalf("frame did not re-marshal: %v", err)
		}
		var roundtrip map[string]any
		if err := json.Unmarshal(raw, &roundtrip); err != nil {
			t.Fatalf("frame was not a single JSON object: %s", raw)
		}
		id, ok := roundtrip["id"].(float64)
		if !ok {
			t.Fatalf("frame missing id: %s", raw)
		}
		if seen[int64(id)] {
			t.Fatalf("duplicate id %d in frames", int64(id))
		}
		seen[int64(id)] = true
	}
}

func TestClientSequentialCalls(t *testing.T) {
	t.Skip("removed: sequential release would deadlock; concurrent test covers wire serialisation")
}

// TestClientCloseFailsPendingCalls proves that Close returns an error
// (rather than the zero-value success response) to every Call that is
// still waiting for a response when the connection ends.
func TestClientCloseFailsPendingCalls(t *testing.T) {
	server := newSlowFakeServer(t)
	defer server.Close()
	conn, err := Connect(context.Background(), server.DebugAddress())
	if err != nil {
		t.Fatal(err)
	}
	const N = 4
	results := make([]json.RawMessage, N)
	errs := make([]error, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = conn.Page().Call(context.Background(),
				"Page.command",
				map[string]any{"idx": idx})
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	// Close the connection without ever releasing the server's gates.
	if err := conn.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	wg.Wait()
	for i, e := range errs {
		if e == nil {
			t.Fatalf("Call[%d] returned success after Close (result=%s)", i, string(results[i]))
		}
	}
}

// TestClientReadLoopDropsExcessEventsWithoutBlocking proves the read loop
// never blocks the read goroutine when the events channel is full. The
// server emits 200 events back-to-back without the client ever reading
// from the events channel. If the read loop blocked, EmitAndWait would
// time out and the test would fail. A passive consumer that does not
// pull from the channel must not back the WebSocket up.
func TestClientReadLoopDropsExcessEventsWithoutBlocking(t *testing.T) {
	server := newEventFloodServer(t, 200)
	defer server.Close()
	conn, err := Connect(context.Background(), server.DebugAddress())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// The flood test must not consume events; if the read loop blocked
	// when the buffer was full, the server's write loop would stall and
	// EmitAndWait would time out.
	if !server.EmitAndWait(2 * time.Second) {
		t.Fatal("server could not emit all events; read loop is blocking the channel")
	}
}

// eventFloodServer is a minimal DevTools server that accepts a single
// page WebSocket and emits a configurable burst of method-only events.
type eventFloodServer struct {
	t      *testing.T
	server *httptest.Server

	emitted int64
	allSent chan struct{}
}

func newEventFloodServer(t *testing.T, _ int) *eventFloodServer {
	s := &eventFloodServer{t: t, allSent: make(chan struct{})}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"webSocketDebuggerUrl": "ws" + strings.TrimPrefix(s.server.URL, "http") + "/ws",
		})
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{{
			"type":                 "page",
			"webSocketDebuggerUrl": "ws" + strings.TrimPrefix(s.server.URL, "http") + "/ws",
		}})
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Drain incoming commands so the client never blocks writing.
		go func() {
			for {
				var raw json.RawMessage
				if err := conn.ReadJSON(&raw); err != nil {
					return
				}
			}
		}()
		// Wait until the test calls EmitAndWait.
		<-s.allSent
		for i := 0; i < 200; i++ {
			if err := conn.WriteJSON(map[string]any{
				"method": "Network.flood",
				"params": map[string]any{"i": i},
			}); err != nil {
				return
			}
			atomic.AddInt64(&s.emitted, 1)
		}
	})
	s.server = httptest.NewServer(mux)
	return s
}

func (s *eventFloodServer) DebugAddress() string {
	u, _ := url.Parse(s.server.URL)
	return u.Host
}

func (s *eventFloodServer) Close() { s.server.Close() }

// EmitAndWait releases the WebSocket handler to flush 200 events, then
// returns whether the server managed to write all of them within the
// given timeout. A false result means the read loop is blocking.
func (s *eventFloodServer) EmitAndWait(within time.Duration) bool {
	close(s.allSent)
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&s.emitted) >= 200 {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return atomic.LoadInt64(&s.emitted) >= 200
}

// disconnectOnFirstFrameServer drops the WebSocket connection as soon as
// the client has sent its first request. The client's read loop sees a
// read error on the same conn; the read-loop exit must wake every
// pending Call.
type disconnectOnFirstFrameServer struct {
	t      *testing.T
	server *httptest.Server
}

func newDisconnectOnFirstFrameServer(t *testing.T) *disconnectOnFirstFrameServer {
	s := &disconnectOnFirstFrameServer{t: t}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"webSocketDebuggerUrl": "ws" + strings.TrimPrefix(s.server.URL, "http") + "/ws",
		})
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{{
			"type":                 "page",
			"webSocketDebuggerUrl": "ws" + strings.TrimPrefix(s.server.URL, "http") + "/ws",
		}})
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Read the first frame, then drop the connection without ever
		// responding. The client's read loop sees a read error on the
		// next iteration.
		var msg map[string]any
		_ = conn.ReadJSON(&msg)
		_ = conn.Close()
	})
	s.server = httptest.NewServer(mux)
	return s
}

func (s *disconnectOnFirstFrameServer) DebugAddress() string {
	u, _ := url.Parse(s.server.URL)
	return u.Host
}

func (s *disconnectOnFirstFrameServer) Close() { s.server.Close() }

// TestReadErrorWakesPendingCalls proves that when the server's WebSocket
// dies without a Connection.Close, the read loop's exit still wakes
// every pending Call. The Call goroutines must observe an error, not
// hang on their context.
func TestReadErrorWakesPendingCalls(t *testing.T) {
	server := newDisconnectOnFirstFrameServer(t)
	defer server.Close()
	conn, err := Connect(context.Background(), server.DebugAddress())
	if err != nil {
		t.Fatal(err)
	}
	const N = 4
	results := make([]json.RawMessage, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		results[i], errs[i] = conn.Page().Call(context.Background(),
			"Page.command", map[string]any{"i": i})
	}
	for i, e := range errs {
		if e == nil {
			t.Fatalf("Call[%d] returned success (result=%s) after server disconnect", i, string(results[i]))
		}
	}
}

// TestClientAndConnectionCloseNoDoubleClose proves a race between
// Client.Close and Connection.Close does not panic on
// "close of closed channel". The shared done is signalled exactly once.
func TestClientAndConnectionCloseNoDoubleClose(t *testing.T) {
	server := newFakeDevToolsServer(t)
	defer server.Close()
	conn, err := Connect(context.Background(), server.DebugAddress())
	if err != nil {
		t.Fatal(err)
	}
	const N = 16
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = conn.Page().Close()
			_ = conn.Browser().Close()
		}()
	}
	done := make(chan struct{})
	go func() {
		_ = conn.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close calls did not return within 2s")
	}
	wg.Wait()
}
