package formatter

import (
	"fmt"
	"strings"
)

func FormatDurationCompact(seconds int) string {
	if seconds < 60 { return fmt.Sprintf("%ds", seconds) }
	if seconds < 3600 { return fmt.Sprintf("%dm", seconds/60) }
	if seconds < 86400 { return fmt.Sprintf("%dh", seconds/3600) }
	return fmt.Sprintf("%dd", seconds/86400)
}

func FormatNumber(n int) string {
	if n >= 1000000 { return fmt.Sprintf("%.1fM", float64(n)/1000000) }
	if n >= 1000 { return fmt.Sprintf("%.1fK", float64(n)/1000) }
	return fmt.Sprintf("%d", n)
}

func FormatCost(cost float64) string {
	if cost == 0 { return "$0" }
	return fmt.Sprintf("$%.6f", cost)
}

func FormatPercent(value int) string {
	return fmt.Sprintf("%d%%", value)
}

func ProgressBar(percent int, width int) string {
	filled := (percent * width) / 100
	var sb strings.Builder
	sb.Grow(width + 6)
	sb.WriteByte('[')
	for i := 0; i < width; i++ {
		if i < filled { sb.WriteString("█") } else { sb.WriteString("░") }
	}
	sb.WriteString(fmt.Sprintf("] %d%%", percent))
	return sb.String()
}
