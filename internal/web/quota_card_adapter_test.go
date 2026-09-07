package web

// Characterization tests for existing provider card renderers
// (OpenCode Go, Ollama, Kimi).

import (
	"strings"
	"testing"
)

// --- Task 1.2: Characterization Tests for Existing Renderers ---

// TestOpenCodeGoLapsedSubscriptionRendersNotice (quota-exhausted regression,
// OBSERVED 2026-08-19) proves a lapsed OpenCode subscription (quota RPC
// returns null → backend marks quota.lapsed) renders an explicit 失效 notice
// instead of a healthy empty meter card: no Rolling/Weekly rows, no percent
// values, and the "订阅已失效" content in a .qlapse body extension.
func TestOpenCodeGoLapsedSubscriptionRendersNotice(t *testing.T) {
	dto := map[string]any{
		"name":    "opencode-account",
		"success": true,
		"quota": map[string]any{
			"lapsed": true,
			"rolling": map[string]any{"usage_percent": 0, "status": "unavailable"},
			"weekly":  map[string]any{"usage_percent": 0, "status": "unavailable"},
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type":    "adapter",
		"adapter": "opencode",
		"dto":     dto,
	})

	if !strings.Contains(result, "订阅已失效") {
		t.Error("lapsed card must render the 订阅已失效 notice")
	}
	if strings.Contains(result, "Rolling") || strings.Contains(result, "Weekly") {
		t.Error("lapsed card must not render metric rows")
	}
	if strings.Contains(result, "0%") {
		t.Error("lapsed card must not render zero percent values")
	}
	if !strings.Contains(result, "qlapse") {
		t.Error("lapsed notice must use .qlapse class")
	}
}

// TestOpenCodeGoCardStructure proves the OpenCode Go card uses the shared
// foundation structure with Rolling, Weekly, and optional Monthly rows.
func TestOpenCodeGoCardStructure(t *testing.T) {
	dto := map[string]any{
		"name":    "opencode-test",
		"success": true,
		"quota": map[string]any{
			"rolling": map[string]any{"usage_percent": 45.5, "reset_display": "2h", "reset_in_sec": 7200},
			"weekly":  map[string]any{"usage_percent": 72.0, "reset_display": "3d", "reset_in_sec": 259200},
			"monthly": map[string]any{"usage_percent": 15.0, "reset_display": "15d", "reset_in_sec": 1296000},
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type":    "adapter",
		"adapter": "opencode",
		"dto":     dto,
	})

	// Verify card shell structure
	if !strings.Contains(result, `data-prov="opencode"`) {
		t.Error("OpenCode card must have data-prov=opencode")
	}
	if !strings.Contains(result, "OpenCode Go") {
		t.Error("OpenCode card must display provider label")
	}

	// Verify row order: Rolling, Weekly, Monthly
	iRolling := strings.Index(result, "Rolling")
	iWeekly := strings.Index(result, "Weekly")
	iMonthly := strings.Index(result, "Monthly")
	if !(iRolling >= 0 && iWeekly > iRolling && iMonthly > iWeekly) {
		t.Errorf("OpenCode rows must be in order Rolling, Weekly, Monthly; got indices %d, %d, %d", iRolling, iWeekly, iMonthly)
	}

	// Verify decimal percentages preserved
	if !strings.Contains(result, "45.5%") {
		t.Error("OpenCode Rolling must display 45.5%")
	}
	if !strings.Contains(result, "72%") {
		t.Error("OpenCode Weekly must display 72%")
	}
	if !strings.Contains(result, "15%") {
		t.Error("OpenCode Monthly must display 15%")
	}
}

// TestOllamaCardStructure proves Ollama card uses the shared foundation
// with Session and Weekly rows.
func TestOllamaCardStructure(t *testing.T) {
	dto := map[string]any{
		"name":    "ollama-test",
		"success": true,
		"quota": map[string]any{
			"rolling": map[string]any{"usage_percent": 10.0, "reset_display": "30m", "reset_in_sec": 1800},
			"weekly":  map[string]any{"usage_percent": 85.5, "reset_display": "5d", "reset_in_sec": 432000},
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type":    "adapter",
		"adapter": "ollama",
		"dto":     dto,
	})

	// Verify card shell structure
	if !strings.Contains(result, `data-prov="ollama"`) {
		t.Error("Ollama card must have data-prov=ollama")
	}
	if !strings.Contains(result, "Ollama") {
		t.Error("Ollama card must display provider label")
	}

	// Verify row labels: Session, Weekly
	if !strings.Contains(result, "Session") {
		t.Error("Ollama card must display Session row")
	}
	if !strings.Contains(result, "Weekly") {
		t.Error("Ollama card must display Weekly row")
	}

	// Verify row order: Session before Weekly
	iSession := strings.Index(result, "Session")
	iWeekly := strings.Index(result, "Weekly")
	if !(iSession >= 0 && iWeekly > iSession) {
		t.Errorf("Ollama rows must be in order Session, Weekly; got indices %d, %d", iSession, iWeekly)
	}

	// Verify decimal percentages preserved
	if !strings.Contains(result, "85.5%") {
		t.Error("Ollama Weekly must display 85.5%")
	}
}

// TestOllamaErrorWithReLogin proves Ollama error cards render with the
// "重新登录" action using the shared error action slot.
func TestOllamaErrorWithReLogin(t *testing.T) {
	dto := map[string]any{
		"name":    "ollama-broken",
		"success": false,
		"error":   "Cookie expired",
	}

	result := runQuotaCardTest(t, map[string]any{
		"type":    "adapter",
		"adapter": "ollama",
		"dto":     dto,
	})

	// Verify error region rendered
	if !strings.Contains(result, `class="qerr"`) {
		t.Error("Ollama error card must have .qerr element")
	}
	if !strings.Contains(result, "Cookie expired") {
		t.Error("Ollama error card must display error message")
	}

	// Verify re-login action rendered
	if !strings.Contains(result, "olLogin") {
		t.Error("Ollama error card must have olLogin action button")
	}
	if !strings.Contains(result, "重新登录") {
		t.Error("Ollama error card must have '重新登录' button text")
	}
}

// TestKimiCardStructure proves Kimi card uses the shared foundation with
// 5h, 7d, and total (Monthly) rows, preserving the Kimi/Code breakdown.
func TestKimiCardStructure(t *testing.T) {
	dto := map[string]any{
		"name":    "kimi-test",
		"success": true,
		"quota": map[string]any{
			"five_hour": map[string]any{"usage_percent": 7.9, "reset_display": "07-31 16:58", "reset_in_sec": 18000},
			"seven_day": map[string]any{"usage_percent": 56.45, "reset_display": "08-04 23:58", "reset_in_sec": 172800},
			"total": map[string]any{
				"total_percent": 11.86,
				"kimi_percent":  0.03,
				"code_percent":  11.83,
				"reset_display": "2026-08-28",
				"reset_in_sec":  1987200,
			},
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type":    "adapter",
		"adapter": "kimi",
		"dto":     dto,
	})

	// Verify card shell structure
	if !strings.Contains(result, `data-prov="kimi"`) {
		t.Error("Kimi card must have data-prov=kimi")
	}
	if !strings.Contains(result, "Kimi Code") {
		t.Error("Kimi card must display provider label")
	}

	// Verify row order: Rolling, Weekly, Monthly
	iRolling := strings.Index(result, "Rolling")
	iWeekly := strings.Index(result, "Weekly")
	iMonthly := strings.Index(result, "Monthly")
	if !(iRolling >= 0 && iWeekly > iRolling && iMonthly > iWeekly) {
		t.Errorf("Kimi rows must be in order Rolling, Weekly, Monthly; got indices %d, %d, %d", iRolling, iWeekly, iMonthly)
	}

	// Verify decimal percentages preserved
	if !strings.Contains(result, "7.9%") {
		t.Error("Kimi Rolling must display 7.9%")
	}
	if !strings.Contains(result, "56.45%") {
		t.Error("Kimi Weekly must display 56.45%")
	}
	if !strings.Contains(result, "11.86%") {
		t.Error("Kimi Monthly must display 11.86%")
	}

	// Verify Kimi/Code breakdown extension
	if !strings.Contains(result, "Kimi 0.03%") {
		t.Error("Kimi card must display 'Kimi 0.03%' breakdown")
	}
	if !strings.Contains(result, "Code 11.83%") {
		t.Error("Kimi card must display 'Code 11.83%' breakdown")
	}

	// Compact duration format verification
	if !strings.Contains(result, "5h") || !strings.Contains(result, "2d") || !strings.Contains(result, "23d") {
		t.Error("Kimi card must use compact duration format (5h, 2d, 23d) from reset_in_sec")
	}
}

// --- Task 1.4: Kimi Percentage Preservation Test ---

// TestKimiPercentagePreservedWithCompactResets proves that specific Kimi
// percentage values remain unchanged while resets switch to compact format.
func TestKimiPercentagePreservedWithCompactResets(t *testing.T) {
	dto := map[string]any{
		"name":    "kimi-test",
		"success": true,
		"quota": map[string]any{
			"five_hour": map[string]any{"usage_percent": 28.21, "reset_display": "07-31 16:58", "reset_in_sec": 18000},
			"seven_day": map[string]any{"usage_percent": 74.86, "reset_display": "08-04 23:58", "reset_in_sec": 172800},
			"total": map[string]any{
				"total_percent": 15.72,
				"kimi_percent":  0.03,
				"code_percent":  15.69,
				"reset_display": "2026-08-28",
				"reset_in_sec":  1987200,
			},
		},
	}

	result := runQuotaCardTest(t, map[string]any{
		"type":    "adapter",
		"adapter": "kimi",
		"dto":     dto,
	})

	// Percentages MUST remain unchanged
	for _, pct := range []string{"28.21%", "74.86%", "15.72%"} {
		if !strings.Contains(result, pct) {
			t.Errorf("Kimi percentage %s must be preserved exactly", pct)
		}
	}

	// Resets MUST use compact format
	for _, reset := range []string{"5h", "2d", "23d"} {
		if !strings.Contains(result, reset) {
			t.Errorf("Kimi reset must use compact format %s", reset)
		}
	}

	// Absolute date strings MUST NOT appear
	for _, absolute := range []string{"07-31 16:58", "08-04 23:58", "2026-08-28"} {
		if strings.Contains(result, absolute) {
			t.Errorf("Kimi card must not display absolute reset %q", absolute)
		}
	}
}
