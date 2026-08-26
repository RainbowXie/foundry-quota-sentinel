package quota

import (
	"context"
	"fmt"
	"io"
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
	ollamaRequestTimeout = 15 * time.Second
	ollamaTagRE          = regexp.MustCompile(`(?is)<[^>]+>`)
	ollamaTagNameRE      = regexp.MustCompile(`(?is)^<\s*(/?)\s*([a-z][a-z0-9:-]*)`)
	ollamaMeterRE        = regexp.MustCompile(`(?i)aria-label\s*=\s*["'](Session|Weekly)\s+usage\s+([0-9]+(?:\.[0-9]+)?)%\s+used["']`)
	ollamaDataTimeRE     = regexp.MustCompile(`(?i)data-time\s*=\s*["']([^"']+)["']`)
)

var ollamaVoidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

type ollamaTag struct {
	raw     string
	depth   int
	closing bool
}

type OllamaQuerier struct {
	Cookie    string
	UserAgent string
	BaseURL   string
	Client    *http.Client
}

func (q *OllamaQuerier) FetchQuota() (*QuotaData, error) {
	if q.Cookie == "" {
		return nil, fmt.Errorf("Ollama cookie not set")
	}

	baseURL := q.BaseURL
	if baseURL == "" {
		baseURL = ollamaBaseURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), ollamaRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/settings", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	userAgent := strings.TrimSpace(q.UserAgent)
	if userAgent == "" {
		userAgent = ollamaUserAgent
	}
	if strings.ContainsAny(userAgent, "\r\n") {
		return nil, fmt.Errorf("invalid Ollama user agent")
	}
	req.Header.Set("Cookie", q.Cookie)
	req.Header.Set("User-Agent", userAgent)

	client := q.Client
	if client == nil {
		client = &http.Client{Timeout: ollamaRequestTimeout}
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
	tags := parseOllamaTags(html)
	for index, tag := range tags {
		match := ollamaMeterRE.FindStringSubmatch(tag.raw)
		if match == nil {
			continue
		}
		name := strings.ToLower(match[1])
		if _, exists := meters[name]; exists {
			return nil, fmt.Errorf("duplicate %s usage meter", match[1])
		}
		reset, err := nextOllamaReset(tags, index)
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

func parseOllamaTags(html string) []ollamaTag {
	depth := 0
	var tags []ollamaTag
	for _, index := range ollamaTagRE.FindAllStringIndex(html, -1) {
		raw := html[index[0]:index[1]]
		match := ollamaTagNameRE.FindStringSubmatch(raw)
		if match == nil {
			continue
		}
		if match[1] == "/" {
			if depth > 0 {
				depth--
			}
			tags = append(tags, ollamaTag{raw: raw, depth: depth, closing: true})
			continue
		}

		tags = append(tags, ollamaTag{raw: raw, depth: depth})
		if !ollamaVoidTags[strings.ToLower(match[2])] && !strings.HasSuffix(strings.TrimSpace(raw), "/>") {
			depth++
		}
	}
	return tags
}

func nextOllamaReset(tags []ollamaTag, meterIndex int) (string, error) {
	meter := tags[meterIndex]
	if containerDepth := ollamaUsageMeterContainerDepth(tags, meterIndex, meter.depth); containerDepth >= 0 {
		for _, tag := range tags[meterIndex+1:] {
			if tag.closing && tag.depth < containerDepth {
				break
			}
			if tag.depth != containerDepth {
				continue
			}
			if !tag.closing && strings.Contains(tag.raw, "data-usage-meter") {
				break
			}
			if match := ollamaDataTimeRE.FindStringSubmatch(tag.raw); match != nil {
				return match[1], nil
			}
		}
		return "", fmt.Errorf("missing reset timestamp")
	}

	for _, tag := range tags[meterIndex+1:] {
		if tag.closing && tag.depth < meter.depth {
			break
		}
		if tag.depth != meter.depth {
			continue
		}
		if ollamaMeterRE.MatchString(tag.raw) {
			break
		}
		if match := ollamaDataTimeRE.FindStringSubmatch(tag.raw); match != nil {
			return match[1], nil
		}
	}
	return "", fmt.Errorf("missing reset timestamp")
}

func ollamaUsageMeterContainerDepth(tags []ollamaTag, meterIndex, meterDepth int) int {
	for index := meterIndex - 1; index >= 0; index-- {
		tag := tags[index]
		if tag.closing || tag.depth >= meterDepth {
			continue
		}
		if strings.Contains(tag.raw, "data-usage-meter") {
			return tag.depth
		}
	}
	return -1
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
		UsagePercent: percent,
		ResetInSec:   seconds,
		ResetDisplay: formatter.FormatDurationCompact(seconds),
	}, nil
}
