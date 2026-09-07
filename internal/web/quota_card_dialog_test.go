package web

// Add Account modal dialog tests for the provider registry,
// accessibility, data types, and action wiring.

import (
	"regexp"
	"strings"
	"testing"
)

// --- Task 1.6: Add Account dialog RED tests ---

// TestProviderOptionsAreKeyboardAccessibleButtons proves the registry-
// rendered Add Account choices are <button> elements (keyboard focusable),
// not bare <div> tiles.
func TestProviderOptionsAreKeyboardAccessibleButtons(t *testing.T) {
	html := readSidebarHTML(t)

	// Options are created as real buttons from the registry.
	if !strings.Contains(html, `document.createElement("button")`) {
		t.Error("provider options must be created as <button> elements")
	}
	// The option primitive must expose a keyboard-focus state.
	if !regexp.MustCompile(`\.prov\s*:\s*focus-visible`).MatchString(html) {
		t.Error(".prov must define :focus-visible for keyboard navigation")
	}
}

// TestSharedEventDelegationIsDataDriven proves account-level click
// delegation is keyed by declarative action classes (olLogin/kimiLogin)
// on the rendered action element, not by provider-name branches in the
// shared renderer.
func TestSharedEventDelegationIsDataDriven(t *testing.T) {
	html := readSidebarHTML(t)

	// Delegated listeners must match the action class and read data-name.
	for _, pair := range [][2]string{
		{"olLogin", "olDoLogin"},
		{"kimiLogin", "kimiDoLogin"},
	} {
		cls, fn := pair[0], pair[1]
		if !strings.Contains(html, `contains("`+cls+`")`) {
			t.Errorf("delegation must match %s by class name", cls)
		}
		if !strings.Contains(html, `data-name`) {
			t.Errorf("delegation must read data-name for account identity")
		}
		if !strings.Contains(html, fn) {
			t.Errorf("delegation for %s must dispatch to %s", cls, fn)
		}
	}
}

// TestAddAccountOptionsAreNameOnly proves the Add Account dialog renders
// all four provider options from a single data-driven registry, each
// showing only the centered provider name with no .prov-d subtitle node
// or obsolete description text.
func TestAddAccountOptionsAreNameOnly(t *testing.T) {
	html := readSidebarHTML(t)

	// The provider registry must define exactly the four allowance
	// providers with type + display name (data-driven selector).
	for _, prov := range []struct{ typ, label string }{
		{"opencode", "OpenCode Go"},
		{"deepseek", "DeepSeek"},
		{"ollama", "Ollama"},
		{"kimi", "Kimi Code"},
	} {
		if !strings.Contains(html, `type: "`+prov.typ+`"`) || !strings.Contains(html, `label: "`+prov.label+`"`) {
			t.Errorf("provider registry must define %s with label %q", prov.typ, prov.label)
		}
	}
	if !strings.Contains(html, `quotaProviders`) {
		t.Error("provider registry (quotaProviders) must exist")
	}
	// Options must be rendered FROM the registry (data-driven), not
	// hand-written per provider.
	if !strings.Contains(html, `quotaProviders.forEach`) {
		t.Error("step-one options must be rendered from the registry (quotaProviders.forEach)")
	}
	if !strings.Contains(html, `modalProviders`) {
		t.Error("step-one option container #modalProviders must exist")
	}

	// No .prov-d subtitle nodes may remain anywhere.
	if strings.Contains(html, "prov-d") {
		t.Error("provider options must not contain .prov-d subtitle nodes")
	}

	// Obsolete subtitle descriptions must be gone.
	for _, stale := range []string{"套餐额度", "用量 / 余额", "Cloud 用量", "Rolling / Weekly / Monthly"} {
		if strings.Contains(html, stale) {
			t.Errorf("obsolete provider subtitle %q must be removed from Add Account dialog", stale)
		}
	}

	// The option primitive must center the name.
	if !regexp.MustCompile(`\.prov\s*\{[^}]*justify-content\s*:\s*center`).MatchString(html) {
		t.Error(".prov option must center its content (justify-content: center)")
	}
}

// TestProviderRegistryUnknownTypeErrors proves the data-driven selector
// errors explicitly on an unknown provider type instead of silently
// falling back to a known provider (e.g. Kimi).
func TestProviderRegistryUnknownTypeErrors(t *testing.T) {
	html := readSidebarHTML(t)

	// quotaProviderByType must return null for unknown types.
	if !strings.Contains(html, `return null`) {
		t.Error("quotaProviderByType must return null for unknown provider types")
	}
	if !strings.Contains(html, `未知服务商`) {
		t.Error("unknown provider type must surface an explicit error (未知服务商)")
	}
	// The step-two label and default name must come from the registry entry,
	// not a hard-coded if/else chain that falls back to Kimi.
	if strings.Contains(html, `: "Kimi Code") + " · 账户名称"`) ||
		strings.Contains(html, `: "Kimi";`) {
		t.Error("step-two label/default-name must not fall back to Kimi via a ternary chain")
	}
}

// TestAddAccountDataTypeMappingsRetained proves removing the subtitles keeps
// the exact data-type → step-two label / default name / login dispatch wiring
// (now derived from the registry).
func TestAddAccountDataTypeMappingsRetained(t *testing.T) {
	html := readSidebarHTML(t)

	// Step-two provider label mapping must still exist (modalProvLabel),
	// now sourced from the registry entry.
	for _, want := range []string{`modalProvLabel`, `p.label + " · 账户名称"`, `p.defaultName`} {
		if !strings.Contains(html, want) {
			t.Errorf("Add Account dialog missing %q (step-two label wiring)", want)
		}
	}

	// Login dispatch must go through the registry's login name.
	if !strings.Contains(html, `window[p.login]`) {
		t.Error("login dispatch must be registry-driven (window[p.login])")
	}

	// All four login functions referenced by the registry must exist.
	for _, fn := range []string{"ocDoLogin", "dsDoLogin", "olDoLogin", "kimiDoLogin"} {
		if !strings.Contains(html, `login: "`+fn+`"`) || !strings.Contains(html, `function `+fn) {
			t.Errorf("registry login %s must be defined and wired", fn)
		}
	}

	// Modal close behavior must be retained.
	if !strings.Contains(html, `id="modalClose"`) || !strings.Contains(html, `closeModal`) {
		t.Error("modal close behavior must be retained")
	}
}
