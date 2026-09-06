package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"sync"

	"foundry-quota-sentinel/pkg/sdk/providers/kimi"
)

func (s *Server) registerKimiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/kimi/accounts", func(w http.ResponseWriter, r *http.Request) {
		type shell struct {
			Name       string `json:"name"`
			Pending    bool   `json:"pending"`
			Generation int    `json:"generation"`
		}
		accs := s.curKimi()
		shells := make([]shell, 0, len(accs))
		for _, a := range accs {
			shells = append(shells, shell{Name: a.Name, Pending: true, Generation: a.Generation})
		}
		sort.Slice(shells, func(i, j int) bool { return shells[i].Name < shells[j].Name })
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": shells})
	})

	mux.HandleFunc("/api/kimi", func(w http.ResponseWriter, r *http.Request) {
		type card struct {
			Name    string               `json:"name"`
			Success bool                 `json:"success"`
			Quota   *kimi.KimiQuotaData `json:"quota,omitempty"`
			Error   string               `json:"error,omitempty"`
		}
		accs := s.curKimi()
		cards := make([]card, len(accs))
		fetch := s.kimiFetch
		if fetch == nil {
			fetch = func(a KimiAccount) (*kimi.KimiQuotaData, error) {
				if s.kimiAccountLock != nil {
					release, lerr := s.kimiAccountLock(a.Name)
					if lerr != nil {
						return nil, fmt.Errorf("Kimi 账户 %q 刷新锁失败: %v", a.Name, lerr)
					}
					defer release()
				} else {
					mu := s.kimiRefreshLock(a.Name)
					mu.Lock()
					defer mu.Unlock()
				}

				acc := a
				if s.kimiReloadAccount != nil {
					latest, ok := s.kimiReloadAccount(a.Name)
					if !ok {
						return nil, fmt.Errorf("Kimi 账户 %q 已不存在", a.Name)
					}
					acc = latest
				}
				fetchRefresh := s.kimiFetchWithRefresh
				if fetchRefresh == nil {
					fetchRefresh = func(ctx context.Context, acc KimiAccount) (*kimi.KimiQuotaData, *kimi.RefreshResult, error) {
						q := &kimi.KimiQuerier{AccessToken: acc.AccessToken, RefreshToken: acc.RefreshToken, Headers: acc.Headers}
						return q.FetchQuotaWithRefresh(ctx)
					}
				}
				data, rotated, err := fetchRefresh(r.Context(), acc)
				if err != nil {
					return nil, err
				}
				if rotated != nil && s.kimiRefreshSave != nil {
					if saveErr := s.kimiRefreshSave(a.Name, rotated.AccessToken, rotated.RefreshToken); saveErr != nil {
						return nil, fmt.Errorf("Kimi 账户 %q token 轮换保存失败，请重新登录", a.Name)
					}
				}
				return data, nil
			}
		}
		var wg sync.WaitGroup
		for i, a := range accs {
			wg.Add(1)
			go func(i int, a KimiAccount) {
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

	mux.HandleFunc("/api/kimi/login", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		spawn := s.spawnKimiLogin
		if spawn == nil {
			spawn = func(n string) error {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				args := []string{"login-kimi"}
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
