package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Profile struct {
	Cookie         string `json:"cookie,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	DeepSeekAPIKey string `json:"deepseek_api_key,omitempty"`
}

type DeepSeekAccount struct {
	Name  string `json:"name"`
	Token string `json:"token"` // platform.deepseek.com 网页 Bearer token
}

type Config struct {
	ActiveProfile    string             `json:"active_profile"`
	Profiles         map[string]Profile `json:"profiles"`
	DeepSeekAccounts []DeepSeekAccount  `json:"deepseek_accounts,omitempty"`
}

func configDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil { return "", fmt.Errorf("cannot find home dir: %w", err) }
	return filepath.Join(h, ".ocgt-monitor"), nil
}

func configPath() (string, error) {
	d, err := configDir()
	if err != nil { return "", err }
	return filepath.Join(d, "config.json"), nil
}

func Load() *Config {
	path, err := configPath()
	if err != nil { return &Config{ActiveProfile: "default", Profiles: map[string]Profile{}} }
	data, err := os.ReadFile(path)
	if err != nil { return &Config{ActiveProfile: "default", Profiles: map[string]Profile{}} }
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil { return &Config{ActiveProfile: "default", Profiles: map[string]Profile{}} }
	if cfg.Profiles == nil { cfg.Profiles = map[string]Profile{} }
	if cfg.ActiveProfile == "" { cfg.ActiveProfile = "default" }
	return &cfg
}

func (c *Config) Save() error {
	path, err := configPath()
	if err != nil { return err }
	dir, _ := configDir()
	if err := os.MkdirAll(dir, 0700); err != nil { return err }
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil { return err }
	return os.WriteFile(path, data, 0600)
}

func (c *Config) GetActiveProfile() (Profile, bool) {
	p, ok := c.Profiles[c.ActiveProfile]
	return p, ok
}

func (c *Config) AddProfile(name string, p Profile) {
	c.Profiles[name] = p
}

func (c *Config) DeleteProfile(name string) error {
	if _, ok := c.Profiles[name]; !ok { return fmt.Errorf("Profile %q 不存在", name) }
	delete(c.Profiles, name)
	if len(c.Profiles) == 0 { c.ActiveProfile = "default"; return nil }
	if c.ActiveProfile == name {
		for k := range c.Profiles { c.ActiveProfile = k; break }
	}
	return nil
}

func (c *Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for k := range c.Profiles { names = append(names, k) }
	return names
}

// UpsertDeepSeekAccount 按 Name 覆盖或追加一个 DeepSeek 账户。
func (c *Config) UpsertDeepSeekAccount(a DeepSeekAccount) {
	for i := range c.DeepSeekAccounts {
		if c.DeepSeekAccounts[i].Name == a.Name {
			c.DeepSeekAccounts[i] = a
			return
		}
	}
	c.DeepSeekAccounts = append(c.DeepSeekAccounts, a)
}

// DeleteDeepSeekAccount 按 Name 删除，不存在返回错误。
func (c *Config) DeleteDeepSeekAccount(name string) error {
	for i := range c.DeepSeekAccounts {
		if c.DeepSeekAccounts[i].Name == name {
			c.DeepSeekAccounts = append(c.DeepSeekAccounts[:i], c.DeepSeekAccounts[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("DeepSeek 账户 %q 不存在", name)
}

func HasEnvVars() (cookie bool, ws bool, dk bool) {
	if os.Getenv("OPENCODE_GO_AUTH_COOKIE") != "" { cookie = true }
	if os.Getenv("OPENCODE_GO_WORKSPACE_ID") != "" { ws = true }
	if os.Getenv("DEEPSEEK_API_KEY") != "" { dk = true }
	return
}
