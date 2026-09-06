package ollama

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ollamaTagRE      = regexp.MustCompile(`(?is)<[^>]+>`)
	ollamaTagNameRE  = regexp.MustCompile(`(?is)^<\s*(/?)\s*([a-z][a-z0-9:-]*)`)
	ollamaMeterRE    = regexp.MustCompile(`(?i)aria-label\s*=\s*["'](Session|Weekly)\s+usage\s+([0-9]+(?:\.[0-9]+)?)%\s+used["']`)
	ollamaDataTimeRE = regexp.MustCompile(`(?i)data-time\s*=\s*["']([^"']+)["']`)
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

// ParseOllamaQuota 解析 HTML 页面中的 aria-label 和 data-time 标签，生成 QuotaData。
func ParseOllamaQuota(html string) (*QuotaData, error) {
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
		ResetDisplay: formatDurationCompact(seconds),
	}, nil
}

func formatDurationCompact(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	if seconds < 86400 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	return fmt.Sprintf("%dd", seconds/86400)
}
