package quota

import (
	"fmt"
	"sync"
	"time"
)

type ProviderType int
const (TypeQuota ProviderType = iota; TypeBalance)

type ProviderInfo struct {
	Name      string
	Type      ProviderType
	QuotaFn   func() (*QuotaData, error)
	BalanceFn func() (*BalanceData, error)
}

type QuotaService struct {
	mu           sync.RWMutex
	providers    map[string]*ProviderInfo
	activeName   string
	lastRefresh  time.Time
	refreshCount int
}

func NewQuotaService() *QuotaService {
	s := &QuotaService{providers: make(map[string]*ProviderInfo)}
	oc := NewOpenCodeGoQuerier()
	s.Register(&ProviderInfo{Name: "opencode-go", Type: TypeQuota, QuotaFn: func() (*QuotaData, error) { return oc.FetchQuota() }})
	ds := NewDeepSeekQuerier()
	s.Register(&ProviderInfo{Name: "deepseek", Type: TypeBalance, BalanceFn: func() (*BalanceData, error) { return ds.FetchBalance() }})
	return s
}

func (s *QuotaService) Register(p *ProviderInfo) { s.mu.Lock(); defer s.mu.Unlock(); s.providers[p.Name] = p }
func (s *QuotaService) SetActive(name string) bool { s.mu.Lock(); defer s.mu.Unlock(); if _, ok := s.providers[name]; !ok { return false }; s.activeName = name; return true }

func (s *QuotaService) FetchQuota() (*QuotaData, error) {
	p := s.activeProvider()
	if p == nil { return nil, fmt.Errorf("no active provider") }
	if p.QuotaFn == nil { return nil, fmt.Errorf("provider %q no quota", p.Name) }
	s.refreshCount++; s.lastRefresh = time.Now()
	return p.QuotaFn()
}

func (s *QuotaService) FetchBalance() (*BalanceData, error) {
	p := s.activeProvider()
	if p == nil { return nil, fmt.Errorf("no active provider") }
	if p.BalanceFn == nil { return nil, fmt.Errorf("provider %q no balance", p.Name) }
	return p.BalanceFn()
}

func (s *QuotaService) ListedProviders() []string {
	s.mu.RLock(); defer s.mu.RUnlock()
	n := make([]string, 0, len(s.providers))
	for k := range s.providers { n = append(n, k) }
	return n
}

func (s *QuotaService) activeProvider() *ProviderInfo { s.mu.RLock(); defer s.mu.RUnlock(); return s.providers[s.activeName] }
