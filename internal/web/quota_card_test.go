package web

// RED tests for the unified quota-card template (openspec change
// unify-quota-card-template). These tests characterize the EXPECTED
// behavior of the shared foundation before it is implemented.
//
// Layer 1: Compact duration formatter tests (formatDurationCompact)
// Layer 2: Row renderer safety & refresh replacement scenario

import (
	"strings"
	"testing"
)

// --- Task 1.3: Compact Duration Formatter Tests ---

// TestFormatDurationCompactBoundaries proves the formatter matches Go
// FormatDurationCompact behavior: floors the largest applicable whole unit
// and uses s/m/h/d vocabulary.
func TestFormatDurationCompactBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		seconds  float64
		expected string
	}{
		// Seconds boundary (< 60)
		{"zero seconds", 0, "0s"},
		{"one second", 1, "1s"},
		{"59 seconds", 59, "59s"},
		// Minutes boundary (< 3600)
		{"60 seconds = 1 minute", 60, "1m"},
		{"90 seconds = 1 minute (floored)", 90, "1m"},
		{"3599 seconds = 59 minutes", 3599, "59m"},
		// Hours boundary (< 86400)
		{"3600 seconds = 1 hour", 3600, "1h"},
		{"5400 seconds = 1 hour (floored)", 5400, "1h"},
		{"86399 seconds = 23 hours", 86399, "23h"},
		// Days boundary (>= 86400)
		{"86400 seconds = 1 day", 86400, "1d"},
		{"90000 seconds = 1 day (floored)", 90000, "1d"},
		// Specific values from spec
		{"18000 seconds = 5 hours", 18000, "5h"},
		{"172800 seconds = 2 days", 172800, "2d"},
		{"1987200 seconds = 23 days", 1987200, "23d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runQuotaCardTest(t, map[string]any{
				"type":    "formatDuration",
				"seconds": tt.seconds,
			})
			if result != tt.expected {
				t.Errorf("formatDurationCompact(%v) = %q, want %q", tt.seconds, result, tt.expected)
			}
		})
	}
}

// TestFormatDurationCompactInvalidValues proves invalid values are clamped
// to safe non-negative display.
func TestFormatDurationCompactInvalidValues(t *testing.T) {
	tests := []struct {
		name     string
		seconds  any
		expected string
	}{
		{"negative seconds", -100, "0s"},
		{"negative one", -1, "0s"},
		{"NaN", "NaN", "0s"},
		{"undefined", nil, "0s"},
		{"Infinity", "Infinity", "0s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runQuotaCardTest(t, map[string]any{
				"type":    "formatDuration",
				"seconds": tt.seconds,
			})
			if result != tt.expected {
				t.Errorf("formatDurationCompact(%v) = %q, want %q", tt.seconds, result, tt.expected)
			}
		})
	}
}

// --- Row renderer safety (WARNING: null/NaN/Infinity) ---

// TestRenderQuotaRowHandlesInvalidRows proves the shared row renderer
// never throws on null/undefined rows and never emits width:NaN% or
// Infinity% — a single validated value drives color, fill, and text.
func TestRenderQuotaRowHandlesInvalidRows(t *testing.T) {
	// null row must not throw and must produce no markup.
	nullOut := runQuotaCardTest(t, map[string]any{
		"type": "renderRow",
		"row":  nil,
	})
	if nullOut != "" {
		t.Errorf("renderQuotaRow(null) should render empty, got: %q", nullOut)
	}

	// missing percent → placeholder, no throw.
	missingOut := runQuotaCardTest(t, map[string]any{
		"type": "renderRow",
		"row":  map[string]any{"label": "Rolling"},
	})
	if !strings.Contains(missingOut, "Rolling") || !strings.Contains(missingOut, "—") {
		t.Errorf("row without percent must render label placeholder with —; got: %q", missingOut)
	}

	// NaN percent → placeholder, never width:NaN% or "NaN%".
	nanOut := runQuotaCardTest(t, map[string]any{
		"type": "renderRow",
		"row":  map[string]any{"label": "Weekly", "percent": "NaN", "percentPrecision": 0, "resetInSec": 3600},
	})
	if strings.Contains(nanOut, "NaN") || strings.Contains(nanOut, "width:NaN") {
		t.Errorf("NaN percent must render a safe placeholder, got: %q", nanOut)
	}

	// Infinity percent → placeholder, never a full bar or Infinity%.
	infOut := runQuotaCardTest(t, map[string]any{
		"type": "renderRow",
		"row":  map[string]any{"label": "Monthly", "percent": "Infinity", "percentPrecision": 0, "resetInSec": 3600},
	})
	if strings.Contains(infOut, "Infinity") || strings.Contains(infOut, "width:Infinity") {
		t.Errorf("Infinity percent must render a safe placeholder, got: %q", infOut)
	}

	// negative percent → clamped fill at 0% but text shows validated value.
	negOut := runQuotaCardTest(t, map[string]any{
		"type": "renderRow",
		"row":  map[string]any{"label": "Rolling", "percent": -5, "percentPrecision": 0, "resetInSec": 1800},
	})
	if strings.Contains(negOut, "width:-5%") || !strings.Contains(negOut, "width:0%") {
		t.Errorf("negative percent must clamp fill width to 0%%, got: %q", negOut)
	}
}

// --- Refresh replacement scenario (WARNING) ---

// TestRefreshReplacementUpdatesCardData proves re-rendering a card with new
// DTO data for the same account replaces rows, fills, percentages, reset
// durations, and details while preserving provider and account identity.
func TestRefreshReplacementUpdatesCardData(t *testing.T) {
	oldDTO := map[string]any{
		"name":    "kimi-refresh",
		"success": true,
		"quota": map[string]any{
			"five_hour": map[string]any{"usage_percent": 10.5, "reset_display": "OLD-5H", "reset_in_sec": 3600},
			"seven_day": map[string]any{"usage_percent": 40.0, "reset_display": "OLD-7D", "reset_in_sec": 86400},
			"total": map[string]any{
				"total_percent": 20.0, "kimi_percent": 0.5, "code_percent": 19.5,
				"reset_display": "OLD-T", "reset_in_sec": 172800,
			},
		},
	}
	newDTO := map[string]any{
		"name":    "kimi-refresh",
		"success": true,
		"quota": map[string]any{
			"five_hour": map[string]any{"usage_percent": 55.25, "reset_display": "NEW-5H", "reset_in_sec": 7200},
			"seven_day": map[string]any{"usage_percent": 90.0, "reset_display": "NEW-7D", "reset_in_sec": 259200},
			"total": map[string]any{
				"total_percent": 75.5, "kimi_percent": 3.25, "code_percent": 72.25,
				"reset_display": "NEW-T", "reset_in_sec": 1987200,
			},
		},
	}

	oldOut := runQuotaCardTest(t, map[string]any{"type": "adapter", "adapter": "kimi", "dto": oldDTO})
	newOut := runQuotaCardTest(t, map[string]any{"type": "adapter", "adapter": "kimi", "dto": newDTO})

	// Identity must be preserved across refresh.
	for _, identity := range []string{`data-prov="kimi"`, `data-name="kimi-refresh"`, `Kimi Code`} {
		if !strings.Contains(oldOut, identity) || !strings.Contains(newOut, identity) {
			t.Errorf("card identity %s must be preserved across refresh", identity)
		}
	}

	// New values must replace old values.
	for _, want := range []string{"55.25%", "2h", "90%", "3d", "75.5%", "23d", "Kimi 3.25%", "Code 72.25%"} {
		if !strings.Contains(newOut, want) {
			t.Errorf("refreshed card must contain %q (replaced value); got:\n%s", want, newOut)
		}
	}
	for _, stale := range []string{"10.5%", "40%", "20%", "Kimi 0.5%", "Code 19.5%"} {
		if strings.Contains(newOut, stale) {
			t.Errorf("refreshed card must NOT contain stale value %q; got:\n%s", stale, newOut)
		}
	}
}
