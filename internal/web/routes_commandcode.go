package web

import (
	"net/http"
	"os"
	"os/exec"
	"sort"
	"sync"

	"foundry-quota-sentinel/pkg/sdk/providers/commandcode"
)

func (s *Server) registerCommandCodeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/commandcode/accounts", func(w http.ResponseWriter, r *http.Request) {
		type shell struct {
			Name    string `json:"name"`
			Pending bool   `json:"pending"`
		}
		accs := s.curCommandCode()
		shells := make([]shell, 0, len(accs))
		for _, a := range accs {
			shells = append(shells, shell{Name: a.Name, Pending: true})
		}
		sort.Slice(shells, func(i, j int) bool { return shells[i].Name < shells[j].Name })
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": shells})
	})

	mux.HandleFunc("/api/commandcode", func(w http.ResponseWriter, r *http.Request) {
		type card struct {
			Name    string                      `json:"name"`
			Success bool                        `json:"success"`
			Quota   *commandcode.QuotaData      `json:"quota,omitempty"`
			Error   string                      `json:"error,omitempty"`
		}
		accs := s.curCommandCode()
		cards := make([]card, len(accs))
		fetch := s.commandCodeFetch
		if fetch == nil {
			fetch = func(a CommandCodeAccount) (*commandcode.QuotaData, error) {
				q := &commandcode.CommandCodeQuerier{Cookie: a.Cookie, UserName: a.UserName}
				return q.FetchQuota()
			}
		}
		var wg sync.WaitGroup
		for i, a := range accs {
			wg.Add(1)
			go func(i int, a CommandCodeAccount) {
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

	mux.HandleFunc("/api/commandcode/login", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		spawn := s.spawnCommandCodeLogin
		if spawn == nil {
			spawn = func(n string) error {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				args := []string{"login-commandcode"}
				if n != "" {
					args = append(args, n)
				}
				return exec.Command(exe, args...).Start()
			}
		}
		if err := spawn(name); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	})
}
