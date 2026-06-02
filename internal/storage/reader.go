package storage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type OCGTLogEntry struct {
	ID                  string    `json:"id"`
	Time                time.Time `json:"time"`
	Method              string    `json:"method"`
	Path                string    `json:"path"`
	Status              int       `json:"status"`
	Duration            string    `json:"duration"`
	Model               string    `json:"model"`
	Route               string    `json:"route"`
	InputTokens         int       `json:"input_tokens"`
	OutputTokens        int       `json:"output_tokens"`
	CacheCreationTokens int       `json:"cache_creation_tokens"`
	CacheReadTokens     int       `json:"cache_read_tokens"`
	TotalTokens         int       `json:"total_tokens"`
	Client              string    `json:"client"`
	Error               string    `json:"error"`
}

type TokenStatsDaily struct {
	Date         string `json:"date"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	TotalTokens  int    `json:"total_tokens"`
	RequestCount int    `json:"request_count"`
}

type TokenStatsByModel struct {
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	TotalTokens  int    `json:"total_tokens"`
	RequestCount int    `json:"request_count"`
}

func ReadOCGTLogs(logDir string) ([]OCGTLogEntry, error) {
	entries, err := os.ReadDir(logDir)
	if err != nil { return nil, err }
	var logs []OCGTLogEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ocgt-") || !strings.HasSuffix(e.Name(), ".jsonl") { continue }
		f, err := os.Open(filepath.Join(logDir, e.Name()))
		if err != nil { continue }
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var l OCGTLogEntry
			if json.Unmarshal(sc.Bytes(), &l) == nil { logs = append(logs, l) }
		}
		f.Close()
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].Time.After(logs[j].Time) })
	return logs, nil
}

func CalculateDailyStats(logs []OCGTLogEntry, days int) map[string]TokenStatsDaily {
	cutoff := time.Now().AddDate(0, 0, -days)
	r := make(map[string]TokenStatsDaily)
	for _, l := range logs {
		if l.Time.Before(cutoff) || l.Error != "" { continue }
		k := l.Time.Format("2006-01-02")
		s := r[k]; s.InputTokens += l.InputTokens; s.OutputTokens += l.OutputTokens
		s.TotalTokens += l.TotalTokens; s.RequestCount++; r[k] = s
	}
	return r
}

func CalculateModelStats(logs []OCGTLogEntry, days int) map[string]TokenStatsByModel {
	cutoff := time.Now().AddDate(0, 0, -days)
	r := make(map[string]TokenStatsByModel)
	for _, l := range logs {
		if l.Time.Before(cutoff) || l.Error != "" || l.Model == "" { continue }
		s := r[l.Model]; s.Model = l.Model; s.InputTokens += l.InputTokens; s.OutputTokens += l.OutputTokens
		s.TotalTokens += l.TotalTokens; s.RequestCount++; r[l.Model] = s
	}
	return r
}
