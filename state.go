package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Record struct {
	LastAction time.Time
	LastTier   int
}
type Store struct {
	mu sync.Mutex
	db *sql.DB
}

func openStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Create with private permissions before SQLite opens the file.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	ok := false
	defer func() {
		if !ok {
			db.Close()
		}
	}()
	if _, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS users (identity TEXT PRIMARY KEY, last_action INTEGER NOT NULL DEFAULT 0, last_tier INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS incidents (id INTEGER PRIMARY KEY, identity TEXT NOT NULL REFERENCES users(identity) ON DELETE CASCADE, at INTEGER NOT NULL, points INTEGER NOT NULL CHECK(points>0));
 CREATE INDEX IF NOT EXISTS incidents_identity_at ON incidents(identity, at);
 CREATE INDEX IF NOT EXISTS incidents_expiry ON incidents(at);`); err != nil {
		return nil, fmt.Errorf("SQLite başlatılamadı: %w", err)
	}
	ok = true
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) add(key string, points int, now time.Time, window time.Duration) (Record, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r Record
	tx, err := s.db.Begin()
	if err != nil {
		return r, 0, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO users(identity) VALUES(?) ON CONFLICT DO NOTHING`, key); err != nil {
		return r, 0, err
	}
	if _, err = tx.Exec(`DELETE FROM incidents WHERE at<=?`, now.Add(-window).UnixNano()); err != nil {
		return r, 0, err
	}
	if _, err = tx.Exec(`INSERT INTO incidents(identity,at,points) VALUES(?,?,?)`, key, now.UnixNano(), points); err != nil {
		return r, 0, err
	}
	var last int64
	if err = tx.QueryRow(`SELECT last_action,last_tier FROM users WHERE identity=?`, key).Scan(&last, &r.LastTier); err != nil {
		return r, 0, err
	}
	if last != 0 {
		r.LastAction = time.Unix(0, last)
	}
	var total int
	if err = tx.QueryRow(`SELECT coalesce(sum(points),0) FROM incidents WHERE identity=? AND at>?`, key, now.Add(-window).UnixNano()).Scan(&total); err != nil {
		return r, 0, err
	}
	return r, total, tx.Commit()
}
func (s *Store) mark(key string, tier int, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO users(identity,last_action,last_tier) VALUES(?,?,?) ON CONFLICT(identity) DO UPDATE SET last_action=excluded.last_action,last_tier=excluded.last_tier`, key, now.UnixNano(), tier)
	return err
}
func (s *Store) reset(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM users WHERE identity=?`, key)
	return err
}
func (s *Store) score(key string, window time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int
	err := s.db.QueryRow(`SELECT coalesce(sum(points),0) FROM incidents WHERE identity=? AND at>?`, key, time.Now().Add(-window).UnixNano()).Scan(&total)
	return total, err
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
