package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"sort"
	"time"

	"ocgt-monitor/internal/quota"
	"ocgt-monitor/internal/state"
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
		logs, e := storage.ReadOCGTLogs(storage.OCGTLogDir())
		if e != nil { writeJSON(w, 200, map[string]any{"success": false, "error": e.Error()}); return }
		daily := storage.CalculateDailyStats(logs, 7)
		type DayStat struct { Date string `json:"date"`; InputTokens int `json:"input_tokens"`; OutputTokens int `json:"output_tokens"`; TotalTokens int `json:"total_tokens"`; RequestCount int `json:"request_count"` }
		var list []DayStat
		for _, s := range daily { list = append(list, DayStat{s.Date, s.InputTokens, s.OutputTokens, s.TotalTokens, s.RequestCount}) }
		sort.Slice(list, func(i, j int) bool { return list[i].Date < list[j].Date })
		writeJSON(w, 200, map[string]any{"success": true, "data": list})
	})

	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		logs, e := storage.ReadOCGTLogs(storage.OCGTLogDir())
		if e != nil { writeJSON(w, 200, map[string]any{"success": false, "error": e.Error()}); return }
		r.ParseForm()
		var models map[string]storage.TokenStatsByModel
		if from := r.Form.Get("from"); from != "" {
			fromT, err1 := time.Parse("2006-01-02", from)
			toT, err2 := time.Parse("2006-01-02", r.Form.Get("to"))
			if err1 == nil && err2 == nil {
				toT = toT.Add(24*time.Hour - time.Second)
				models = storage.CalculateModelStatsByRange(logs, fromT, toT)
			} else {
				days := 7
				if d := r.Form.Get("days"); d != "" {
					if n, err := fmt.Sscanf(d, "%d", &days); err != nil || n != 1 || days < 1 { days = 7 }
				}
				models = storage.CalculateModelStats(logs, days)
			}
		} else {
			days := 7
			if d := r.Form.Get("days"); d != "" {
				if n, err := fmt.Sscanf(d, "%d", &days); err != nil || n != 1 || days < 1 { days = 7 }
			}
			models = storage.CalculateModelStats(logs, days)
		}
		type MStat struct { Model string `json:"model"`; InputTokens int `json:"input_tokens"`; OutputTokens int `json:"output_tokens"`; TotalTokens int `json:"total_tokens"`; RequestCount int `json:"request_count"` }
		var list []MStat
		for _, s := range models { list = append(list, MStat{s.Model, s.InputTokens, s.OutputTokens, s.TotalTokens, s.RequestCount}) }
		sort.Slice(list, func(i, j int) bool { return list[i].TotalTokens > list[j].TotalTokens })
		writeJSON(w, 200, map[string]any{"success": true, "data": list})
	})

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"status": "ok", "time": time.Now()}) })
		mux.HandleFunc("/api/quit", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"status": "bye"}); go func() { time.Sleep(100 * time.Millisecond); os.Exit(0) }() })
		mux.HandleFunc("/api/pin", func(w http.ResponseWriter, r *http.Request) { state.Pinned = !state.Pinned; writeJSON(w, 200, map[string]any{"pinned": state.Pinned}) })
		mux.HandleFunc("/api/pin-state", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"pinned": state.Pinned}) })

	sub, _ := fs.Sub(webAssets, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return http.ListenAndServe(s.addr, mux)
}

func writeJSON(w http.ResponseWriter, s int, d any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s)
	json.NewEncoder(w).Encode(d)
}
