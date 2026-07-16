package quota

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func TestOllamaQuerierSendsCookie(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/settings" {
			t.Fatalf("path = %q, want /settings", r.URL.Path)
		}
		if got := r.Header.Get("Cookie"); got != "__Secure-session=valid" {
			t.Fatalf("Cookie = %q", got)
		}
		if r.Header.Get("User-Agent") == "" {
			t.Fatal("User-Agent header is empty")
		}
		_, _ = io.WriteString(w, `<i aria-label="Session usage 1% used"></i><i data-time="2099-01-01T00:00:00Z"></i><i aria-label="Weekly usage 2% used"></i><i data-time="2099-01-02T00:00:00Z"></i>`)
	}))
	defer s.Close()

	if _, err := (&OllamaQuerier{Cookie: "__Secure-session=valid", BaseURL: s.URL}).FetchQuota(); err != nil {
		t.Fatal(err)
	}
}
