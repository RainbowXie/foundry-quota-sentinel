package web

import (
	"net/http"
	"os"
	"os/exec"
	"sort"
	"sync"

	"foundry-quota-sentinel/pkg/sdk/providers/ollama"
)

func (s *Server) registerOllamaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/ollama", func(w http.ResponseWriter, r *http.Request) {
		type card struct {
			Name    string             `json:"name"`
			Success bool               `json:"success"`
			Quota   *ollama.QuotaData  `json:"quota,omitempty"`
			Error   string             `json:"error,omitempty"`
		}
		accs := s.curOllama()
		cards := make([]card, len(accs))
		fetch := s.ollamaFetch
		if fetch == nil {
			fetch = func(a OllamaAccount) (*ollama.QuotaData, error) {
				return (&ollama.OllamaQuerier{Cookie: a.Cookie, UserAgent: a.UserAgent}).FetchQuota()
			}
		}
		var wg sync.WaitGroup
		for i, a := range accs {
			wg.Add(1)
			go func(i int, a OllamaAccount) {
				defer wg.Done()
				d, err := fetch(a)
				if err != nil {
					cards[i] = card{Name: a.Name, Error: err.Error()}
					return
				}
				cards[i] = card{Name: a.Name, Success: true, Quota: d}
			}(i, a)
		}
		wg.Wait()
		sort.Slice(cards, func(i, j int) bool { return cards[i].Name < cards[j].Name })
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": cards})
	})

	mux.HandleFunc("/api/ollama/accounts", func(w http.ResponseWriter, r *http.Request) {
		type shell struct {
			Name    string `json:"name"`
			Pending bool   `json:"pending"`
		}
		accs := s.curOllama()
		shells := make([]shell, 0, len(accs))
		for _, a := range accs {
			shells = append(shells, shell{Name: a.Name, Pending: true})
		}
		sort.Slice(shells, func(i, j int) bool { return shells[i].Name < shells[j].Name })
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": shells})
	})

	mux.HandleFunc("/api/ollama/login", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		exe, err := os.Executable()
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
			return
		}
		args := []string{"login-ollama"}
		if name != "" {
			args = append(args, name)
		}
		if err := exec.Command(exe, args...).Start(); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	})
}
