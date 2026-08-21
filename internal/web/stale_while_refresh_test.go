package web

import (
	"strings"
	"testing"
)

// 3.1 周期刷新已有数据 → 容器保留旧卡，无"加载中"闪烁。
// 3.2 手动刷新已有数据 → 旧数据保留到 fill 替换。
// 3.3 首载无数据 → 壳渲染 → fill 替换并置 hasData。
// 3.4 fill 失败 + 已有数据 → 旧数据保留；fill 失败 + 无数据 → 错误态。

// TestSidebarPeriodicRefreshKeepsOldData proves a 30s periodic refresh
// over real data renders NO loading shell and keeps the old card until the
// fill settles (stale-while-refresh).
func TestSidebarPeriodicRefreshKeepsOldData(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		// First fill settles with real data for A.
		{Op: "resolve", URL: "api/accounts", Data: map[string]any{
			"success": true,
			"data": []map[string]any{
				{"name": "A", "success": true, "quota": map[string]any{
					"rolling": map[string]any{"status": "ok", "usage_percent": 42, "reset_in_sec": 300},
				}},
			},
		}},
		{Op: "advance", Ms: 0},
		// 30s later the periodic refresh starts: shell fetch (auto) then
		// fill stays pending. During the pending fill the OLD card must
		// remain — no 加载中 placeholder anywhere.
		{Op: "advance", Ms: 30000},
		{Op: "checkContainer", ID: "accountCards", WantName: "A", NoLoading: true},
		// Fill settles with UPDATED data; card replaced in place.
		{Op: "resolve", URL: "api/accounts", Data: map[string]any{
			"success": true,
			"data": []map[string]any{
				{"name": "A", "success": true, "quota": map[string]any{
					"rolling": map[string]any{"status": "ok", "usage_percent": 99, "reset_in_sec": 100},
				}},
			},
		}},
		{Op: "advance", Ms: 0},
	})
	// No container write between the first fill and the second must carry
	// a 加载中 placeholder (the old card stays through the refresh).
	for _, w := range obs.ContainerWrites {
		if w.ID != "accountCards" {
			continue
		}
		if strings.Contains(w.HTML, "加载中") {
			t.Fatalf("refresh must never render 加载中 over existing data (t=%d): %q", w.T, w.HTML)
		}
	}
}

// TestSidebarManualRefreshKeepsOldDataUntilFill proves an explicit refresh
// (fq call) keeps the old card until the new fill settles.
func TestSidebarManualRefreshKeepsOldDataUntilFill(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		{Op: "resolve", URL: "api/accounts", Data: map[string]any{
			"success": true,
			"data": []map[string]any{
				{"name": "A", "success": true, "quota": map[string]any{
					"rolling": map[string]any{"status": "ok", "usage_percent": 42, "reset_in_sec": 300},
				}},
			},
		}},
		{Op: "advance", Ms: 0},
		// Manual refresh: fill pending, old card must stay.
		{Op: "call", Fn: "fq"},
		{Op: "advance", Ms: 0},
		{Op: "checkContainer", ID: "accountCards", WantName: "A", NoLoading: true},
	})
	for _, w := range obs.ContainerWrites {
		if w.ID == "accountCards" && strings.Contains(w.HTML, "加载中") {
			t.Fatalf("manual refresh must not show 加载中: %q", w.HTML)
		}
	}
}

// TestSidebarFirstLoadShellThenFill proves the shell-first promise still
// holds for a container with NO data yet: shells render, then the fill
// replaces them and the data-state flag turns true (no shells thereafter).
func TestSidebarFirstLoadShellThenFill(t *testing.T) {
	obs := runSchedulerScenario(t, []schedulerStep{
		// The script's initial auto fq at load time leaves /api/accounts
		// pending; settle it with empty data (no accounts configured yet)
		// so the single-flight state clears and a manual refresh can run
		// its own shell-first chain.
		{Op: "resolve", URL: "api/accounts", Data: map[string]any{"success": true, "data": []map[string]any{}}},
		{Op: "advance", Ms: 0},
		// Configure the shell endpoint to report account A (config has it),
		// then trigger an explicit refresh: shell-first path renders A's
		// shell (containerHasData is still false), fill stays pending.
		{Op: "setShellData", URL: "/api/opencode/accounts", Data: []map[string]any{
			{"name": "A", "pending": true},
		}},
		{Op: "call", Fn: "fq"},
		{Op: "advance", Ms: 0},
		{Op: "advance", Ms: 0}, // extra tick: shell render settles before assert
		{Op: "checkContainer", ID: "accountCards", WantName: "A", NoLoading: false},
		// Fill settles with real data.
		{Op: "resolve", URL: "api/accounts", Data: map[string]any{
			"success": true,
			"data": []map[string]any{
				{"name": "A", "success": true, "quota": map[string]any{
					"rolling": map[string]any{"status": "ok", "usage_percent": 42, "reset_in_sec": 300},
				}},
			},
		}},
		{Op: "advance", Ms: 0},
		{Op: "checkContainer", ID: "accountCards", WantName: "A", NoLoading: true},
	})
	// The first non-empty write must be the shell (加载中 present), and a
	// later write must be the real card (no 加载中).
	var sawShell, sawReal bool
	for _, w := range obs.ContainerWrites {
		if w.ID != "accountCards" {
			continue
		}
		if strings.Contains(w.HTML, "加载中") {
			sawShell = true
		} else if w.HTML != "" && strings.Contains(w.HTML, "data-name=\"A\"") {
			sawReal = true
		}
	}
	if !sawShell {
		t.Fatal("first load must render a loading shell before the fill")
	}
	if !sawReal {
		t.Fatal("fill must replace the shell with real data")
	}
}

// TestSidebarFillFailureKeepsOldData proves a failed refresh keeps the old
// data (no error substitution), while a failed FIRST fill renders the error
// state.
func TestSidebarFillFailureKeepsOldData(t *testing.T) {
	// Phase 1: first fill fails -> error state (no data existed).
	obs1 := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		{Op: "reject", URL: "api/accounts"},
		{Op: "advance", Ms: 0},
	})
	var sawErr bool
	for _, w := range obs1.ContainerWrites {
		if w.ID == "accountCards" && strings.Contains(w.HTML, "qerr") {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatal("first-load fill failure must render the error state")
	}

	// Phase 2: data present, then a refresh fails -> old data stays.
	obs2 := runSchedulerScenario(t, []schedulerStep{
		{Op: "advance", Ms: 0},
		{Op: "resolve", URL: "api/accounts", Data: map[string]any{
			"success": true,
			"data": []map[string]any{
				{"name": "A", "success": true, "quota": map[string]any{
					"rolling": map[string]any{"status": "ok", "usage_percent": 42, "reset_in_sec": 300},
				}},
			},
		}},
		{Op: "advance", Ms: 0},
		// 30s later refresh fails.
		{Op: "advance", Ms: 30000},
		{Op: "reject", URL: "api/accounts"},
		{Op: "advance", Ms: 0},
		// Old data must still be there, no error, no loading.
		{Op: "checkContainer", ID: "accountCards", WantName: "A", NoLoading: true},
	})
	for _, w := range obs2.ContainerWrites {
		if w.ID == "accountCards" && strings.Contains(w.HTML, "qerr") {
			t.Fatalf("failed refresh must NOT substitute error over existing data: %q", w.HTML)
		}
	}
}
