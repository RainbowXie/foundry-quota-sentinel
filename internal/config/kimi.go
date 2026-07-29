package config

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// KimiAccount is one saved Kimi Code account. Auth holds the versioned,
// provider-owned authentication envelope; Generation is a non-secret login
// counter bumped on every overwrite so the sidebar can detect a same-
// envelope re-login without reading the credential.
type KimiAccount struct {
	Name       string           `json:"name"`
	Auth       KimiAuthEnvelope `json:"auth"`
	Generation int              `json:"generation,omitempty"`
}

// kimiAuthEnvelopeVersion is the supported envelope schema version. An
// unknown version fails with a re-login error instead of being partially
// replayed. Bump only on a deliberate, backward-incompatible envelope change.
const kimiAuthEnvelopeVersion = 1

// kimiAuthAllowlist is the closed set of cookie names the envelope may carry.
// It starts empty and grows ONLY with names the evidence phase proves
// necessary for Kimi replay. An unknown name is rejected at capture so
// unrelated captured state never reaches the persisted credential. The
// single synthetic placeholder below is EVIDENCE-GATED: replace with the
// real minimum replay set once the CDP capture confirms it.
var kimiAuthAllowlist = []string{"kimi_session"}

// KimiAuthEnvelope is the versioned, provider-owned authentication material
// for one Kimi account. It carries only allowlisted cookie values proven
// necessary for replay. Cookies is a name→value map; values are validated to
// reject control characters (CR/LF) that could inject HTTP headers.
type KimiAuthEnvelope struct {
	Version int               `json:"version"`
	Cookies map[string]string `json:"cookies,omitempty"`
}

// SetCookie records an allowlisted cookie value after validating it carries
// no control characters. An unknown name or an unsafe value is rejected so
// neither reaches the persisted credential nor the replayed cookie set.
func (e *KimiAuthEnvelope) SetCookie(name, value string) error {
	if !kimiAllowlisted(name) {
		return fmt.Errorf("Kimi 凭证字段 %q 不在允许列表", name)
	}
	if name == "" || value == "" {
		return fmt.Errorf("Kimi 凭证字段 %q 值为空", name)
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("Kimi 凭证字段 %q 含非法控制字符", name)
	}
	if e.Cookies == nil {
		e.Cookies = map[string]string{}
	}
	e.Cookies[name] = value
	return nil
}

// Cookie returns an allowlisted cookie value by name.
func (e *KimiAuthEnvelope) Cookie(name string) (string, bool) {
	v, ok := e.Cookies[name]
	return v, ok
}

func kimiAllowlisted(name string) bool {
	for _, allowed := range kimiAuthAllowlist {
		if allowed == name {
			return true
		}
	}
	return false
}

// Encode serialises the envelope to JSON. The version is always written so a
// decoder can reject an unsupported version before replaying anything.
func (e KimiAuthEnvelope) Encode() ([]byte, error) {
	if e.Version == 0 {
		e.Version = kimiAuthEnvelopeVersion
	}
	return json.Marshal(e)
}

// Decode parses an envelope and rejects an unsupported version. An
// unsupported version fails closed (re-login required) rather than being
// partially replayed.
func (e *KimiAuthEnvelope) Decode(data []byte) error {
	var raw struct {
		Version int               `json:"version"`
		Cookies map[string]string `json:"cookies"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("Kimi 认证信封解析失败: %w", err)
	}
	if raw.Version != kimiAuthEnvelopeVersion {
		return fmt.Errorf("Kimi 认证信封版本 %d 不受支持，请重新登录", raw.Version)
	}
	// Drop any cookie name outside the allowlist at load time too, so a
	// hand-edited config cannot smuggle an unknown credential in.
	cookies := make(map[string]string, len(raw.Cookies))
	for name, value := range raw.Cookies {
		if !kimiAllowlisted(name) {
			continue
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("Kimi 认证信封字段 %q 含非法控制字符", name)
		}
		cookies[name] = value
	}
	e.Version = raw.Version
	e.Cookies = cookies
	return nil
}

// UpsertKimiAccount replaces or appends a Kimi account by Name. Overwriting
// an existing name bumps Generation (a real re-login, even with an identical
// envelope); a new account starts at Generation 1. Other Kimi accounts and
// every other provider section are untouched.
func (c *Config) UpsertKimiAccount(a KimiAccount) {
	for i := range c.KimiAccounts {
		if c.KimiAccounts[i].Name == a.Name {
			gen := c.KimiAccounts[i].Generation + 1
			a.Generation = gen
			c.KimiAccounts[i] = a
			return
		}
	}
	if a.Generation == 0 {
		a.Generation = 1
	}
	c.KimiAccounts = append(c.KimiAccounts, a)
}

// DeleteKimiAccount removes a Kimi account by Name. An unknown name is an
// error so the caller can surface "account not found".
func (c *Config) DeleteKimiAccount(name string) error {
	for i := range c.KimiAccounts {
		if c.KimiAccounts[i].Name == name {
			c.KimiAccounts = append(c.KimiAccounts[:i], c.KimiAccounts[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("Kimi 账户 %q 不存在", name)
}

// KimiAccountNames returns the saved Kimi account names in sorted order for
// deterministic CLI/sidebar listing.
func (c *Config) KimiAccountNames() []string {
	names := make([]string, 0, len(c.KimiAccounts))
	for _, a := range c.KimiAccounts {
		names = append(names, a.Name)
	}
	sort.Strings(names)
	return names
}
