package quota

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"time"
)

const (
	openCodeGoBaseURL   = "https://opencode.ai"
	openCodeGoServiceID = "c7389bd0e731f80f49593e5ee53835475f4e28594dd6bd83eb229bab753498cd"
)

type OpenCodeGoQuerier struct {
	Cookie      string
	WorkspaceID string
}

func NewOpenCodeGoQuerier() *OpenCodeGoQuerier {
	return &OpenCodeGoQuerier{Cookie: os.Getenv("OPENCODE_GO_AUTH_COOKIE"), WorkspaceID: os.Getenv("OPENCODE_GO_WORKSPACE_ID")}
}

func (q *OpenCodeGoQuerier) FetchQuota() (*QuotaData, error) {
	if err := q.validate(); err != nil { return nil, err }
	args := buildRPCArgs(q.WorkspaceID)
	reqURL := fmt.Sprintf("%s/_server?id=%s&args=%s", openCodeGoBaseURL, openCodeGoServiceID, url.QueryEscape(args))
	req, _ := http.NewRequest("GET", reqURL, nil)
	req.Header.Set("accept", "*/*")
	req.Header.Set("cookie", q.Cookie)
	req.Header.Set("x-server-id", openCodeGoServiceID)
	req.Header.Set("x-server-instance", "server-fn:3")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil { return nil, fmt.Errorf("http request: %w", err) }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return parseQuotaResponse(string(body))
}

func (q *OpenCodeGoQuerier) validate() error {
	if q.Cookie == "" { return fmt.Errorf("OPENCODE_GO_AUTH_COOKIE not set") }
	if q.WorkspaceID == "" { return fmt.Errorf("OPENCODE_GO_WORKSPACE_ID not set") }
	return nil
}

func buildRPCArgs(workspaceID string) string {
	data, _ := json.Marshal(map[string]any{
		"t": map[string]any{"t": 9, "i": 0, "l": 1, "a": []any{map[string]any{"t": 1, "s": workspaceID}}, "o": 0},
		"f": 31, "m": []any{},
	})
	return string(data)
}

func parseQuotaResponse(text string) (*QuotaData, error) {
	rollingRe := regexp.MustCompile(`rollingUsage:\$R\[1\]=\{status:"([^"]+)",resetInSec:(\d+),usagePercent:(\d+)\}`)
	weeklyRe := regexp.MustCompile(`weeklyUsage:\$R\[2\]=\{status:"([^"]+)",resetInSec:(\d+),usagePercent:(\d+)\}`)
	monthlyRe := regexp.MustCompile(`monthlyUsage:\$R\[3\]=\{status:"([^"]+)",resetInSec:(\d+),usagePercent:(\d+)\}`)
	rollingMatch := rollingRe.FindStringSubmatch(text)
	weeklyMatch := weeklyRe.FindStringSubmatch(text)
	monthlyMatch := monthlyRe.FindStringSubmatch(text)
	if rollingMatch == nil { return nil, fmt.Errorf("failed to parse rollingUsage") }
	if weeklyMatch == nil { return nil, fmt.Errorf("failed to parse weeklyUsage") }
	rolling := parseUsage(rollingMatch)
	weekly := parseUsage(weeklyMatch)
	var monthly *QuotaUsage
	if monthlyMatch != nil {
		m := parseUsage(monthlyMatch)
		if m.Status != "unlimited" { monthly = &m }
	}
	return &QuotaData{Rolling: rolling, Weekly: weekly, Monthly: monthly, FetchedAt: time.Now()}, nil
}

func parseUsage(m []string) QuotaUsage {
	reset, _ := strconv.Atoi(m[2])
	percent, _ := strconv.Atoi(m[3])
	return QuotaUsage{Status: m[1], UsagePercent: percent, ResetInSec: reset, ResetDisplay: fmtDurationCompact(reset)}
}

func fmtDurationCompact(s int) string {
	if s < 60 { return fmt.Sprintf("%ds", s) }
	if s < 3600 { return fmt.Sprintf("%dm", s/60) }
	if s < 86400 { return fmt.Sprintf("%dh", s/3600) }
	return fmt.Sprintf("%dd", s/86400)
}
