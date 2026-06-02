package web

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"ocgt-monitor/internal/quota"
	"ocgt-monitor/internal/storage"
)

//go:embed static/*
var webAssets embed.FS

type Server struct {
	addr     string
	querier  *quota.OpenCodeGoQuerier
	deepseek *quota.DeepSeekQuerier
}

func NewServer(q *quota.OpenCodeGoQuerier) *Server {
	return &Server{addr: ":8788", querier: q, deepseek: quota.NewDeepSeekQuerier()}
}

func logDir() string {
	h, err := os.UserHomeDir()
	if err != nil { h = os.Getenv("USERPROFILE") }
	return filepath.Join(h, ".ocgt", "history")
}

func (s *Server) Start(addr string) error {
	if addr != "" { s.addr = addr }
	mux := http.NewServeMux()
	mux.HandleFunc("/api/quota", func(w http.ResponseWriter, r *http.Request) {
		d, e := s.querier.FetchQuota()
		if e != nil { writeJSON(w, 200, map[string]any{"success": false, "error": e.Error()}); return }
		writeJSON(w, 200, map[string]any{"success": true, "data": d})
	})
	mux.HandleFunc("/api/balance", func(w http.ResponseWriter, r *http.Request) {
		d, e := s.deepseek.FetchBalance()
		if e != nil { writeJSON(w, 200, map[string]any{"success": false, "error": e.Error()}); return }
		writeJSON(w, 200, map[string]any{"success": true, "data": d})
	})
	mux.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		logs, e := storage.ReadOCGTLogs(logDir())
		if e != nil { writeJSON(w, 200, map[string]any{"success": false, "error": e.Error()}); return }
		daily := storage.CalculateDailyStats(logs, 7)
		type DayStat struct { Date string `json:"date"`; InputTokens int `json:"input_tokens"`; OutputTokens int `json:"output_tokens"`; TotalTokens int `json:"total_tokens"`; RequestCount int `json:"request_count"` }
		var list []DayStat
		for _, s := range daily { list = append(list, DayStat{s.Date, s.InputTokens, s.OutputTokens, s.TotalTokens, s.RequestCount}) }
		sort.Slice(list, func(i, j int) bool { return list[i].Date < list[j].Date })
		writeJSON(w, 200, map[string]any{"success": true, "data": list})
	})
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"status": "ok", "time": time.Now()}) })
	sub, _ := fs.Sub(webAssets, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return http.ListenAndServe(s.addr, mux)
}

func writeJSON(w http.ResponseWriter, s int, d any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s)
	json.NewEncoder(w).Encode(d)
}
