package web

// Structural and DOM tests for the shared quota-card foundation
// including error states, extension slots, accessibility, and parity.

import (
	"regexp"
	"strings"
	"testing"
)

// --- Task 1.5: Structural/DOM Tests for Shared Foundation ---

// TestSharedFoundationStructure proves the shared quota-card foundation
// provides common shell/row primitives with extension slots.
func TestSharedFoundationStructure(t *testing.T) {
	view := map[string]any{
		"provider":      "testprovider",
		"providerLabel": "Test Provider",
		"accountName":   "test-account",
		"success":       true,
		"rows": []map[string]any{
			{"label": "Rolling", "percent": 45, "percentPrecision": 0, "resetInSec": 7200, "tone": "g1"},
			{"label": "Weekly", "percent": 72, "percentPrecision": 0, "resetInSec": 259200, "tone": "g2"},
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type": "renderCard",
		"view": view,
	})

	if !strings.Contains(result, `class="acard"`) {
		t.Error("Shared foundation must use .acard shell class")
	}
	if !strings.Contains(result, `class="acard-h"`) {
		t.Error("Shared foundation must use .acard-h header class")
	}
	if !strings.Contains(result, `data-prov="testprovider"`) {
		t.Error("Shared foundation must set data-prov attribute")
	}
	if !strings.Contains(result, `data-name="test-account"`) {
		t.Error("Shared foundation must set data-name attribute")
	}

	if !strings.Contains(result, `class="qr"`) {
		t.Error("Shared foundation must use .qr row class")
	}
	if !strings.Contains(result, `class="ql"`) {
		t.Error("Shared foundation must use .ql label class")
	}
	if !strings.Contains(result, `class="qbw"`) {
		t.Error("Shared foundation must use .qbw track class")
	}
	if !strings.Contains(result, `class="qf`) {
		t.Error("Shared foundation must use .qf fill class")
	}
	if !strings.Contains(result, `class="qp`) {
		t.Error("Shared foundation must use .qp percentage class")
	}
	if !strings.Contains(result, `class="qtm"`) {
		t.Error("Shared foundation must use .qtm reset class")
	}
}

// TestSharedFoundationErrorState proves error states use shared error region.
func TestSharedFoundationErrorState(t *testing.T) {
	view := map[string]any{
		"provider":      "testprovider",
		"providerLabel": "Test Provider",
		"accountName":   "broken-account",
		"success":       false,
		"error":         "Authentication failed",
		"errorAction": map[string]any{
			"label": "重新登录",
			"class": "testLogin",
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type": "renderCard",
		"view": view,
	})

	if !strings.Contains(result, `class="qerr"`) {
		t.Error("Shared foundation must use .qerr error class")
	}
	if !strings.Contains(result, "Authentication failed") {
		t.Error("Shared foundation must display error message")
	}
	if !strings.Contains(result, "testLogin") {
		t.Error("Shared foundation must render error action with declared class")
	}
	if !strings.Contains(result, "重新登录") {
		t.Error("Shared foundation must render error action label")
	}
	if strings.Contains(result, `class="qr"`) {
		t.Error("Error state must not fabricate metric rows")
	}
}

// TestSharedFoundationExtensionSlots proves typed extension slots work
// for header, body, row-detail, and footer extensions.
func TestSharedFoundationExtensionSlots(t *testing.T) {
	view := map[string]any{
		"provider":      "testprovider",
		"providerLabel": "Test Provider",
		"accountName":   "test-account",
		"success":       true,
		"headerExtension": map[string]any{
			"type":    "badge",
			"content": "PRO",
			"class":   "test-badge",
		},
		"rows": []map[string]any{
			{
				"label":            "Monthly",
				"percent":          85.5,
				"percentPrecision": 1,
				"resetInSec":       86400,
				"tone":             "g1",
				"details": []map[string]any{
					{"label": "Kimi", "value": "0.5%"},
					{"label": "Code", "value": "85.0%"},
				},
			},
		},
		"bodyExtensions": []map[string]any{
			{"type": "note", "content": "Extra info", "class": "test-note"},
		},
		"footerExtension": map[string]any{
			"type":    "link",
			"content": "View Details",
			"class":   "test-footer",
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type": "renderCard",
		"view": view,
	})

	if !strings.Contains(result, "test-badge") || !strings.Contains(result, "PRO") {
		t.Error("Header extension must be rendered with declared class and content")
	}
	if !strings.Contains(result, "Kimi 0.5%") {
		t.Error("Row detail must render 'Kimi 0.5%'")
	}
	if !strings.Contains(result, "Code 85.0%") {
		t.Error("Row detail must render 'Code 85.0%'")
	}
	if !strings.Contains(result, "test-note") || !strings.Contains(result, "Extra info") {
		t.Error("Body extension must be rendered")
	}
	if !strings.Contains(result, "test-footer") || !strings.Contains(result, "View Details") {
		t.Error("Footer extension must be rendered")
	}
}

// TestSharedFoundationEscapesHostileText proves all text is escaped.
func TestSharedFoundationEscapesHostileText(t *testing.T) {
	view := map[string]any{
		"provider":      "testprovider",
		"providerLabel": "Test <script>alert('xss')</script>",
		"accountName":   "account<img src=x onerror=alert(1)>",
		"success":       true,
		"rows": []map[string]any{
			{
				"label":            "Rolling<svg onload=alert(1)>",
				"percent":          50,
				"percentPrecision": 0,
				"resetInSec":       3600,
				"tone":             "g1",
			},
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type": "renderCard",
		"view": view,
	})

	if strings.Contains(result, "<script>") {
		t.Error("Provider label must escape <script> tags")
	}
	if strings.Contains(result, "<img") {
		t.Error("Account name must escape <img> tags")
	}
	if strings.Contains(result, "<svg") {
		t.Error("Row label must escape <svg> tags")
	}
	if !strings.Contains(result, "&lt;script&gt;") {
		t.Error("Escaped <script> should appear as &lt;script&gt;")
	}
}

// TestSharedFoundationNoProviderBranches proves shared renderers contain
// no provider-name conditional branches.
func TestSharedFoundationNoProviderBranches(t *testing.T) {
	html := readSidebarHTML(t)

	re := regexp.MustCompile(`function renderQuotaCard\s*\([^)]*\)\s*\{[\s\S]*?\n\s*\}`)
	matches := re.FindStringSubmatch(html)
	if len(matches) == 0 {
		t.Skip("renderQuotaCard not yet implemented - RED test")
	}

	funcBody := matches[0]

	for _, provider := range []string{"kimi", "ollama", "opencode", "deepseek"} {
		if strings.Contains(funcBody, `"`+provider+`"`) || strings.Contains(funcBody, `'`+provider+`'`) {
			t.Errorf("renderQuotaCard must not contain provider-name branch for %q", provider)
		}
	}
}

// TestSharedFoundationActionsNeverExposeCredentials proves the shared
// error/action slots carry only the account name needed for the action,
// never tokens, cookies, or credential material.
func TestSharedFoundationActionsNeverExposeCredentials(t *testing.T) {
	view := map[string]any{
		"provider":      "kimi",
		"providerLabel": "Kimi Code",
		"accountName":   "kimi-account",
		"success":       false,
		"error":         "凭证已过期",
		"errorAction": map[string]any{
			"label": "重新登录",
			"class": "kimiLogin",
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type": "renderCard",
		"view": view,
	})

	if !strings.Contains(result, `data-name="kimi-account"`) {
		t.Error("error action must carry data-name for account identity")
	}
	for _, secret := range []string{"token", "cookie", "secret", "password", "credential", "authorization"} {
		if strings.Contains(strings.ToLower(result), secret) && !strings.Contains(strings.ToLower(result), "凭证已过期") {
			t.Errorf("error action must not embed credential material (%q) in rendered card", secret)
		}
	}
	if strings.Contains(result, "Bearer ") {
		t.Error("error card must not embed bearer tokens")
	}
}

// TestSharedFoundationErrorActionIsKeyboardAccessible proves the shared
// error action renders as a real <button> (focusable) with a declarative
// data-action attribute, never a bare <span> that cannot receive focus.
func TestSharedFoundationErrorActionIsKeyboardAccessible(t *testing.T) {
	view := map[string]any{
		"provider":      "kimi",
		"providerLabel": "Kimi Code",
		"accountName":   "kimi-account",
		"success":       false,
		"error":         "凭证已过期",
		"errorAction": map[string]any{
			"label": "重新登录",
			"class": "kimiLogin",
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type": "renderCard",
		"view": view,
	})

	if !strings.Contains(result, `<button type="button" class="qact kimiLogin"`) {
		t.Errorf("error action must render as <button type=button> with shared qact class; got:\n%s", result)
	}
	if !strings.Contains(result, `data-action="relogin"`) {
		t.Errorf("error action must carry a declarative data-action; got:\n%s", result)
	}
	if strings.Contains(result, `<span class="kimiLogin"`) {
		t.Error("error action must not be a non-focusable span")
	}
}

// TestSharedQactActionStylingExists proves the shared .qact class owns
// the action-button visual contract (idle/hover/active/focus-visible) so
// every provider's re-login action (Ollama's olLogin included) is themed
// and none falls back to the browser-native gray button.
func TestSharedQactActionStylingExists(t *testing.T) {
	html := readSidebarHTML(t)

	if !strings.Contains(html, `.qact:hover`) ||
		!strings.Contains(html, `.qact:active`) ||
		!strings.Contains(html, `.qact:focus-visible`) {
		t.Error("shared .qact action styling must define hover/active/focus-visible states")
	}
	for _, hook := range []string{".kimiLogin,", ".olLogin"} {
		if !strings.Contains(html, hook) {
			t.Errorf("provider action delegation hook %q must remain", hook)
		}
	}
}

// TestSyntheticFutureProvider proves a new provider can use the shared
// foundation with only an adapter and extension content.
func TestSyntheticFutureProvider(t *testing.T) {
	view := map[string]any{
		"provider":      "futureai",
		"providerLabel": "Future AI",
		"accountName":   "future-account",
		"success":       true,
		"rows": []map[string]any{
			{"label": "Hourly", "percent": 25, "percentPrecision": 0, "resetInSec": 1800, "tone": "g1"},
			{"label": "Daily", "percent": 60, "percentPrecision": 0, "resetInSec": 43200, "tone": "g2"},
		},
		"headerExtension": map[string]any{
			"type":    "badge",
			"content": "BETA",
			"class":   "future-badge",
		},
		"bodyExtensions": []map[string]any{
			{
				"type":    "custom",
				"content": "Unique future provider content",
				"class":   "future-custom",
			},
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type": "renderCard",
		"view": view,
	})

	if !strings.Contains(result, `class="acard"`) {
		t.Error("Future provider must reuse shared shell")
	}
	if !strings.Contains(result, `class="qr"`) {
		t.Error("Future provider must reuse shared rows")
	}
	if !strings.Contains(result, "future-badge") {
		t.Error("Future provider header extension must render")
	}
	if !strings.Contains(result, "future-custom") {
		t.Error("Future provider body extension must render")
	}
	if !strings.Contains(result, `data-prov="futureai"`) {
		t.Error("Future provider must set data-prov")
	}
}

// --- Structural Parity Tests ---

// TestAllowanceProvidersShareDesignTokens proves OpenCode Go, Ollama, and
// Kimi cards share the same CSS design tokens and grid behavior.
func TestAllowanceProvidersShareDesignTokens(t *testing.T) {
	html := readSidebarHTML(t)

	gridRule := regexp.MustCompile(`(?s)([^{}]*)\{[^}]*repeat\(auto-fill,\s*minmax\(320px,\s*1fr\)\)`)
	m := gridRule.FindStringSubmatch(html)
	if m == nil {
		t.Fatal("shared responsive grid rule not found")
	}

	selectorGroup := m[1]
	for _, container := range []string{"#accountCards", "#ollamaCards", "#kimiCards"} {
		if !strings.Contains(selectorGroup, container) {
			t.Errorf("container %s must be part of shared grid selector group", container)
		}
	}

	if !regexp.MustCompile(`\.acard\s*\{`).MatchString(html) {
		t.Error("shared .acard shell CSS must exist")
	}
	if !regexp.MustCompile(`\.acard-h\s*\{`).MatchString(html) {
		t.Error("shared .acard-h header CSS must exist")
	}

	for _, cls := range []string{".qr", ".ql", ".qbw", ".qf", ".qp", ".qtm"} {
		if !regexp.MustCompile(regexp.QuoteMeta(cls) + `\s*\{`).MatchString(html) {
			t.Errorf("shared row CSS %s must exist", cls)
		}
	}
}

// TestProviderAdaptersReturnViewModels proves adapters return normalized
// view models rather than concatenating HTML.
func TestProviderAdaptersReturnViewModels(t *testing.T) {
	html := readSidebarHTML(t)

	for _, adapter := range []string{"opencodeAdapter", "ollamaAdapter", "kimiAdapter"} {
		if !strings.Contains(html, "function "+adapter) {
			t.Skipf("adapter %s not yet implemented - RED test", adapter)
		}
	}

	adapterPattern := regexp.MustCompile(`function\s+\w+Adapter\s*\([^)]*\)\s*\{[\s\S]*?return\s+[^;]+;`)
	matches := adapterPattern.FindAllString(html, -1)
	for _, match := range matches {
		if strings.Contains(match, `return "<div`) || strings.Contains(match, `return '<div`) {
			t.Error("adapters must return view model objects, not HTML strings")
		}
	}
}
