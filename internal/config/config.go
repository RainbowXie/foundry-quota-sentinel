package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Profile struct {
	Cookie         string `json:"cookie,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	DeepSeekAPIKey string `json:"deepseek_api_key,omitempty"`
}

type DeepSeekAccount struct {
	Name     string `json:"name"`
	Token    string `json:"token"`               // platform.deepseek.com 网页 Bearer token（供卡片调接口）
	WebStore string `json:"web_store,omitempty"` // 登录时 local/sessionStorage 快照 JSON {"l":{},"s":{}}，打开账户页时原样回放以恢复登录态
	// Generation is a non-sensitive, per-account success-login counter.
	// UpsertDeepSeekAccount bumps it on every overwrite (a real re-login,
	// even when the returned token is identical to the old one — DeepSeek
	// can return the same long-lived token while the Cookie/WebStore is
	// refreshed). The sidebar compares generations to detect completion
	// without ever reading the token. A token-derived signal cannot
	// detect a same-token re-login. Window-size and other-provider saves
	// do not touch this account's generation, so they cannot falsely
	// complete a re-login poll.
	Generation int `json:"generation,omitempty"`
}

type OllamaAccount struct {
	Name      string `json:"name"`
	Cookie    string `json:"cookie"`
	UserAgent string `json:"user_agent,omitempty"`
}

type Config struct {
	ActiveProfile    string             `json:"active_profile"`
	Profiles         map[string]Profile `json:"profiles"`
	DeepSeekAccounts []DeepSeekAccount  `json:"deepseek_accounts,omitempty"`
	OllamaAccounts   []OllamaAccount    `json:"ollama_accounts,omitempty"`
	KimiAccounts     []KimiAccount      `json:"kimi_accounts,omitempty"`
	WindowW          int                `json:"window_w,omitempty"`
	WindowH          int                `json:"window_h,omitempty"`
}

// SaveWindowSize 持久化窗口大小（重载 config 再写，避免覆盖其它字段）。
func SaveWindowSize(w, h int) {
	c := Load()
	c.WindowW, c.WindowH = w, h
	_ = c.Save()
}

// configPathOverride lets tests redirect the config file to a temp location
// without touching the real user config. Empty in production. Guarded by
// configPathMu because Load/Save may run concurrently (e.g. window-size save
// alongside a credential rotation) and all read this override.
var (
	configPathMu       sync.RWMutex
	configPathOverride string
)

func configDir() (string, error) {
	configPathMu.RLock()
	override := configPathOverride
	configPathMu.RUnlock()
	if override != "" {
		return filepath.Dir(override), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find home dir: %w", err)
	}
	return filepath.Join(h, ".foundry-quota-sentinel"), nil
}

func configPath() (string, error) {
	configPathMu.RLock()
	override := configPathOverride
	configPathMu.RUnlock()
	if override != "" {
		return override, nil
	}
	d, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.json"), nil
}

// migrateLegacyConfig 把旧目录 ~/.ocgt-monitor 迁移到新目录 ~/.foundry-quota-sentinel
// （仅当新目录尚不存在、旧目录存在时整体改名），平滑升级老用户配置。
func migrateLegacyConfig() {
	h, err := os.UserHomeDir()
	if err != nil {
		return
	}
	newDir := filepath.Join(h, ".foundry-quota-sentinel")
	oldDir := filepath.Join(h, ".ocgt-monitor")
	if _, err := os.Stat(newDir); err == nil {
		return
	} // 新目录已存在，不迁移
	if _, err := os.Stat(oldDir); err != nil {
		return
	} // 旧目录不存在，无需迁移
	_ = os.Rename(oldDir, newDir)
}

func Load() *Config {
	migrateLegacyConfig()
	path, err := configPath()
	if err != nil {
		return &Config{ActiveProfile: "default", Profiles: map[string]Profile{}}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return &Config{ActiveProfile: "default", Profiles: map[string]Profile{}}
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &Config{ActiveProfile: "default", Profiles: map[string]Profile{}}
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	if cfg.ActiveProfile == "" {
		cfg.ActiveProfile = "default"
	}
	return &cfg
}

func (c *Config) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	dir, _ := configDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// Atomic write: serialize to a temp file in the same directory, then
	// rename. A plain os.WriteFile truncates the target first, so a crash or a
	// concurrent reader mid-write can observe a torn/partial/empty config.
	// temp+rename guarantees a reader sees either the old or the new complete
	// file, never a half-written one — critical for rotated-credential
	// persistence (a torn save must not lose the prior envelope).
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Clean up the temp file on any failure path below.
	defer func() {
		if _, statErr := os.Stat(tmpName); statErr == nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (c *Config) GetActiveProfile() (Profile, bool) {
	p, ok := c.Profiles[c.ActiveProfile]
	return p, ok
}

func (c *Config) AddProfile(name string, p Profile) {
	c.Profiles[name] = p
}

func (c *Config) DeleteProfile(name string) error {
	if _, ok := c.Profiles[name]; !ok {
		return fmt.Errorf("Profile %q 不存在", name)
	}
	delete(c.Profiles, name)
	if len(c.Profiles) == 0 {
		c.ActiveProfile = "default"
		return nil
	}
	if c.ActiveProfile == name {
		for k := range c.Profiles {
			c.ActiveProfile = k
			break
		}
	}
	return nil
}

func (c *Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for k := range c.Profiles {
		names = append(names, k)
	}
	return names
}

// UpsertDeepSeekAccount 按 Name 覆盖或追加一个 DeepSeek 账户。覆盖已
// 存在账号时把 Generation 递增（一次真实重登录，即使返回的 token 与旧
// 值完全相同——DeepSeek 可能返回同一长期 token 但 Cookie/WebStore 已刷新）；
// 新账号 Generation 从 1 起。这样侧边栏按 generation 比较即可检测完成，
// 无需读取 token。
func (c *Config) UpsertDeepSeekAccount(a DeepSeekAccount) {
	for i := range c.DeepSeekAccounts {
		if c.DeepSeekAccounts[i].Name == a.Name {
			gen := c.DeepSeekAccounts[i].Generation + 1
			a.Generation = gen
			c.DeepSeekAccounts[i] = a
			return
		}
	}
	if a.Generation == 0 {
		a.Generation = 1
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

// UpsertOllamaAccount 按 Name 覆盖或追加一个 Ollama 账户。
func (c *Config) UpsertOllamaAccount(a OllamaAccount) {
	for i := range c.OllamaAccounts {
		if c.OllamaAccounts[i].Name == a.Name {
			c.OllamaAccounts[i] = a
			return
		}
	}
	c.OllamaAccounts = append(c.OllamaAccounts, a)
}

// DeleteOllamaAccount 按 Name 删除，不存在返回错误。
func (c *Config) DeleteOllamaAccount(name string) error {
	for i := range c.OllamaAccounts {
		if c.OllamaAccounts[i].Name == name {
			c.OllamaAccounts = append(c.OllamaAccounts[:i], c.OllamaAccounts[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("Ollama 账户 %q 不存在", name)
}

func HasEnvVars() (cookie bool, ws bool, dk bool) {
	if os.Getenv("OPENCODE_GO_AUTH_COOKIE") != "" {
		cookie = true
	}
	if os.Getenv("OPENCODE_GO_WORKSPACE_ID") != "" {
		ws = true
	}
	if os.Getenv("DEEPSEEK_API_KEY") != "" {
		dk = true
	}
	return
}
