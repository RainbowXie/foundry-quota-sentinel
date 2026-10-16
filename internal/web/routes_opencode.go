package web

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"sync"
	"time"

	"foundry-quota-sentinel/pkg/sdk/providers/opencode"
)

const openCodeOrgCacheTTL = 2 * time.Minute

func (s *Server) getOpenCodeOrganizations(cookie string, defaultWS string) []opencode.Organization {
	s.openCodeOrgCacheMu.RLock()
	entry, ok := s.openCodeOrgCache[cookie]
	s.openCodeOrgCacheMu.RUnlock()
	if ok && time.Since(entry.fetchedAt) < openCodeOrgCacheTTL && len(entry.orgs) > 0 {
		return entry.orgs
	}

	orgs, err := opencode.FetchOrganizations(cookie, nil)
	if err != nil || len(orgs) == 0 {
		if defaultWS != "" {
			return []opencode.Organization{{ID: defaultWS, Name: ""}}
		}
		if ok && len(entry.orgs) > 0 {
			return entry.orgs
		}
		return nil
	}

	s.openCodeOrgCacheMu.Lock()
	s.openCodeOrgCache[cookie] = openCodeOrgEntry{orgs: orgs, fetchedAt: time.Now()}
	s.openCodeOrgCacheMu.Unlock()
	return orgs
}

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
			Name        string              `json:"name"`
			Success     bool                `json:"success"`
			Quota       *opencode.QuotaData `json:"quota,omitempty"`
			Error       string              `json:"error,omitempty"`
			WorkspaceID string              `json:"workspace_id,omitempty"`
		}
		accs := s.curAccounts()
		fetch := s.openCodeFetch
		if fetch != nil {
			// 单测 Mock 路径：保持与既有单账户 Mock 行为一致
			results := make([]result, len(accs))
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
			return
		}

		// 生产环境：动态工作空间发现并过滤有效 Go 订阅
		var results []result
		var mu sync.Mutex
		var wg sync.WaitGroup

		for _, a := range accs {
			orgs := s.getOpenCodeOrganizations(a.Cookie, a.WorkspaceID)
			if len(orgs) == 0 {
				results = append(results, result{Name: a.Name, Success: false, Error: "未发现有效工作空间"})
				continue
			}

			for _, org := range orgs {
				wg.Add(1)
				go func(acc Account, o opencode.Organization) {
					defer wg.Done()
					displayName := acc.Name
					if o.Name != "" {
						displayName = acc.Name + "/" + o.Name
					} else if len(orgs) > 1 {
						displayName = acc.Name + "/" + o.ID
					}

					q := &opencode.OpenCodeQuerier{Cookie: acc.Cookie, WorkspaceID: o.ID}
					d, e := q.FetchQuota()
					if e != nil {
						if errors.Is(e, opencode.ErrNoGoSubscription) {
							return
						}
						mu.Lock()
						results = append(results, result{
							Name:        displayName,
							Success:     false,
							Error:       e.Error(),
							WorkspaceID: o.ID,
						})
						mu.Unlock()
						return
					}
					mu.Lock()
					results = append(results, result{
						Name:        displayName,
						Success:     true,
						Quota:       d,
						WorkspaceID: o.ID,
					})
					mu.Unlock()
				}(a, org)
			}
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
			s.openCodeOrgCacheMu.RLock()
			entry, ok := s.openCodeOrgCache[a.Cookie]
			s.openCodeOrgCacheMu.RUnlock()
			if ok && len(entry.orgs) > 0 {
				for _, o := range entry.orgs {
					displayName := a.Name
					if o.Name != "" {
						displayName = a.Name + "/" + o.Name
					} else if len(entry.orgs) > 1 {
						displayName = a.Name + "/" + o.ID
					}
					shells = append(shells, shell{Name: displayName, Pending: true})
				}
			} else {
				shells = append(shells, shell{Name: a.Name, Pending: true})
			}
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
