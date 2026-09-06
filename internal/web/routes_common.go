package web

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"sort"
	"time"

	"foundry-quota-sentinel/internal/state"
	"foundry-quota-sentinel/internal/storage"
)

func writeJSON(w http.ResponseWriter, s int, d any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s)
	json.NewEncoder(w).Encode(d)
}

func (s *Server) registerCommonRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/delete", func(w http.ResponseWriter, r *http.Request) {
		provider := r.URL.Query().Get("provider")
		name := r.URL.Query().Get("name")
		if provider != "opencode" && provider != "deepseek" && provider != "ollama" && provider != "kimi" && provider != "commandcode" {
			writeJSON(w, 200, map[string]any{"success": false, "error": "bad provider"})
			return
		}
		if s.onDelete == nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": "delete not supported"})
			return
		}
		if err := s.onDelete(provider, name); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})

	mux.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		logs, e := storage.ReadOCGTLogs(storage.OCGTLogDir())
		if e != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": e.Error()})
			return
		}
		daily := storage.CalculateDailyStats(logs, 7)
		type DayStat struct {
			Date         string `json:"date"`
			InputTokens  int    `json:"input_tokens"`
			OutputTokens int    `json:"output_tokens"`
			TotalTokens  int    `json:"total_tokens"`
			RequestCount int    `json:"request_count"`
		}
		var list []DayStat
		for _, s := range daily {
			list = append(list, DayStat{s.Date, s.InputTokens, s.OutputTokens, s.TotalTokens, s.RequestCount})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Date < list[j].Date })
		writeJSON(w, 200, map[string]any{"success": true, "data": list})
	})

	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		logs, e := storage.ReadOCGTLogs(storage.OCGTLogDir())
		if e != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": e.Error()})
			return
		}
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
					if n, err := fmt.Sscanf(d, "%d", &days); err != nil || n != 1 || days < 1 {
						days = 7
					}
				}
				if days == 1 {
					now := time.Now()
					start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
					models = storage.CalculateModelStatsByRange(logs, start, start.Add(24*time.Hour-time.Second))
				} else {
					models = storage.CalculateModelStats(logs, days)
				}
			}
		} else {
			days := 7
			if d := r.Form.Get("days"); d != "" {
				if n, err := fmt.Sscanf(d, "%d", &days); err != nil || n != 1 || days < 1 {
					days = 7
				}
			}
			if days == 1 {
				now := time.Now()
				start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
				models = storage.CalculateModelStatsByRange(logs, start, start.Add(24*time.Hour-time.Second))
			} else {
				models = storage.CalculateModelStats(logs, days)
			}
		}
		type MStat struct {
			Model        string `json:"model"`
			InputTokens  int    `json:"input_tokens"`
			OutputTokens int    `json:"output_tokens"`
			TotalTokens  int    `json:"total_tokens"`
			RequestCount int    `json:"request_count"`
		}
		var list []MStat
		for _, s := range models {
			list = append(list, MStat{s.Model, s.InputTokens, s.OutputTokens, s.TotalTokens, s.RequestCount})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].TotalTokens > list[j].TotalTokens })
		writeJSON(w, 200, map[string]any{"success": true, "data": list})
	})

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "time": time.Now()})
	})
	mux.HandleFunc("/api/quit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "bye"})
		go func() { time.Sleep(100 * time.Millisecond); os.Exit(0) }()
	})
	mux.HandleFunc("/api/pin", func(w http.ResponseWriter, r *http.Request) {
		state.Pinned = !state.Pinned
		writeJSON(w, 200, map[string]any{"pinned": state.Pinned})
	})
	mux.HandleFunc("/api/pin-state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"pinned": state.Pinned})
	})
	mux.HandleFunc("/api/position", func(w http.ResponseWriter, r *http.Request) {
		if yStr := r.URL.Query().Get("y"); yStr != "" {
			var y int
			if _, err := fmt.Sscanf(yStr, "%d", &y); err == nil && y >= 0 && y < 5000 {
				state.PanelY = y
			}
		}
		writeJSON(w, 200, map[string]any{"y": state.PanelY})
	})

	mux.HandleFunc("/api/winsize", func(w http.ResponseWriter, r *http.Request) {
		var ww, hh int
		fmt.Sscanf(r.URL.Query().Get("w"), "%d", &ww)
		fmt.Sscanf(r.URL.Query().Get("h"), "%d", &hh)
		if ww > 0 && hh > 0 && s.onWinSize != nil {
			s.onWinSize(ww, hh)
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	sub, _ := fs.Sub(webAssets, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))
}
