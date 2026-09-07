package web

import (
	"net/http"
	"os"
	"os/exec"
	"sort"
	"sync"

	"foundry-quota-sentinel/pkg/sdk/providers/opencode"
)

func (s *Server) registerOpenCodeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/quota", func(w http.ResponseWriter, r *http.Request) {
		accs := s.curAccounts()
		if len(accs) == 0 {
			writeJSON(w, 200, map[string]any{"success": false, "error": "no account configured"})
			return
		}
		a := accs[0]
		q := &opencode.OpenCodeQuerier{Cookie: a.Cookie, WorkspaceID: a.WorkspaceID}
		d, e := q.FetchQuota()
		if e != nil {
			writeJSON(w, 200, map[string]any{"success": false, "error": e.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"success": true, "data": d})
	})

	mux.HandleFunc("/api/accounts", func(w http.ResponseWriter, r *http.Request) {
		type result struct {
			Name    string             `json:"name"`
			Success bool               `json:"success"`
			Quota   *opencode.QuotaData `json:"quota,omitempty"`
			Error   string             `json:"error,omitempty"`
		}
		accs := s.curAccounts()
		results := make([]result, len(accs))
		fetch := s.openCodeFetch
		if fetch == nil {
			fetch = func(a Account) (*opencode.QuotaData, error) {
				q := &opencode.OpenCodeQuerier{Cookie: a.Cookie, WorkspaceID: a.WorkspaceID}
				return q.FetchQuota()
			}
		}
		var wg sync.WaitGroup
		for i, a := range accs {
			wg.Add(1)
			go func(i int, a Account) {
				defer wg.Done()
				d, e := fetch(a)
				if e != nil {
					results[i] = result{Name: a.Name, Success: false, Error: e.Error()}
				} else {
					results[i] = result{Name: a.Name, Success: true, Quota: d}
				}
			}(i, a)
		}
		wg.Wait()
		sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
		writeJSON(w, 200, map[string]any{"success": true, "data": results})
	})

	mux.HandleFunc("/api/opencode/accounts", func(w http.ResponseWriter, r *http.Request) {
		type shell struct {
			Name    string `json:"name"`
			Pending bool   `json:"pending"`
		}
		accs := s.curAccounts()
		shells := make([]shell, 0, len(accs))
		for _, a := range accs {
			shells = append(shells, shell{Name: a.Name, Pending: true})
		}
		sort.Slice(shells, func(i, j int) bool { return shells[i].Name < shells[j].Name })
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": shells})
	})

	mux.HandleFunc("/api/opencode/login", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		spawn := s.spawnOpenCodeLogin
		if spawn == nil {
			spawn = func(n string) error {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				args := []string{"login-opencode"}
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
