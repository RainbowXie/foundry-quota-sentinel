package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"
)

var openSessionSeq int64

func newOpenSession() string {
	seq := atomic.AddInt64(&openSessionSeq, 1)
	return fmt.Sprintf("fqs-open-%d-%d", time.Now().UnixNano(), seq)
}

func openHandshakePath(session string) string {
	return filepath.Join(os.TempDir(), session+".json")
}

func openLogPath(session string) string {
	return filepath.Join(os.TempDir(), session+".log")
}

func WriteOpenHandshake(session, status, errMsg string) {
	if session == "" {
		return
	}
	path := openHandshakePath(session)
	data, _ := json.Marshal(map[string]any{"status": status, "error": errMsg})
	_ = os.WriteFile(path, data, 0o600)
}

func waitForOpenHandshake(session string, waitErr <-chan error, timeout time.Duration) (status, errMsg string, ok bool) {
	path := openHandshakePath(session)
	deadline := time.Now().Add(timeout)
	for {
		if data, err := os.ReadFile(path); err == nil {
			var h struct {
				Status string `json:"status"`
				Error  string `json:"error"`
			}
			if json.Unmarshal(data, &h) == nil && h.Status != "" {
				return h.Status, h.Error, true
			}
		}
		select {
		case err := <-waitErr:
			msg := "账户页子进程已退出"
			if err != nil {
				msg = err.Error()
			}
			return "error", msg, true
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return "", "", false
		}
	}
}

func (s *Server) registerOpenPageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/open", func(w http.ResponseWriter, r *http.Request) {
		provider := r.URL.Query().Get("provider")
		name := r.URL.Query().Get("name")
		if provider != "opencode" && provider != "deepseek" && provider != "ollama" && provider != "kimi" && provider != "commandcode" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "bad provider"})
			return
		}
		session := newOpenSession()
		spawn := s.spawnOpenPage
		if spawn == nil {
			spawn = func(p, n, sess string) (func() error, error) {
				exe, err := os.Executable()
				if err != nil {
					return nil, err
				}
				cmd := exec.Command(exe, "open-page", p, n)
				cmd.Env = append(os.Environ(), "FQS_OPEN_SESSION="+sess)
				var logFile *os.File
				if openLogCapture {
					logPath := openLogPath(sess)
					if f, logErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); logErr == nil {
						logFile = f
						cmd.Stdout = f
						cmd.Stderr = f
						log.Printf("open-page: 子进程日志写入 %s", logPath)
					}
				}
				if err := cmd.Start(); err != nil {
					if logFile != nil {
						_ = logFile.Close()
					}
					return nil, err
				}
				if logFile != nil {
					wait := cmd.Wait
					return func() error {
						defer logFile.Close()
						return wait()
					}, nil
				}
				return cmd.Wait, nil
			}
		}
		wait, err := spawn(provider, name, session)
		if err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		waitErr := make(chan error, 1)
		go func() { waitErr <- wait() }()
		timeout := s.openHandshakeTimeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		status, errMsg, ok := waitForOpenHandshake(session, waitErr, timeout)
		_ = os.Remove(openHandshakePath(session))
		if ok && status == "ready" {
			writeJSON(w, 200, map[string]any{"success": true})
			return
		}
		if ok && status == "error" {
			msg := errMsg
			if msg == "" {
				msg = "账户页子进程报告失败"
			}
			writeJSON(w, 200, map[string]any{"success": false, "error": msg})
			return
		}
		writeJSON(w, 200, map[string]any{"success": false, "error": "打开账户页超时：未收到就绪或错误信号"})
	})
}
