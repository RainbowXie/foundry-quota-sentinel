package quota

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"foundry-quota-sentinel/internal/formatter"
)

const (
	ollamaBaseURL         = "https://ollama.com"
	ollamaUserAgent       = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
	maxOllamaResponseSize = 1 << 20
)

var (
	ollamaTagRE      = regexp.MustCompile(`(?is)<[^>]+>`)
	ollamaMeterRE    = regexp.MustCompile(`(?i)aria-label\s*=\s*["'](Session|Weekly)\s+usage\s+([0-9]+(?:\.[0-9]+)?)%\s+used["']`)
	ollamaDataTimeRE = regexp.MustCompile(`(?i)data-time\s*=\s*["']([^"']+)["']`)
)

type OllamaQuerier struct {
	Cookie  string
	BaseURL string
	Client  *http.Client
}

func (q *OllamaQuerier) FetchQuota() (*QuotaData, error) {
	if q.Cookie == "" {
		return nil, fmt.Errorf("Ollama cookie not set")
	}

	baseURL := q.BaseURL
	if baseURL == "" {
		baseURL = ollamaBaseURL
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/settings", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Cookie", q.Cookie)
	req.Header.Set("User-Agent", ollamaUserAgent)

	client := q.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("Ollama returned %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOllamaResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if len(body) > maxOllamaResponseSize {
		return nil, fmt.Errorf("Ollama response exceeds %d bytes", maxOllamaResponseSize)
	}
	return parseOllamaQuota(string(body))
}

func parseOllamaQuota(html string) (*QuotaData, error) {
	meters := make(map[string]QuotaUsage, 2)
	for _, tagIndex := range ollamaTagRE.FindAllStringIndex(html, -1) {
		tag := html[tagIndex[0]:tagIndex[1]]
		match := ollamaMeterRE.FindStringSubmatch(tag)
		if match == nil {
			continue
		}
		name := strings.ToLower(match[1])
		if _, exists := meters[name]; exists {
			return nil, fmt.Errorf("duplicate %s usage meter", match[1])
		}
		reset, err := nextOllamaReset(html, tagIndex[1])
		if err != nil {
			return nil, fmt.Errorf("%s usage: %w", match[1], err)
		}
		usage, err := newOllamaUsage(match[2], reset)
		if err != nil {
			return nil, fmt.Errorf("%s usage: %w", match[1], err)
		}
		meters[name] = usage
	}

	rolling, ok := meters["session"]
	if !ok {
		return nil, fmt.Errorf("missing Session usage meter")
	}
	weekly, ok := meters["weekly"]
	if !ok {
		return nil, fmt.Errorf("missing Weekly usage meter")
	}
	return &QuotaData{Rolling: rolling, Weekly: weekly, FetchedAt: time.Now()}, nil
}

func nextOllamaReset(html string, after int) (string, error) {
	for _, tagIndex := range ollamaTagRE.FindAllStringIndex(html[after:], -1) {
		tag := html[after+tagIndex[0] : after+tagIndex[1]]
		if ollamaMeterRE.MatchString(tag) {
			break
		}
		if match := ollamaDataTimeRE.FindStringSubmatch(tag); match != nil {
			return match[1], nil
		}
	}
	return "", fmt.Errorf("missing reset timestamp")
}

func newOllamaUsage(percentText, resetText string) (QuotaUsage, error) {
	percent, err := strconv.ParseFloat(percentText, 64)
	if err != nil || percent < 0 || percent > 100 {
		return QuotaUsage{}, fmt.Errorf("invalid usage percent %q", percentText)
	}
	reset, err := time.Parse(time.RFC3339, resetText)
	if err != nil || !reset.After(time.Now()) {
		return QuotaUsage{}, fmt.Errorf("invalid or past reset timestamp %q", resetText)
	}
	seconds := int(time.Until(reset).Seconds())
	return QuotaUsage{
		Status:       "active",
		UsagePercent: int(math.Round(percent)),
		ResetInSec:   seconds,
		ResetDisplay: formatter.FormatDurationCompact(seconds),
	}, nil
}
