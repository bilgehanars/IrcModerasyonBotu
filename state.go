package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Incident struct {
	At     time.Time `json:"at"`
	Points int       `json:"points"`
}
type Record struct {
	Incidents  []Incident `json:"incidents"`
	LastAction time.Time  `json:"last_action"`
	LastTier   int        `json:"last_tier"`
}
type Store struct {
	mu      sync.Mutex
	path    string
	Records map[string]Record
}

func openStore(path string) (*Store, error) {
	s := &Store{path: path, Records: map[string]Record{}}
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return s, nil
	}
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal(b, &s.Records); e != nil {
		return nil, fmt.Errorf("puan dosyası bozuk: %w", e)
	}
	if s.Records == nil {
		return nil, fmt.Errorf("puan dosyası null olamaz")
	}
	return s, nil
}

// Caller holds mu. Atomic replacement prevents partial state after a crash.
func (s *Store) save() error {
	if e := os.MkdirAll(filepath.Dir(s.path), 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(s.Records, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(s.path), ".scores-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	return os.Rename(tmp, s.path)
}
func prune(r Record, now time.Time, window time.Duration) (Record, int) {
	keep := make([]Incident, 0, len(r.Incidents))
	total := 0
	for _, v := range r.Incidents {
		if v.At.After(now.Add(-window)) {
			keep = append(keep, v)
			total += v.Points
		}
	}
	r.Incidents = keep
	return r, total
}
func (s *Store) add(key string, points int, now time.Time, window time.Duration) (Record, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.Records[key]
	r, total := prune(old, now, window)
	r.Incidents = append(r.Incidents, Incident{now, points})
	s.Records[key] = r
	if err := s.save(); err != nil {
		s.Records[key] = old
		return Record{}, 0, err
	}
	return r, total + points, nil
}
func (s *Store) mark(key string, tier int, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.Records[key]
	r := old
	r.LastAction = now
	r.LastTier = tier
	s.Records[key] = r
	if err := s.save(); err != nil {
		s.Records[key] = old
		return err
	}
	return nil
}
func (s *Store) reset(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.Records[key]
	delete(s.Records, key)
	if e := s.save(); e != nil {
		if ok {
			s.Records[key] = old
		}
		return e
	}
	return nil
}
func (s *Store) score(key string, window time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, n := prune(s.Records[key], time.Now(), window)
	return n
}
func penaltyFor(ps []Penalty, total int, r Record, now time.Time, cooldown time.Duration) *Penalty {
	var selected *Penalty
	for i := range ps {
		if total >= ps[i].Points {
			selected = &ps[i]
		}
	}
	if selected == nil {
		return nil
	}
	if selected.Points <= r.LastTier && now.Sub(r.LastAction) < cooldown {
		return nil
	}
	return selected
}
