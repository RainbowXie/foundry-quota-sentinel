package config

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// KimiAccount is one saved Kimi Code account. Auth holds the versioned,
// provider-owned authentication envelope (Bearer token + cookie + the stable
// browser headers proven necessary for replay); Generation is a non-secret
// login counter bumped on every overwrite so the sidebar can detect a same-
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

// KimiAuthEnvelopeVersion returns the supported envelope schema version, so
// main.go's login command can stamp a freshly captured envelope without
// reaching into the package's private constant.
func KimiAuthEnvelopeVersion() int { return kimiAuthEnvelopeVersion }

// kimiAuthAllowlist is the CLOSED set of envelope fields the replay needs,
// derived from the OBSERVED request headers of the authenticated
// GetSubscriptionStats call (verified by a plain Go-HTTP replay returning
// 200). accessToken is sent as `Authorization: Bearer <accessToken>`; cookie is
// the raw Cookie header; the x-msh-* / x-traffic-id / user-agent /
// r-timezone / x-language values are the stable browser headers. Unknown
// captured state is rejected at capture time so unrelated data never reaches
// the persisted credential.
var kimiAuthAllowlist = []string{
	"accessToken",
	"refreshToken",
	"cookie",
	"x_msh_device_id",
	"x_traffic_id",
	"x_msh_platform",
	"x_msh_version",
	"x_language",
	"r_timezone",
	"user_agent",
}

// KimiAuthEnvelope is the versioned, provider-owned authentication material
// for one Kimi account. Fields is a name→value map keyed by the allowlist
// above (header names with non-alphanumeric chars use underscores). Values are
// validated to reject control characters (CR/LF) that could inject HTTP
// headers. Only allowlisted names are accepted by SetField.
type KimiAuthEnvelope struct {
	Version int               `json:"version"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// SetField records an allowlisted field value after validating it carries no
// control characters. An unknown name or an unsafe value is rejected so
// neither reaches the persisted credential nor the replayed request.
func (e *KimiAuthEnvelope) SetField(name, value string) error {
	if !kimiAllowlisted(name) {
		return fmt.Errorf("Kimi 凭证字段 %q 不在允许列表", name)
	}
	if value == "" {
		return fmt.Errorf("Kimi 凭证字段 %q 值为空", name)
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("Kimi 凭证字段 %q 含非法控制字符", name)
	}
	if e.Fields == nil {
		e.Fields = map[string]string{}
	}
	e.Fields[name] = value
	return nil
}

// Field returns an allowlisted field value by name.
func (e *KimiAuthEnvelope) Field(name string) (string, bool) {
	v, ok := e.Fields[name]
	return v, ok
}

// AccessToken is the persisted Bearer token (sent as `Authorization: Bearer
// <accessToken>`). Empty when not saved.
func (e *KimiAuthEnvelope) AccessToken() string {
	v, _ := e.Fields["accessToken"]
	return v
}

// RefreshToken is the persisted durable refresh token (sent in the
// RefreshToken request body). Empty when not saved.
func (e *KimiAuthEnvelope) RefreshToken() string {
	v, _ := e.Fields["refreshToken"]
	return v
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
// partially replayed. Any field name outside the allowlist or value carrying
// CR/LF is dropped/rejected at load time too, so a hand-edited config cannot
// smuggle an unknown credential in.
func (e *KimiAuthEnvelope) Decode(data []byte) error {
	var raw struct {
		Version int               `json:"version"`
		Fields  map[string]string `json:"fields"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("Kimi 认证信封解析失败: %w", err)
	}
	if raw.Version != kimiAuthEnvelopeVersion {
		return fmt.Errorf("Kimi 认证信封版本 %d 不受支持，请重新登录", raw.Version)
	}
	fields := make(map[string]string, len(raw.Fields))
	for name, value := range raw.Fields {
		if !kimiAllowlisted(name) {
			continue
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("Kimi 认证信封字段 %q 含非法控制字符", name)
		}
		fields[name] = value
	}
	e.Version = raw.Version
	e.Fields = fields
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
