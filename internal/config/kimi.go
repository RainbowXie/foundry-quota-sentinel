package config

import (
	"fmt"
	"sort"
	"sync"

	"foundry-quota-sentinel/pkg/sdk/providers/kimi"
)

// KimiAccount is one saved Kimi Code account.
type KimiAccount struct {
	Name       string           `json:"name"`
	Auth       KimiAuthEnvelope `json:"auth"`
	Generation int              `json:"generation,omitempty"`
}

// KimiAuthEnvelope is an alias to the SDK's native KimiAuthEnvelope.
type KimiAuthEnvelope = kimi.KimiAuthEnvelope

const kimiAuthEnvelopeVersion = 1

// KimiAuthEnvelopeVersion returns the supported envelope schema version.
func KimiAuthEnvelopeVersion() int { return kimi.KimiAuthEnvelopeVersion() }

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

// configWriteMu is the single shared transaction lock for ALL config writes
// (token rotation, login upsert, delete, window-size save, config add/use).
// Every writer does Load→modify→Save on the whole config; without a shared
// lock, a concurrent login/window-save would overwrite a freshly-rotated token
// (and vice versa) with a stale snapshot. Mutate() takes this lock, reloads
// the latest on-disk config, applies the mutation, and saves atomically, so
// concurrent writers serialize and each sees the other's prior write.
var configWriteMu sync.Mutex

// Mutate is the shared config-write transaction: it holds BOTH the in-process
// configWriteMu and the cross-process file lock (WithConfigLock), re-loads the
// LATEST on-disk config, applies fn (which may modify c in place), and saves
// atomically. fn returning an error aborts the save (no partial write). All
// config-mutating paths go through Mutate so they share one transaction lock
// (in-process + cross-process) and never overwrite each other with stale
// snapshots — including across separate processes (CLI vs web server vs
// open-page) that share the same config file.
func Mutate(fn func(c *Config) error) error {
	return WithConfigLock(fn)
}

// SaveKimiTokens atomically persists rotated access + refresh tokens for one
// named Kimi account. It is the shared production persistence path used by both
// the CLI (kimiPersistRotated) and the web server (SetKimiRefreshSave): through
// Mutate it re-loads the LATEST on-disk config under the shared write lock,
// updates ONLY the target account's accessToken + refreshToken (every other
// account, provider section, and field is untouched), then saves atomically.
// A missing account is an error (the caller surfaces re-login); a SetField
// rejection (CR/LF) is an error. The tokens never appear in the returned error.
func SaveKimiTokens(name, accessToken, refreshToken string) error {
	return Mutate(func(c *Config) error {
		for i := range c.KimiAccounts {
			if c.KimiAccounts[i].Name != name {
				continue
			}
			env := c.KimiAccounts[i].Auth
			if err := env.SetField("accessToken", accessToken); err != nil {
				return fmt.Errorf("Kimi 账户 %q 保存失败", name)
			}
			if err := env.SetField("refreshToken", refreshToken); err != nil {
				return fmt.Errorf("Kimi 账户 %q 保存失败", name)
			}
			c.KimiAccounts[i].Auth = env
			return nil
		}
		return fmt.Errorf("Kimi 账户 %q 不存在", name)
	})
}
