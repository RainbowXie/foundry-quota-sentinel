package web

import (
	"net/http"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"

	"foundry-quota-sentinel/pkg/sdk/providers/deepseek"
)

func (s *Server) registerDeepSeekRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/balance", func(w http.ResponseWriter, r *http.Request) {
		b, e := s.deepseek.FetchBalance()
		if e != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": e.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "data": b})
	})

	mux.HandleFunc("/api/deepseek/accounts", func(w http.ResponseWriter, r *http.Request) {
		type shell struct {
			Name       string `json:"name"`
			Pending    bool   `json:"pending"`
			Generation int    `json:"generation"`
		}
		accs := s.curDeepSeek()
		shells := make([]shell, 0, len(accs))
		for _, a := range accs {
			shells = append(shells, shell{Name: a.Name, Pending: true, Generation: a.Generation})
		}
		sort.Slice(shells, func(i, j int) bool { return shells[i].Name < shells[j].Name })
		writeJSON(w, 200, map[string]any{"success": true, "data": shells})
	})

	mux.HandleFunc("/api/deepseek", func(w http.ResponseWriter, r *http.Request) {
		type card struct {
			Name    string                     `json:"name"`
			Success bool                       `json:"success"`
			Summary *deepseek.DeepSeekSummary  `json:"summary,omitempty"`
			Models  []deepseek.DeepSeekModelUsage `json:"models,omitempty"`
			Error   string                     `json:"error,omitempty"`
		}
		accs := s.curDeepSeek()
		cards := make([]card, len(accs))
		var wg sync.WaitGroup
		now := time.Now()
		for i, a := range accs {
			wg.Add(1)
			go func(i int, a DeepSeekAccount) {
				defer wg.Done()
				c := card{Name: a.Name}
				q := &deepseek.DeepSeekWebQuerier{Token: a.Token}
				sum, err := q.FetchSummary()
				if err != nil {
					c.Error = err.Error()
					cards[i] = c
					return
				}
				models, err := q.FetchUsage(now.Year(), int(now.Month()))
				if err != nil {
					c.Error = err.Error()
					cards[i] = c
					return
				}
				c.Success = true
				c.Summary = sum
				c.Models = models
				cards[i] = c
			}(i, a)
		}
		wg.Wait()
		sort.Slice(cards, func(i, j int) bool { return cards[i].Name < cards[j].Name })
		writeJSON(w, 200, map[string]any{"success": true, "data": cards})
	})

	mux.HandleFunc("/api/deepseek/login", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		spawn := s.spawnDeepSeekLogin
		if spawn == nil {
			spawn = func(n string) error {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				args := []string{"login-deepseek"}
				if n != "" {
					args = append(args, n)
				}
				return exec.Command(exe, args...).Start()
			}
		}
		if err := spawn(name); err != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true})
	})
}
