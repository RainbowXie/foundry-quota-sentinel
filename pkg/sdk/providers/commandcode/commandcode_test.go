package commandcode

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var testCommandCodeNow = time.Date(2026, 8, 20, 4, 0, 0, 0, time.UTC)

func TestParseCommandCodeQuotaHappyPath(t *testing.T) {
	got, err := ParseCommandCodeQuota(commandCodeCreditsFixture, commandCodeSubscriptionsFixture, testCommandCodeNow)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if math.Abs(got.Rolling.UsagePercent-13.144901) > 0.0005 {
		t.Errorf("fiveHour percent = %v, want ~13.144901", got.Rolling.UsagePercent)
	}
	if got.Rolling.Status != "active" {
		t.Errorf("fiveHour status = %q, want active", got.Rolling.Status)
	}
	if got.Rolling.ResetInSec != 10120 {
		t.Errorf("fiveHour resetInSec = %d, want 10120", got.Rolling.ResetInSec)
	}

	if math.Abs(got.Weekly.UsagePercent-29.830003) > 0.0005 {
		t.Errorf("weekly percent = %v, want ~29.830003", got.Weekly.UsagePercent)
	}
	if got.Weekly.ResetInSec != 477314 {
		t.Errorf("weekly resetInSec = %d, want 477314", got.Weekly.ResetInSec)
	}

	if got.Monthly == nil {
		t.Fatal("monthly is nil, want a row")
	}
	if math.Abs(got.Monthly.UsagePercent-14.915) > 0.0005 {
		t.Errorf("monthly percent = %v, want ~14.915", got.Monthly.UsagePercent)
	}
	if got.Monthly.ResetInSec != 2550616 {
		t.Errorf("monthly resetInSec = %d, want 2550616", got.Monthly.ResetInSec)
	}
	if !got.FetchedAt.Equal(testCommandCodeNow) {
		t.Errorf("fetchedAt = %v, want %v", got.FetchedAt, testCommandCodeNow)
	}
}

func TestParseCommandCodeQuotaUnlimitedNoMonthly(t *testing.T) {
	body := `{"credits":{"monthlyCredits":3.5},"windowLimits":{"limited":false,
		"fiveHour":{"used":0.5,"cap":14,"exceeded":false,"resetAt":1787208520419},
		"weekly":{"used":1.0,"cap":35,"exceeded":false,"resetAt":1787675714733}}}`
	got, err := ParseCommandCodeQuota(body, commandCodeSubscriptionsFixture, testCommandCodeNow)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if got.Monthly != nil {
		t.Errorf("monthly = %+v, want nil (unlimited plan)", got.Monthly)
	}
	if math.Abs(got.Rolling.UsagePercent-3.571429) > 0.0005 {
		t.Errorf("fiveHour percent = %v, want ~3.571429", got.Rolling.UsagePercent)
	}
}

func TestParseCommandCodeQuotaUnknownPlanFailsClosed(t *testing.T) {
	subs := `{"data":{"planId":"individual-mega-ultra","currentPeriodEnd":"2026-09-18T16:30:16.000Z"}}`
	_, err := ParseCommandCodeQuota(commandCodeCreditsFixture, subs, testCommandCodeNow)
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
	_, err := ParseCommandCodeQuota(body, commandCodeSubscriptionsFixture, testCommandCodeNow)
	if err == nil || !strings.Contains(err.Error(), "周") {
		t.Fatalf("expected weekly-missing error, got %v", err)
	}
}

func TestParseCommandCodeQuotaNegativeUsedFailsClosed(t *testing.T) {
	body := `{"credits":{"monthlyCredits":5},"windowLimits":{"limited":true,
		"fiveHour":{"used":-1,"cap":14,"exceeded":false,"resetAt":1787208520419},
		"weekly":{"used":1.0,"cap":35,"exceeded":false,"resetAt":1787675714733}}}`
	_, err := ParseCommandCodeQuota(body, commandCodeSubscriptionsFixture, testCommandCodeNow)
	if err == nil || !strings.Contains(err.Error(), "非法") {
		t.Fatalf("expected negative-used error, got %v", err)
	}
}

func TestParseCommandCodeQuotaBadResetParsesAsNow(t *testing.T) {
	body := `{"credits":{"monthlyCredits":5},"windowLimits":{"limited":true,
		"fiveHour":{"used":1.0,"cap":14,"exceeded":false,"resetAt":0},
		"weekly":{"used":1.0,"cap":35,"exceeded":false,"resetAt":1787675714733}}}`
	got, err := ParseCommandCodeQuota(body, commandCodeSubscriptionsFixture, testCommandCodeNow)
	if err != nil {
		t.Fatalf("resetAt=0 must parse, got error: %v", err)
	}
	if got.Rolling.ResetInSec != 0 {
		t.Fatalf("resetAt=0 must render reset 0m, got resetInSec=%d", got.Rolling.ResetInSec)
	}
	if math.Abs(got.Rolling.UsagePercent-7.142857) > 0.0005 {
		t.Fatalf("fiveHour percent = %v, want ~7.142857", got.Rolling.UsagePercent)
	}
	if got.Weekly.ResetInSec <= 0 {
		t.Fatalf("weekly resetInSec = %d, want > 0", got.Weekly.ResetInSec)
	}
}

func TestParseCommandCodeQuotaMalformedJSON(t *testing.T) {
	_, err := ParseCommandCodeQuota("{not json", commandCodeSubscriptionsFixture, testCommandCodeNow)
	if err == nil || !strings.Contains(err.Error(), "解析失败") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

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

	q := &CommandCodeQuerier{Cookie: "session_token=x; session_data=y", Client: srv.Client(), BaseURL: srv.URL}
	got, err := q.FetchQuota()
	if err != nil {
		t.Fatalf("FetchQuota: %v", err)
	}
	if !strings.Contains(gotCookie, "session_token=x") {
		t.Errorf("cookie not sent: %q", gotCookie)
	}
	if math.Abs(got.Rolling.UsagePercent-13.144901) > 0.0005 {
		t.Errorf("rolling percent = %v, want ~13.144901", got.Rolling.UsagePercent)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"secret":"do-not-leak"}`)
	}))
	defer srv2.Close()
	q2 := &CommandCodeQuerier{Cookie: "session_token=x", Client: srv2.Client(), BaseURL: srv2.URL}
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

	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", commandCodeMaxResponseSize+10))
	}))
	defer srv3.Close()
	q3 := &CommandCodeQuerier{Cookie: "session_token=x", Client: srv3.Client(), BaseURL: srv3.URL}
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

func TestParseCommandCodeQuotaExhaustedResetAtIsLegal(t *testing.T) {
	credits := `{"credits":{"belowThreshold":false,"creditThreshold":0,"monthlyCredits":34.996812375,"purchasedCredits":0,"premiumMonthlyCredits":0,"opensourceMonthlyCredits":34.996812375},"windowLimits":{"limited":true,"exceeded":"weekly","fiveHour":{"used":0,"cap":14,"exceeded":false,"resetAt":0},"weekly":{"used":35.003187625,"cap":35,"exceeded":true,"resetAt":1787675714733}}}`
	subs := `{"success":true,"data":{"planId":"individual-goat","currentPeriodEnd":"2026-09-18T16:30:16.000Z"}}`
	got, err := ParseCommandCodeQuota(credits, subs, time.UnixMilli(1787600000000))
	if err != nil {
		t.Fatalf("real exhausted response must parse, got error: %v", err)
	}
	if got.Rolling.UsagePercent != 0 {
		t.Fatalf("fiveHour percent = %v, want 0", got.Rolling.UsagePercent)
	}
	if got.Rolling.ResetInSec != 0 {
		t.Fatalf("fiveHour resetInSec = %d, want 0 (resetAt=0)", got.Rolling.ResetInSec)
	}
	if got.Weekly.UsagePercent != 100 {
		t.Fatalf("weekly percent = %v, want 100", got.Weekly.UsagePercent)
	}
	if got.Weekly.ResetInSec <= 0 {
		t.Fatalf("weekly resetInSec = %d, want > 0", got.Weekly.ResetInSec)
	}
}
