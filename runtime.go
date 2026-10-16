package main

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type Runtime struct {
	mu            sync.RWMutex
	base, current Config
	store         *Store
	exceptions    map[string]bool
	revision      atomic.Uint64
}

func cloneConfig(c Config) Config {
	c.Moderation.Rules = maps.Clone(c.Moderation.Rules)
	c.Moderation.Penalties = slices.Clone(c.Moderation.Penalties)
	c.IRC.Channels = slices.Clone(c.IRC.Channels)
	c.IRC.OwnerAccounts = slices.Clone(c.IRC.OwnerAccounts)
	c.Exceptions.Accounts = slices.Clone(c.Exceptions.Accounts)
	c.Exceptions.Hosts = slices.Clone(c.Exceptions.Hosts)
	return c
}
func newRuntime(c Config, s *Store) (*Runtime, error) {
	r := &Runtime{base: cloneConfig(c), current: cloneConfig(c), store: s}
	overrides, e := s.overrides()
	if e != nil {
		return nil, e
	}
	for k, v := range effectiveOverrides(c.Provider, overrides) {
		if e = applySetting(&r.current, k, v); e != nil {
			return nil, fmt.Errorf("kalıcı ayar geçersiz: %s", k)
		}
	}
	if e = r.current.validate(); e != nil {
		return nil, e
	}
	r.exceptions, e = s.exceptionRows()
	if e != nil {
		return nil, e
	}
	return r, nil
}
func (r *Runtime) snapshot() Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneConfig(r.current)
}
func applySetting(c *Config, key, value string) error {
	m := &c.Moderation
	switch key {
	case "dry_run":
		if value != "true" && value != "false" {
			return fmt.Errorf("true veya false gerekli")
		}
		m.DryRun = value == "true"
	case "score_window_hours", "action_cooldown_seconds", "max_message_age_seconds":
		n, e := strconv.Atoi(value)
		if e != nil {
			return fmt.Errorf("tam sayı gerekli")
		}
		switch key {
		case "score_window_hours":
			m.ScoreWindowHours = n
		case "action_cooldown_seconds":
			m.ActionCooldownSeconds = n
		case "max_message_age_seconds":
			m.MaxMessageAgeSeconds = n
		}
	case "reason":
		m.Reason = value
	default:
		parts := strings.Split(key, ".")
		if len(parts) != 3 {
			return fmt.Errorf("izin verilen bir ayar adı kullan")
		}
		if parts[0] == "rule" {
			rule, ok := m.Rules[parts[1]]
			if !ok {
				return fmt.Errorf("kategori bulunamadı")
			}
			switch parts[2] {
			case "threshold":
				v, e := strconv.ParseFloat(value, 64)
				if e != nil {
					return fmt.Errorf("ondalık sayı gerekli")
				}
				rule.Threshold = v
			case "points":
				v, e := strconv.Atoi(value)
				if e != nil {
					return fmt.Errorf("tam sayı gerekli")
				}
				rule.Points = v
			default:
				return fmt.Errorf("threshold veya points gerekli")
			}
			m.Rules[parts[1]] = rule
		} else if parts[0] == "penalty" {
			n, e := strconv.Atoi(parts[1])
			if e != nil {
				return fmt.Errorf("ceza puanı gerekli")
			}
			found := false
			for i, p := range m.Penalties {
				if p.Points == n {
					found = true
					switch parts[2] {
					case "duration":
						p.Duration = value
					case "command":
						p.Command = value
					default:
						return fmt.Errorf("duration veya command gerekli")
					}
					m.Penalties[i] = p
				}
			}
			if !found {
				return fmt.Errorf("ceza basamağı bulunamadı")
			}
		} else {
			return fmt.Errorf("bu ayar IRC üzerinden değiştirilemez")
		}
	}
	return c.validate()
}
func (r *Runtime) set(actor, key string, value *string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	overrides, e := r.store.overrides()
	if e != nil {
		return e
	}
	if value != nil {
		probe := cloneConfig(r.current)
		if e := applySetting(&probe, key, *value); e != nil {
			return e
		}
	}
	persistKey := key
	if strings.HasPrefix(key, "rule.") {
		persistKey = r.base.Provider + "|" + key
	}
	if value == nil {
		if _, ok := overrides[persistKey]; !ok {
			return fmt.Errorf("bu ayarın kalıcı değişikliği yok")
		}
		delete(overrides, persistKey)
	} else {
		overrides[persistKey] = *value
	}
	candidate := cloneConfig(r.base)
	for k, v := range effectiveOverrides(r.base.Provider, overrides) {
		if e = applySetting(&candidate, k, v); e != nil {
			return e
		}
	}
	if e = r.store.setOverride(actor, persistKey, value); e != nil {
		return e
	}
	r.current = candidate
	r.revision.Add(1)
	return nil
}
func exceptionKey(kind, target string) (string, error) {
	switch kind {
	case "host":
		if validHost(target) {
			return hostKey(target), nil
		}
	case "account":
		if atom(target) && target != "*" {
			return "account:" + accountID(target), nil
		}
	}
	return "", fmt.Errorf("tam host/IP veya geçerli account kullan")
}
func (r *Runtime) exempt(account, host string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	host = hostKey(host)
	a := "account:" + accountID(account)
	if r.exceptions[host] || (account != "" && account != "*" && r.exceptions[a]) {
		return true
	}
	for _, h := range r.current.Exceptions.Hosts {
		if hostKey(h) == host {
			return true
		}
	}
	for _, v := range r.current.Exceptions.Accounts {
		if account != "" && account != "*" && accountID(v) == accountID(account) {
			return true
		}
	}
	return false
}
func (r *Runtime) exception(actor, kind, target string, add bool) error {
	key, e := exceptionKey(kind, target)
	if e != nil {
		return e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !add {
		for _, h := range r.base.Exceptions.Hosts {
			if hostKey(h) == key {
				return fmt.Errorf("bu exception conf dosyasından kaldırılmalı")
			}
		}
		for _, a := range r.base.Exceptions.Accounts {
			if "account:"+accountID(a) == key {
				return fmt.Errorf("bu exception conf dosyasından kaldırılmalı")
			}
		}
	}
	target = strings.TrimPrefix(key, kind+":")
	if e = r.store.changeException(actor, kind, target, add); e != nil {
		return e
	}
	if add {
		r.exceptions[key] = true
	} else {
		delete(r.exceptions, key)
	}
	r.revision.Add(1)
	return nil
}
func (r *Runtime) exceptionList() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m := maps.Clone(r.exceptions)
	for _, h := range r.current.Exceptions.Hosts {
		m[hostKey(h)] = true
	}
	for _, a := range r.current.Exceptions.Accounts {
		m["account:"+accountID(a)] = true
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func effectiveOverrides(provider string, all map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range all {
		if scope, key, ok := strings.Cut(k, "|"); ok {
			if scope == provider {
				out[key] = v
			}
		} else {
			out[k] = v
		}
	}
	return out
}
