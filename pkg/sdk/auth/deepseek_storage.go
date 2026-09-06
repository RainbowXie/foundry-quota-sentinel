package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"

	"foundry-quota-sentinel/pkg/sdk/auth/browserauth"
)

var deepSeekTokenRe = regexp.MustCompile(`[A-Za-z0-9._\-]{30,800}`)

func deepSeekTokenFromEvent(event browserauth.Event) (token, requestID, requestURL string) {
	decoded, ok := browserauth.DecodeRequestHeadersEvent(event)
	if !ok {
		return "", "", ""
	}
	return browserauth.BearerToken(decoded.Headers), decoded.RequestID, decoded.URL
}

func deepSeekStorageCandidates(snapshot string) []string {
	if snapshot == "" {
		return nil
	}
	var wrapper struct {
		L map[string]json.RawMessage `json:"l"`
		S map[string]json.RawMessage `json:"s"`
	}
	if err := json.Unmarshal([]byte(snapshot), &wrapper); err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	collect := func(values map[string]json.RawMessage) {
		for _, raw := range values {
			walkStringCandidates(string(raw), seen, &out)
		}
	}
	collect(wrapper.L)
	collect(wrapper.S)
	return out
}

func walkStringCandidates(raw string, seen map[string]bool, out *[]string) {
	for _, match := range deepSeekTokenRe.FindAllString(raw, -1) {
		if seen[match] {
			continue
		}
		seen[match] = true
		*out = append(*out, match)
	}
	if len(raw) == 0 || (raw[0] != '{' && raw[0] != '[') {
		return
	}
	var any any
	if err := json.Unmarshal([]byte(raw), &any); err != nil {
		return
	}
	walkJSONCandidates(any, seen, out)
}

func walkJSONCandidates(value any, seen map[string]bool, out *[]string) {
	switch v := value.(type) {
	case string:
		for _, match := range deepSeekTokenRe.FindAllString(v, -1) {
			if seen[match] {
				continue
			}
			seen[match] = true
			*out = append(*out, match)
		}
	case map[string]any:
		for _, child := range v {
			walkJSONCandidates(child, seen, out)
		}
	case []any:
		for _, child := range v {
			walkJSONCandidates(child, seen, out)
		}
	}
}

type deepSeekStorageEntry struct {
	key         string
	expectedLen int
}

const deepSeekAuthKey = "userToken"

func deepSeekAuthStorageEntries(all []deepSeekStorageEntry) []deepSeekStorageEntry {
	out := make([]deepSeekStorageEntry, 0, len(all))
	for _, e := range all {
		if strings.EqualFold(e.key, deepSeekAuthKey) {
			out = append(out, e)
		}
	}
	return out
}

func deepSeekExpectedStorageEntries(webStore string) []deepSeekStorageEntry {
	if webStore == "" {
		return nil
	}
	var envelope struct {
		L map[string]json.RawMessage `json:"l"`
	}
	if err := json.Unmarshal([]byte(webStore), &envelope); err != nil {
		return nil
	}
	entries := make([]deepSeekStorageEntry, 0, len(envelope.L))
	for k, raw := range envelope.L {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			s = string(raw)
		}
		entries = append(entries, deepSeekStorageEntry{key: k, expectedLen: len(s)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	return entries
}

func deepSeekStorageProbeExpr(keys []string) string {
	keysJSON, _ := json.Marshal(keys)
	return fmt.Sprintf(`JSON.stringify(%s.map(function(k){var v=localStorage.getItem(k);return v==null?[-1,-1]:[1,v.length]}))`, string(keysJSON))
}

func deepSeekStorageMismatch(ctx context.Context, cdp deepSeekCDP, expected []deepSeekStorageEntry) []deepSeekStorageEntry {
	if len(expected) == 0 {
		return nil
	}
	keys := make([]string, len(expected))
	for i, e := range expected {
		keys[i] = e.key
	}
	expr := deepSeekStorageProbeExpr(keys)
	raw, err := cdp.Evaluate(ctx, expr)
	if err != nil {
		return expected
	}
	var envelope struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return expected
	}
	var live [][2]int
	if err := json.Unmarshal([]byte(envelope.Result.Value), &live); err != nil || len(live) != len(expected) {
		return expected
	}
	var mismatch []deepSeekStorageEntry
	for i, e := range expected {
		present, liveLen := live[i][0], live[i][1]
		if present < 0 {
			mismatch = append(mismatch, e)
			log.Printf("deepseek: localStorage 键 %q 缺失（document-start 脚本未生效）", e.key)
			continue
		}
		if liveLen != e.expectedLen {
			mismatch = append(mismatch, e)
			log.Printf("deepseek: localStorage 键 %q 长度不匹配：期望 %d 实际 %d（SPA 可能覆盖了恢复值）", e.key, e.expectedLen, liveLen)
			continue
		}
		log.Printf("deepseek: localStorage 键 %q 已恢复（长度 %d）", e.key, liveLen)
	}
	return mismatch
}

func deepSeekRestoreScript(webStore string) (string, error) {
	if webStore == "" {
		webStore = `{"l":{},"s":{}}`
	}
	if !json.Valid([]byte(webStore)) {
		return "", fmt.Errorf("DeepSeek 登录态快照无效")
	}
	encoded, err := json.Marshal(webStore)
	if err != nil {
		return "", fmt.Errorf("DeepSeek 登录态脚本生成失败: %w", err)
	}
	return `(function(){try{var raw=` + string(encoded) + `;var o=JSON.parse(raw);var l=o.l||{};var s=o.s||{};` +
		`for(var k in l){try{localStorage.setItem(k,l[k])}catch(e){}};` +
		`for(var k in s){try{sessionStorage.setItem(k,s[k])}catch(e){}};` +
		`}catch(e){}})();`, nil
}

type deepSeekStoredCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"httpOnly"`
}

func deepSeekRestoreState(webStore string) (string, []browserauth.Cookie, error) {
	if webStore == "" {
		script, err := deepSeekRestoreScript(webStore)
		return script, nil, err
	}
	var envelope struct {
		L map[string]json.RawMessage `json:"l"`
		S map[string]json.RawMessage `json:"s"`
		C []deepSeekStoredCookie     `json:"c"`
	}
	if err := json.Unmarshal([]byte(webStore), &envelope); err != nil {
		return "", nil, fmt.Errorf("DeepSeek 登录态快照无效: %w", err)
	}
	cookies := make([]browserauth.Cookie, 0, len(envelope.C))
	for _, cookie := range envelope.C {
		if cookie.Name == "" || cookie.Value == "" || cookie.Domain == "" {
			continue
		}
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		cookies = append(cookies, browserauth.Cookie{
			Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain,
			Path: path, Secure: cookie.Secure, HTTPOnly: cookie.HTTPOnly,
		})
	}
	script, err := deepSeekRestoreScript(webStore)
	return script, cookies, err
}

func deepSeekSnapshotWithCookies(ctx context.Context, snapshot string, cdp deepSeekCDP) string {
	cookies, err := cdp.BrowserCookies(ctx)
	if err != nil {
		return snapshot
	}
	stored := make([]deepSeekStoredCookie, 0, len(cookies))
	for _, cookie := range cookies {
		if !browserauth.CookieDomainMatches(cookie.Domain, deepSeekHost) || cookie.Value == "" ||
			strings.ContainsAny(cookie.Name+cookie.Value, ";\r\n") {
			continue
		}
		stored = append(stored, deepSeekStoredCookie{
			Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain,
			Path: cookie.Path, Secure: cookie.Secure, HTTPOnly: cookie.HTTPOnly,
		})
	}
	if len(stored) == 0 {
		return snapshot
	}
	var envelope struct {
		L map[string]json.RawMessage `json:"l"`
		S map[string]json.RawMessage `json:"s"`
		C []deepSeekStoredCookie     `json:"c"`
	}
	if err := json.Unmarshal([]byte(snapshot), &envelope); err != nil {
		return snapshot
	}
	envelope.C = stored
	data, err := json.Marshal(envelope)
	if err != nil {
		return snapshot
	}
	return string(data)
}

func isDeepSeekSnapshotValid(snapshot string) bool {
	if snapshot == "" {
		return false
	}
	var envelope struct {
		L map[string]json.RawMessage `json:"l"`
		S map[string]json.RawMessage `json:"s"`
	}
	if err := json.Unmarshal([]byte(snapshot), &envelope); err != nil {
		return false
	}
	return envelope.L != nil && envelope.S != nil
}
