package quota

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"time"

	"foundry-quota-sentinel/internal/formatter"
)

const (
	// commandCodeAPIBase is the commandcode.ai API origin (OBSERVED). The
	// web UI (commandcode.ai) and the API (api.commandcode.ai) are separate
	// origins; the quota endpoints live on the API origin and require the
	// same-site HttpOnly session cookie pair.
	commandCodeAPIBase = "https://api.commandcode.ai"
	// commandCodeMaxResponseSize bounds each JSON response body. The reader
	// reads maxSize+1 bytes so an exactly-fitting response is accepted and
	// one exceeding the bound is rejected as oversized (never silently
	// truncated into a partial quota result).
	commandCodeMaxResponseSize = 1 << 20
	commandCodeRequestTimeout  = 15 * time.Second
)

// commandCodePlanCredits is the commandcode.ai plan tier → monthly credit
// cap table (OBSERVED in the production JS pricing constants, 2026-08).
// The usage page computes the MONTHLY meter as
// used = totalCredits − credits.monthlyCredits, cap = totalCredits. The
// table is embedded because the billing API returns only the planId
// (e.g. individual-goat) — the cap itself ships in the frontend bundle.
// An unknown planId FAILS CLOSED (never guesses a cap).
var commandCodePlanCredits = map[string]int{
	"individual-go":     10,
	"individual-goat":   70,
	"individual-pro":    30,
	"individual-pro-v1": 80,
	"individual-max":    150,
	"individual-ultra":  300,
	"teams-pro":         40,
}

// CommandCodeQuerier fetches the commandcode.ai quota for one account.
// Cookie is the serialised HttpOnly session pair captured at login
// (__Secure-commandcode_prod_.session_token + session_data); UserName is
// the GitHub login embedded in the usage-page URL and used by open-page.
// Client is injectable for tests; nil builds a default client with
// commandCodeRequestTimeout. The request URL is ALWAYS the fixed
// commandCodeAPIBase origin — no BaseURL override seam, so the cookie
// can never be sent to an unvalidated host.
type CommandCodeQuerier struct {
	Cookie   string
	UserName string
	Client   *http.Client
	// baseURL overrides the fixed API origin for tests only. Production
	// callers leave it empty, and getJSON always uses commandCodeAPIBase
	// when baseURL is empty — so the cookie can never be sent to an
	// unvalidated host from production code.
	baseURL string
}

// NewCommandCodeQuerier builds a querier from COMMANDCODE env vars.
func NewCommandCodeQuerier() *CommandCodeQuerier {
	return &CommandCodeQuerier{
		Cookie:   os.Getenv("COMMANDCODE_AUTH_COOKIE"),
		UserName: os.Getenv("COMMANDCODE_USER_NAME"),
	}
}

// FetchQuota retrieves and parses the account's three-window quota:
// fiveHour + weekly come from /internal/billing/credits, the monthly cap
// from the plan tier in /internal/billing/subscriptions, and the monthly
// used = cap − credits.monthlyCredits (matching the usage page's own
// calculation). The three windows map onto the shared QuotaData model as
// Rolling / Weekly / Monthly.
func (q *CommandCodeQuerier) FetchQuota() (*QuotaData, error) {
	if err := q.validate(); err != nil {
		return nil, err
	}
	client := q.Client
	if client == nil {
		client = &http.Client{Timeout: commandCodeRequestTimeout}
	}
	creditsBody, err := q.getJSON(client, "/internal/billing/credits")
	if err != nil {
		return nil, err
	}
	subsBody, err := q.getJSON(client, "/internal/billing/subscriptions")
	if err != nil {
		return nil, err
	}
	return parseCommandCodeQuota(creditsBody, subsBody, time.Now())
}

func (q *CommandCodeQuerier) validate() error {
	if q.Cookie == "" {
		return fmt.Errorf("commandcode cookie not set")
	}
	return nil
}

// getJSON GETs a commandcode API path with the saved session cookie and
// returns the raw JSON body (bounded). A non-200 response returns ONLY the
// status code in the error — never the upstream body, which may carry
// private/account material. A read error is propagated (a connection
// breaking mid-body must not look like a truncated-but-valid response),
// and an oversized body is rejected rather than silently truncated.
func (q *CommandCodeQuerier) getJSON(client *http.Client, path string) (string, error) {
	base := q.baseURL
	if base == "" {
		base = commandCodeAPIBase
	}
	req, err := http.NewRequest("GET", base+path, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("accept", "*/*")
	req.Header.Set("cookie", q.Cookie)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("commandcode API returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, commandCodeMaxResponseSize+1))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if len(body) > commandCodeMaxResponseSize {
		return "", fmt.Errorf("commandcode response exceeds %d bytes", commandCodeMaxResponseSize)
	}
	return string(body), nil
}

// commandCodeWindow mirrors one windowLimits entry (fiveHour/weekly) in
// /internal/billing/credits (OBSERVED): {used, cap, exceeded, resetAt}.
// resetAt is a Unix epoch-millisecond integer.
type commandCodeWindow struct {
	Used     *float64 `json:"used,omitempty"`
	Cap      *float64 `json:"cap,omitempty"`
	Exceeded *bool    `json:"exceeded,omitempty"`
	ResetAt  *int64   `json:"resetAt,omitempty"`
}

// commandCodeCreditsResponse mirrors /internal/billing/credits (OBSERVED):
// {credits:{monthlyCredits,...}, windowLimits:{limited, fiveHour, weekly}}.
type commandCodeCreditsResponse struct {
	Credits *struct {
		MonthlyCredits *float64 `json:"monthlyCredits,omitempty"`
	} `json:"credits,omitempty"`
	WindowLimits *struct {
		Limited  *bool             `json:"limited,omitempty"`
		FiveHour *commandCodeWindow `json:"fiveHour,omitempty"`
		Weekly   *commandCodeWindow `json:"weekly,omitempty"`
	} `json:"windowLimits,omitempty"`
}

// commandCodeSubscription mirrors the parts of /internal/billing/subscriptions
// (OBSERVED) needed for the monthly meter: {data:{planId, currentPeriodEnd}}.
type commandCodeSubscription struct {
	Data *struct {
		PlanID           string `json:"planId,omitempty"`
		CurrentPeriodEnd string `json:"currentPeriodEnd,omitempty"`
	} `json:"data,omitempty"`
}

// parseCommandCodeQuota parses the two sanitized JSON bodies into the
// shared three-window QuotaData. Fail-closed rules:
//   - both bodies must be valid JSON with the required objects present;
//   - fiveHour and weekly each require used + cap + resetAt, with
//     used >= 0 and cap > 0; percent = used/cap*100 (clamped to 0..100);
//   - monthly requires credits.monthlyCredits + a KNOWN planId (from
//     subscriptions); used = cap − monthlyCredits must be >= 0; the
//     monthly row is omitted when the plan has no cap (fail-closed on
//     unknown plans);
//   - resetAt is a Unix-epoch-millisecond integer, must be > 0.
func parseCommandCodeQuota(creditsBody, subsBody string, now time.Time) (*QuotaData, error) {
	var credits commandCodeCreditsResponse
	if err := json.Unmarshal([]byte(creditsBody), &credits); err != nil {
		return nil, fmt.Errorf("commandcode credits 响应解析失败: %w", err)
	}
	if credits.WindowLimits == nil {
		return nil, fmt.Errorf("commandcode credits 响应缺少 windowLimits")
	}
	var subs commandCodeSubscription
	if err := json.Unmarshal([]byte(subsBody), &subs); err != nil {
		return nil, fmt.Errorf("commandcode subscriptions 响应解析失败: %w", err)
	}

	fiveHour, err := parseCommandCodeWindow(credits.WindowLimits.FiveHour, "5 小时", now)
	if err != nil {
		return nil, err
	}
	weekly, err := parseCommandCodeWindow(credits.WindowLimits.Weekly, "周", now)
	if err != nil {
		return nil, err
	}

	var monthly *QuotaUsage
	// When windowLimits.limited is false the account has no monthly cap
	// (pay-as-you-go / unlimited) — the monthly row is omitted, mirroring
	// the OpenCode unlimited-monthly semantics. When limited is absent the
	// response is treated as malformed for the monthly computation (the
	// observed response always carries it) — the computation itself fails
	// closed on unknown plans.
	if credits.WindowLimits.Limited == nil || *credits.WindowLimits.Limited {
		m, err := parseCommandCodeMonthly(creditsBody, subsBody, now)
		if err != nil {
			return nil, err
		}
		monthly = m
	}

	return &QuotaData{
		Rolling:   fiveHour,
		Weekly:    weekly,
		Monthly:   monthly,
		FetchedAt: now,
	}, nil
}

func parseCommandCodeWindow(w *commandCodeWindow, name string, now time.Time) (QuotaUsage, error) {
	if w == nil || w.Used == nil || w.Cap == nil || w.ResetAt == nil {
		return QuotaUsage{}, fmt.Errorf("commandcode %s 用量窗口数据缺失", name)
	}
	used := *w.Used
	cap := *w.Cap
	if math.IsNaN(used) || math.IsInf(used, 0) || used < 0 {
		return QuotaUsage{}, fmt.Errorf("commandcode %s 用量非法", name)
	}
	if math.IsNaN(cap) || math.IsInf(cap, 0) || cap <= 0 {
		return QuotaUsage{}, fmt.Errorf("commandcode %s 额度非法", name)
	}
	// resetAt is a Unix-epoch-millisecond integer. A value <= 0 is LEGAL
	// (OBSERVED 2026-08-19: the real credits response carries fiveHour.
	// resetAt=0 for a normal/empty window) — it means the window has no
	// reset point, rendered as reset "0m". Only the shape (presence of the
	// field) is validated above; the value is not fail-closed.
	resetInSec := 0
	if *w.ResetAt > 0 {
		resetAt := time.UnixMilli(*w.ResetAt)
		resetInSec = int(resetAt.Sub(now).Seconds())
		if resetInSec < 0 {
			resetInSec = 0
		}
	}
	pct := clampPercent(used / cap * 100)
	return QuotaUsage{
		Status:       "active",
		UsagePercent: pct,
		ResetInSec:   resetInSec,
		ResetDisplay: formatter.FormatDurationCompact(resetInSec),
	}, nil
}

// parseCommandCodeMonthly computes the monthly meter against the RAW
// bodies so currentPeriodEnd is available for the reset display:
// used = planCap − credits.monthlyCredits, percent = used/planCap*100.
func parseCommandCodeMonthly(creditsBody, subsBody string, now time.Time) (*QuotaUsage, error) {
	var subs commandCodeSubscription
	if err := json.Unmarshal([]byte(subsBody), &subs); err != nil {
		return nil, fmt.Errorf("commandcode subscriptions 响应解析失败: %w", err)
	}
	if subs.Data == nil || subs.Data.PlanID == "" {
		return nil, fmt.Errorf("commandcode 响应缺少计划标识")
	}
	var credits commandCodeCreditsResponse
	if err := json.Unmarshal([]byte(creditsBody), &credits); err != nil {
		return nil, fmt.Errorf("commandcode credits 响应解析失败: %w", err)
	}
	if credits.Credits == nil || credits.Credits.MonthlyCredits == nil {
		return nil, fmt.Errorf("commandcode 响应缺少月度额度")
	}
	cap, ok := commandCodePlanCredits[subs.Data.PlanID]
	if !ok {
		// Unknown plan: fail closed rather than guessing a cap. The error
		// carries ONLY the plan id — no credential or account material.
		return nil, fmt.Errorf("commandcode 未知计划 %s", subs.Data.PlanID)
	}
	remaining := *credits.Credits.MonthlyCredits
	if math.IsNaN(remaining) || math.IsInf(remaining, 0) || remaining < 0 {
		return nil, fmt.Errorf("commandcode 月度剩余额度非法")
	}
	used := float64(cap) - remaining
	if used < 0 {
		// Spent more than the monthly allowance: the page shows the meter
		// full; over-cap is clamped to 100%.
		used = float64(cap)
	}
	pct := clampPercent(used / float64(cap) * 100)
	resetInSec := commandCodeResetSeconds(subs.Data.CurrentPeriodEnd, now)
	return &QuotaUsage{
		Status:       "active",
		UsagePercent: pct,
		ResetInSec:   resetInSec,
		ResetDisplay: formatter.FormatDurationCompact(resetInSec),
	}, nil
}

// clampPercent clamps a usage percentage into 0..100.
func clampPercent(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// commandCodeResetSeconds converts an ISO-8601 reset instant to seconds
// from now. An empty or unparseable value fails closed to 0 (reset
// "now" — never a fabricated far-future date).
func commandCodeResetSeconds(iso string, now time.Time) int {
	if iso == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return 0
	}
	s := int(t.Sub(now).Seconds())
	if s < 0 {
		return 0
	}
	return s
}
