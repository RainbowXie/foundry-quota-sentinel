package quota

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseOllamaQuota(t *testing.T) {
	q, err := parseOllamaQuota(`<i aria-label="Session usage 42.5% used"></i><i data-time="2099-01-01T00:00:00Z"></i><i aria-label="Weekly usage 17.7% used"></i><i data-time="2099-01-02T00:00:00Z"></i>`)
	if err != nil {
		t.Fatal(err)
	}
	if q.Rolling.UsagePercent != 43 || q.Weekly.UsagePercent != 18 {
		t.Fatalf("quota = %#v", q)
	}
}

func TestParseOllamaQuotaMissingWeekly(t *testing.T) {
	_, err := parseOllamaQuota(`<i aria-label="Session usage 42.5% used"></i><i data-time="2099-01-01T00:00:00Z"></i>`)
	if err == nil || !strings.Contains(err.Error(), "Weekly") {
		t.Fatalf("error = %v, want missing Weekly error", err)
	}
}

func TestParseOllamaQuotaSkipsUnrelatedTimestamp(t *testing.T) {
	genuineReset := time.Date(2099, time.February, 1, 0, 0, 0, 0, time.UTC)
	q, err := parseOllamaQuota(`<div><i aria-label="Session usage 42.5% used"></i><aside><i data-time="2099-01-01T00:00:00Z"></i></aside><i data-time="2099-02-01T00:00:00Z"></i><i aria-label="Weekly usage 17.7% used"></i><i data-time="2099-03-01T00:00:00Z"></i></div>`)
	if err != nil {
		t.Fatal(err)
	}
	want := int(time.Until(genuineReset).Seconds())
	if got := q.Rolling.ResetInSec; got < want-1 || got > want+1 {
		t.Fatalf("Session ResetInSec = %d, want approximately %d", got, want)
	}
}

func TestParseOllamaQuotaFindsResetBesideNestedUsageMeter(t *testing.T) {
	q, err := parseOllamaQuota(`<section><div><div data-usage-meter><div aria-label="Session usage 42.5% used"></div></div><div data-time="2099-02-01T00:00:00Z"></div></div><div><div data-usage-meter><div aria-label="Weekly usage 17.7% used"></div></div><div data-time="2099-03-01T00:00:00Z"></div></div></section>`)
	if err != nil {
		t.Fatal(err)
	}
	if q.Rolling.UsagePercent != 43 || q.Weekly.UsagePercent != 18 {
		t.Fatalf("quota = %#v", q)
	}
}

func TestParseOllamaQuotaRejectsOutOfRangePercent(t *testing.T) {
	_, err := parseOllamaQuota(`<i aria-label="Session usage 100.1% used"></i><i data-time="2099-01-01T00:00:00Z"></i><i aria-label="Weekly usage 2% used"></i><i data-time="2099-01-02T00:00:00Z"></i>`)
	if err == nil || !strings.Contains(err.Error(), "invalid usage percent") {
		t.Fatalf("error = %v, want invalid percent error", err)
	}
}

func TestParseOllamaQuotaRejectsInvalidOrPastReset(t *testing.T) {
	for _, reset := range []string{"not-a-timestamp", "2000-01-01T00:00:00Z"} {
		t.Run(reset, func(t *testing.T) {
			_, err := parseOllamaQuota(`<i aria-label="Session usage 1% used"></i><i data-time="` + reset + `"></i><i aria-label="Weekly usage 2% used"></i><i data-time="2099-01-02T00:00:00Z"></i>`)
			if err == nil || !strings.Contains(err.Error(), "invalid or past reset timestamp") {
				t.Fatalf("error = %v, want invalid or past reset timestamp error", err)
			}
		})
	}
}

func TestOllamaQuerierSendsCookie(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/settings" {
			t.Fatalf("path = %q, want /settings", r.URL.Path)
		}
		if got := r.Header.Get("Cookie"); got != "__Secure-session=valid" {
			t.Fatalf("Cookie = %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "Ollama login browser" {
			t.Fatalf("User-Agent = %q, want login browser user agent", got)
		}
		_, _ = io.WriteString(w, `<i aria-label="Session usage 1% used"></i><i data-time="2099-01-01T00:00:00Z"></i><i aria-label="Weekly usage 2% used"></i><i data-time="2099-01-02T00:00:00Z"></i>`)
	}))
	defer s.Close()

	if _, err := (&OllamaQuerier{Cookie: "__Secure-session=valid", UserAgent: "Ollama login browser", BaseURL: s.URL}).FetchQuota(); err != nil {
		t.Fatal(err)
	}
}

func TestOllamaQuerierUsesRequestDeadlineWithCustomClient(t *testing.T) {
	previousTimeout := ollamaRequestTimeout
	ollamaRequestTimeout = 50 * time.Millisecond
	t.Cleanup(func() { ollamaRequestTimeout = previousTimeout })

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer s.Close()

	started := time.Now()
	_, err := (&OllamaQuerier{Cookie: "__Secure-session=valid", BaseURL: s.URL, Client: &http.Client{}}).FetchQuota()
	if err == nil {
		t.Fatal("FetchQuota succeeded, want request deadline error")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("FetchQuota took %s, want less than 1s", elapsed)
	}
}
