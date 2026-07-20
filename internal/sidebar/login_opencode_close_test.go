package sidebar

import (
	"context"
	"reflect"
	"testing"

	"foundry-quota-sentinel/internal/browserauth"
)

// TestRunOpenCodeLoginClosesBrowserBeforeValidate proves the browser is
// reaped before the Go quota validator is called. Validation may issue
// a network request of its own, and the shared browser must not still
// be alive at that point — otherwise the user's machine would be
// hosting an application-owned Chrome process for the duration of the
// Go validation round trip.
func TestRunOpenCodeLoginClosesBrowserBeforeValidate(t *testing.T) {
	var closedAtValidate bool
	browser := newFakeOpenCodeBrowser("session=good", "wrk_abc123", func() {
		// No-op; the close hook runs at browser.Close(), which fires
		// before validate() because the coordinator must reap first.
	})
	_, _, err := runOpenCodeLogin(context.Background(), browser, func(string, string) bool {
		closedAtValidate = browser.closed
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if !closedAtValidate {
		t.Fatal("browser was not closed before the Go quota validator ran")
	}
}

// TestRunOpenCodeLoginRejectsValidateFailureWithoutEmptyResult proves a
// validator returning false causes the coordinator to return an error
// rather than a credential pair, and that the browser is closed.
func TestRunOpenCodeLoginRejectsValidateFailure(t *testing.T) {
	browser := newFakeOpenCodeBrowser("session=good", "wrk_abc123", nil)
	cookie, wsid, err := runOpenCodeLogin(context.Background(), browser, func(string, string) bool {
		return false
	})
	if err == nil {
		t.Fatal("expected error when validator rejects credentials")
	}
	if cookie != "" || wsid != "" {
		t.Fatalf("expected empty credentials on validation failure, got (%q, %q)", cookie, wsid)
	}
	if !browser.closed {
		t.Fatal("browser must close even when validator rejects")
	}
}

// TestOpenCodeSavedCookiesRejectsCRLF proves the saved-cookie parser
// refuses values containing carriage return or newline. A CRLF in a
// cookie value would let an attacker inject a Set-Cookie header into
// the response and pivot through the next request.
func TestOpenCodeSavedCookiesRejectsCRLF(t *testing.T) {
	headers := []string{
		"session=ok\r\nX-Injected: 1",
		"session=ok\nX-Injected: 1",
		"session=ok\rSet-Cookie: x",
	}
	for _, h := range headers {
		if _, err := openCodeSavedCookies(h); err == nil {
			t.Fatalf("openCodeSavedCookies(%q) accepted CRLF in value", h)
		}
	}
}

// TestOpenCodeSavedCookiesRejectsControlAndQuoteChars proves the
// saved-cookie parser refuses control characters, whitespace, quotes,
// backslashes, and embedded `=` in either name or value. Each of
// these would either forge a request header (CRLF), smuggle a
// second cookie into the slot (`=`), or break the request parser
// downstream.
func TestOpenCodeSavedCookiesRejectsControlAndQuoteChars(t *testing.T) {
	cases := []string{
		"session=ok\r\nX-Injected: 1",
		"session=ok\nX-Injected: 1",
		"session=ok\rSet-Cookie: x",
		`session=ok"`,
		`session=ok\`,
		"session=ok second=1", // whitespace in value
		"se ssion=ok",         // whitespace in name
		"a=b;c",               // empty value
		"=v",                  // empty name
		"a;session=ok",        // duplicate
		"a;session=ok;session=x",
	}
	for _, c := range cases {
		if _, err := openCodeSavedCookies(c); err == nil {
			t.Fatalf("openCodeSavedCookies(%q) accepted unsafe header", c)
		}
	}
}

// TestOpenCodeSavedCookiesAcceptsWellFormed proves a normal header
// still parses into the expected number of secure cookies.
func TestOpenCodeSavedCookiesAcceptsWellFormed(t *testing.T) {
	cookies, err := openCodeSavedCookies("session=good; aid=track")
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 2 {
		t.Fatalf("got %d cookies, want 2", len(cookies))
	}
	for _, c := range cookies {
		if !c.Secure || !c.HTTPOnly {
			t.Fatalf("cookie %q missing secure/httpOnly: %#v", c.Name, c)
		}
		if c.Domain != "opencode.ai" {
			t.Fatalf("cookie %q wrong domain %q", c.Name, c.Domain)
		}
	}
}

// TestOpenCodeSavedCookiesAcceptsBase64Padding makes sure a real
// session cookie value (base64 with `=` padding) round-trips through
// the parser. Tightening the value charset to exclude `=` would
// reject genuine session cookies.
func TestOpenCodeSavedCookiesAcceptsBase64Padding(t *testing.T) {
	cookies, err := openCodeSavedCookies("session=YWJj==")
	if err != nil {
		t.Fatalf("openCodeSavedCookies accepted a base64-padded value as malformed: %v", err)
	}
	if len(cookies) != 1 || cookies[0].Value != "YWJj==" {
		t.Fatalf("got %#v", cookies)
	}
}

// TestOpenCodeSavedCookiesAcceptsFullCookieOctetCharset proves the
// saved-cookie parser accepts the full RFC 6265 cookie-octet set
// (excluding CRLF, ';', '"', '\\', and whitespace, which stay
// rejected). Real opencode.ai session cookies carry characters the old
// narrow regex rejected (e.g. /, ~, *, !, (, ), #, $, &, <, >, ?, [, ]),
// which made RunOpenCodePage fail BEFORE the browser launched; /api/open
// swallowed the subprocess error, so the user saw "no reaction".
func TestOpenCodeSavedCookiesAcceptsFullCookieOctetCharset(t *testing.T) {
	cases := []string{
		"session=abc/def", // '/' (base64 standard, JWT separators)
		"session=a~b",     // '~'
		"session=a*b",     // '*'
		"session=a!b",     // '!'
		"session=a(b)c",   // '(' ')' (URL-safe tokens)
		"session=a#b$c&d", // '#', '$', '&'
		"session=a?b<c>d", // '?', '<', '>'
		"session=a[b]c",   // '[', ']'
		"session=a^b|c",   // '^', '|'
		"session=a`b",     // '`'
		"session=a{b}c",   // '{', '}'
		"session=a'b",     // '\''
	}
	for _, c := range cases {
		cookies, err := openCodeSavedCookies(c)
		if err != nil {
			t.Fatalf("openCodeSavedCookies(%q) rejected a valid cookie-octet: %v", c, err)
		}
		if len(cookies) != 1 {
			t.Fatalf("openCodeSavedCookies(%q) = %d cookies, want 1", c, len(cookies))
		}
	}
}

// TestOpenCodeSavedCookiesStillRejectsUnsafeSep proves the widened
// charset does NOT relax the safety boundary: CRLF, ';', '"', '\\',
// and whitespace in a value are still rejected.
func TestOpenCodeSavedCookiesStillRejectsUnsafeSep(t *testing.T) {
	for _, c := range []string{
		"session=a;b",
		"session=a\r\nb",
		"session=a\nb",
		`session=a"b`,
		`session=a\ b`,
		"session=a b",
	} {
		if _, err := openCodeSavedCookies(c); err == nil {
			t.Fatalf("openCodeSavedCookies(%q) accepted an unsafe value", c)
		}
	}
}

// TestCookieDomainMatchesAcceptsSubdomain proves the host filter used
// by both capture and injection accepts a direct subdomain of the
// policy host.
func TestCookieDomainMatchesAcceptsSubdomain(t *testing.T) {
	if !cookieDomainMatches(".opencode.ai", "opencode.ai") {
		t.Fatal("expected subdomain match for \".opencode.ai\"")
	}
}

// TestCookieDomainMatchesRejectsEmpty proves the host filter refuses
// either empty input rather than returning a permissive true.
func TestCookieDomainMatchesRejectsEmpty(t *testing.T) {
	if cookieDomainMatches("", "opencode.ai") {
		t.Fatal("expected empty host to be rejected")
	}
	if cookieDomainMatches("opencode.ai", "") {
		t.Fatal("expected empty policy to be rejected")
	}
}

// TestRunOpenCodePageInjectsCookiesBeforeNavigate makes sure the page
// flow sets cookies through the browser-level CDP before the page
// navigates. Saving cookies AFTER navigation would be useless because
// the page would already have loaded the unauthenticated origin.
func TestRunOpenCodePageInjectsCookiesBeforeNavigate(t *testing.T) {
	browser := newFakeOpenCodeBrowser("", "", nil)
	cookies := []browserauth.Cookie{{
		Name: "session", Value: "good", Domain: "opencode.ai", Path: "/",
	}}
	if err := runOpenCodePage(context.Background(), browser,
		"https://opencode.ai/workspace/wrk_abc/go", cookies); err != nil {
		t.Fatal(err)
	}
	want := []string{"set-cookie", "navigate", "wait"}
	if !reflect.DeepEqual(browser.operations, want) {
		t.Fatalf("operations = %#v, want %#v", browser.operations, want)
	}
}

// TestOpenCodeCookieHeaderDropsUnsafeCapture proves the capture-time
// serialiser refuses cookies whose name or value contains an unsafe
// character. Cookies that fail the check are dropped from the header
// so a malformed capture cannot reach the persisted credential.
func TestOpenCodeCookieHeaderDropsUnsafeCapture(t *testing.T) {
	cookies := []browserauth.Cookie{
		{Name: "session", Value: "YWJj==", Domain: "opencode.ai"},
		{Name: "ok", Value: "safe", Domain: "opencode.ai"},
		{Name: "bad\r\n", Value: "v", Domain: "opencode.ai"},
		{Name: "quote", Value: `v"`, Domain: "opencode.ai"},
		{Name: "", Value: "empty", Domain: "opencode.ai"},
		{Name: "emptyVal", Value: "", Domain: "opencode.ai"},
		{Name: "wronghost", Value: "x", Domain: "example.com"},
		{Name: "auth", Value: "sub", Domain: "auth.opencode.ai"},
	}
	got := openCodeCookieHeader(cookies)
	want := "session=YWJj==; ok=safe"
	if got != want {
		t.Fatalf("openCodeCookieHeader = %q, want %q", got, want)
	}
}

// TestOpenCodeCookieHeaderEmptyWhenAllUnsafe proves the capture path
// yields an empty header when every captured cookie is unsafe. The
// caller (runOpenCodeLogin) sees an empty header and keeps polling
// instead of returning a corrupted credential.
func TestOpenCodeCookieHeaderEmptyWhenAllUnsafe(t *testing.T) {
	cookies := []browserauth.Cookie{
		{Name: "bad\r\n", Value: "v", Domain: "opencode.ai"},
		{Name: "wronghost", Value: "x", Domain: "example.com"},
	}
	if got := openCodeCookieHeader(cookies); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// TestFilterOpenCodeCookiesAppliesSafetyCheck proves the
// capture-time filter that feeds the serialiser also rejects
// cookies whose name or value falls outside the safe character set.
// Otherwise the two paths could disagree: the filter would admit a
// bad cookie and the serialiser would silently drop it, but
// downstream code would still observe a partial credential.
func TestFilterOpenCodeCookiesAppliesSafetyCheck(t *testing.T) {
	in := []browserauth.Cookie{
		{Name: "session", Value: "YWJj==", Domain: "opencode.ai"},
		{Name: "ok", Value: "safe", Domain: "opencode.ai"},
		{Name: "bad\r\n", Value: "v", Domain: "opencode.ai"},
		{Name: "quote", Value: `v"`, Domain: "opencode.ai"},
		{Name: "wronghost", Value: "x", Domain: "example.com"},
		{Name: "auth", Value: "sub", Domain: "auth.opencode.ai"},
	}
	got := filterOpenCodeCookies(in)
	if len(got) != 2 {
		t.Fatalf("filter returned %d cookies, want 2: %#v", len(got), got)
	}
	if got[0].Name != "session" || got[1].Name != "ok" {
		t.Fatalf("filter dropped the wrong entries: %#v", got)
	}
}

// TestFilterOpenCodeCookiesDropsUnsafe proves the filter that feeds
// openCodeCookieHeader also rejects cookies whose name or value
// contains an unsafe character. Otherwise the two paths would
// disagree: the filter would admit a bad cookie and the serialiser
// would silently drop it, but downstream code would still observe
// a partial credential.
func TestFilterOpenCodeCookiesDropsUnsafe(t *testing.T) {
	in := []browserauth.Cookie{
		{Name: "session", Value: "YWJj==", Domain: "opencode.ai"},
		{Name: "ok", Value: "safe", Domain: "opencode.ai"},
		{Name: "bad\r\n", Value: "v", Domain: "opencode.ai"},
		{Name: "quote", Value: `v"`, Domain: "opencode.ai"},
		{Name: "wronghost", Value: "x", Domain: "example.com"},
		{Name: "auth", Value: "sub", Domain: "auth.opencode.ai"},
	}
	got := filterOpenCodeCookies(in)
	if len(got) != 2 {
		t.Fatalf("filter returned %d cookies, want 2: %#v", len(got), got)
	}
	if got[0].Name != "session" || got[1].Name != "ok" {
		t.Fatalf("filter dropped the wrong entries: %#v", got)
	}
}
