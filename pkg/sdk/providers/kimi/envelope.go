package kimi

import (
	"encoding/json"
	"fmt"
	"strings"
)

const kimiAuthEnvelopeVersion = 1

// KimiAuthEnvelopeVersion 返回支持的认证信封协议版本号。
func KimiAuthEnvelopeVersion() int { return kimiAuthEnvelopeVersion }

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

// KimiAuthEnvelope 封装 Kimi 账户的持久化鉴权材料与回放所必需的稳定请求头。
// 必须严格校验白名单与控制字符（CR/LF），防止非法注入或无关敏感字段落盘。
type KimiAuthEnvelope struct {
	Version int               `json:"version"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// SetField 记录允许列表中的字段，严禁包含 CR/LF 控制字符。
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

// Field 获取允许列表中的指定字段值。
func (e *KimiAuthEnvelope) Field(name string) (string, bool) {
	v, ok := e.Fields[name]
	return v, ok
}

// AccessToken 获取持久化的 Bearer access token。
func (e *KimiAuthEnvelope) AccessToken() string {
	v, _ := e.Fields["accessToken"]
	return v
}

// RefreshToken 获取持久化的 refresh token。
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

// Encode 将信封序列化为 JSON。
func (e KimiAuthEnvelope) Encode() ([]byte, error) {
	if e.Version == 0 {
		e.Version = kimiAuthEnvelopeVersion
	}
	return json.Marshal(e)
}

// Decode 从 JSON 反序列化并校验版本号与字段合法性。
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
