package quota

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testCommandCodeNow is a fixed instant used by the parser tests. It sits
// 2026-08-20T04:00:00Z, after the fixture's fiveHour resetAt
// (1787208520419 = 2026-08-20T03:28:40Z) and weekly resetAt
// (1787675714733 = 2026-08-24T03:15:14Z), and before the subscription
// period end (2026-09-18T16:30:16Z) — so every reset shows a positive
// remaining duration.
var testCommandCodeNow = time.Date(2026, 8, 20, 4, 0, 0, 0, time.UTC)

func TestParseCommandCodeQuotaHappyPath(t *testing.T) {
	got, err := parseCommandCodeQuota(commandCodeCreditsFixture, commandCodeSubscriptionsFixture, testCommandCodeNow)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	// fiveHour: 1.8402861307/14 = 13.1% -> 13
	if got.Rolling.UsagePercent != 13 {
		t.Errorf("fiveHour percent = %d, want 13", got.Rolling.UsagePercent)
	}
	if got.Rolling.Status != "active" {
		t.Errorf("fiveHour status = %q, want active", got.Rolling.Status)
	}
	// fiveHour reset: 1787208520419 = 2026-08-20T06:48:40Z, 10120s before now
	if got.Rolling.ResetInSec != 10120 {
		t.Errorf("fiveHour resetInSec = %d, want 10120", got.Rolling.ResetInSec)
	}

	// weekly: 10.4405010462/35 = 29.8% -> 30
	if got.Weekly.UsagePercent != 30 {
		t.Errorf("weekly percent = %d, want 30", got.Weekly.UsagePercent)
	}
	// weekly reset: 1787675714733 = 2026-08-25T16:35:14Z = 477314s
	if got.Weekly.ResetInSec != 477314 {
		t.Errorf("weekly resetInSec = %d, want 477314", got.Weekly.ResetInSec)
	}

	// monthly: GOAT cap 70 − monthlyCredits 59.5595 = 10.4405 used -> 14.9% -> 15
	if got.Monthly == nil {
		t.Fatal("monthly is nil, want a row")
	}
	if got.Monthly.UsagePercent != 15 {
		t.Errorf("monthly percent = %d, want 15", got.Monthly.UsagePercent)
	}
	// period end 2026-09-18T16:30:16Z − now = 2550616s
	if got.Monthly.ResetInSec != 2550616 {
		t.Errorf("monthly resetInSec = %d, want 2550616", got.Monthly.ResetInSec)
	}
	if !got.FetchedAt.Equal(testCommandCodeNow) {
		t.Errorf("fetchedAt = %v, want %v", got.FetchedAt, testCommandCodeNow)
	}
}

func TestParseCommandCodeQuotaUnlimitedNoMonthly(t *testing.T) {
	// windowLimits.limited=false (pay-as-you-go): monthly row is omitted.
	body := `{"credits":{"monthlyCredits":3.5},"windowLimits":{"limited":false,
		"fiveHour":{"used":0.5,"cap":14,"exceeded":false,"resetAt":1787208520419},
		"weekly":{"used":1.0,"cap":35,"exceeded":false,"resetAt":1787675714733}}}`
	got, err := parseCommandCodeQuota(body, commandCodeSubscriptionsFixture, testCommandCodeNow)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if got.Monthly != nil {
		t.Errorf("monthly = %+v, want nil (unlimited plan)", got.Monthly)
	}
	if got.Rolling.UsagePercent != 4 {
		t.Errorf("fiveHour percent = %d, want 4 (0.5/14=3.6%%->4)", got.Rolling.UsagePercent)
	}
}

func TestParseCommandCodeQuotaUnknownPlanFailsClosed(t *testing.T) {
	// An unknown planId must fail, never guess a cap.
	subs := `{"data":{"planId":"individual-mega-ultra","currentPeriodEnd":"2026-09-18T16:30:16.000Z"}}`
	_, err := parseCommandCodeQuota(commandCodeCreditsFixture, subs, testCommandCodeNow)
	if err == nil {
		t.Fatal("expected error for unknown plan, got nil")
	}
	if !strings.Contains(err.Error(), "未知计划") {
		t.Errorf("error = %q, want it to mention unknown plan", err.Error())
	}
}

func TestParseCommandCodeQuotaMissingWindow(t *testing.T) {
	body := `{"credits":{"monthlyCredits":5},"windowLimits":{"limited":true,
		"fiveHour":{"used":0.5,"cap":14,"exceeded":false,"resetAt":1787208520419}}}`
	_, err := parseCommandCodeQuota(body, commandCodeSubscriptionsFixture, testCommandCodeNow)
	if err == nil || !strings.Contains(err.Error(), "周") {
		t.Fatalf("expected weekly-missing error, got %v", err)
	}
}

func TestParseCommandCodeQuotaNegativeUsedFailsClosed(t *testing.T) {
	body := `{"credits":{"monthlyCredits":5},"windowLimits":{"limited":true,
		"fiveHour":{"used":-1,"cap":14,"exceeded":false,"resetAt":1787208520419},
		"weekly":{"used":1.0,"cap":35,"exceeded":false,"resetAt":1787675714733}}}`
	_, err := parseCommandCodeQuota(body, commandCodeSubscriptionsFixture, testCommandCodeNow)
	if err == nil || !strings.Contains(err.Error(), "非法") {
		t.Fatalf("expected negative-used error, got %v", err)
	}
}

func TestParseCommandCodeQuotaBadResetFailsClosed(t *testing.T) {
	body := `{"credits":{"monthlyCredits":5},"windowLimits":{"limited":true,
		"fiveHour":{"used":1.0,"cap":14,"exceeded":false,"resetAt":0},
		"weekly":{"used":1.0,"cap":35,"exceeded":false,"resetAt":1787675714733}}}`
	_, err := parseCommandCodeQuota(body, commandCodeSubscriptionsFixture, testCommandCodeNow)
	if err == nil || !strings.Contains(err.Error(), "重置") {
		t.Fatalf("expected reset error, got %v", err)
	}
}

func TestParseCommandCodeQuotaMalformedJSON(t *testing.T) {
	_, err := parseCommandCodeQuota("{not json", commandCodeSubscriptionsFixture, testCommandCodeNow)
	if err == nil || !strings.Contains(err.Error(), "解析失败") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

// TestCommandCodeQuerierHTTPContract verifies the transport behavior:
// session cookie sent, bounded read, status-only errors on non-200, and
// oversized-body rejection.
func TestCommandCodeQuerierHTTPContract(t *testing.T) {
	var gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("cookie")
		switch r.URL.Path {
		case "/internal/billing/credits":
			fmt.Fprint(w, commandCodeCreditsFixture)
		case "/internal/billing/subscriptions":
			fmt.Fprint(w, commandCodeSubscriptionsFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// The querier's production base is the fixed commandCodeAPIBase; the
	// test-only baseURL seam lets this test drive the transport against a
	// local server without any production override path.
	q := &CommandCodeQuerier{Cookie: "session_token=x; session_data=y", Client: srv.Client(), baseURL: srv.URL}
	got, err := q.FetchQuota()
	if err != nil {
		t.Fatalf("FetchQuota: %v", err)
	}
	if !strings.Contains(gotCookie, "session_token=x") {
		t.Errorf("cookie not sent: %q", gotCookie)
	}
	if got.Rolling.UsagePercent != 13 {
		t.Errorf("rolling percent = %d, want 13", got.Rolling.UsagePercent)
	}

	// Non-200: error carries only the status code.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"secret":"do-not-leak"}`)
	}))
	defer srv2.Close()
	q2 := &CommandCodeQuerier{Cookie: "session_token=x", Client: srv2.Client(), baseURL: srv2.URL}
	_, err = q2.FetchQuota()
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if strings.Contains(err.Error(), "do-not-leak") {
		t.Errorf("error leaked response body: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should carry status code: %q", err.Error())
	}

	// Oversized body: rejected, not truncated.
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", commandCodeMaxResponseSize+10))
	}))
	defer srv3.Close()
	q3 := &CommandCodeQuerier{Cookie: "session_token=x", Client: srv3.Client(), baseURL: srv3.URL}
	_, err = q3.FetchQuota()
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected oversized error, got %v", err)
	}
}

func TestCommandCodeQuerierMissingCookie(t *testing.T) {
	q := &CommandCodeQuerier{}
	_, err := q.FetchQuota()
	if err == nil || !strings.Contains(err.Error(), "cookie not set") {
		t.Fatalf("expected cookie error, got %v", err)
	}
}
