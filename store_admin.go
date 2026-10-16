package main

import (
	"fmt"
	"time"
)

type Action struct {
	ID                              int64
	Identity, Command, Mask, Status string
	Tier                            int
	At, Expires                     int64
}
type ScoreRow struct {
	Identity string
	Points   int
	Last     time.Time
}
type IncidentRow struct {
	At       time.Time
	Points   int
	Category string
}

func (s *Store) initAdminSchema() error {
	// A numbered schema permits future upgrades without discarding scores.
	var version int
	if e := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); e != nil {
		return e
	}
	if version > 1 {
		return fmt.Errorf("veritabanı daha yeni bir bot sürümüne ait")
	}
	rows, e := s.db.Query(`PRAGMA table_info(incidents)`)
	if e != nil {
		return e
	}
	hasCategory := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var def any
		if e = rows.Scan(&cid, &name, &typ, &notnull, &def, &pk); e != nil {
			rows.Close()
			return e
		}
		hasCategory = hasCategory || name == "category"
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if !hasCategory {
		if _, e = tx.Exec(`ALTER TABLE incidents ADD COLUMN category TEXT NOT NULL DEFAULT ''`); e != nil {
			return e
		}
	}
	_, e = tx.Exec(`CREATE TABLE IF NOT EXISTS actions(id INTEGER PRIMARY KEY, identity TEXT NOT NULL, command TEXT NOT NULL, mask TEXT NOT NULL, tier INTEGER NOT NULL, at INTEGER NOT NULL, expires INTEGER NOT NULL, status TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS actions_identity ON actions(identity,status,expires);
 CREATE TABLE IF NOT EXISTS exceptions(kind TEXT NOT NULL,target TEXT NOT NULL, PRIMARY KEY(kind,target));
 CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS admin_audit(id INTEGER PRIMARY KEY,at INTEGER NOT NULL,actor TEXT NOT NULL,command TEXT NOT NULL,target TEXT NOT NULL);
 PRAGMA user_version=1;`)
	if e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) overrides() (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query(`SELECT key,value FROM settings ORDER BY key`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if e = rows.Scan(&k, &v); e != nil {
			return nil, e
		}
		out[k] = v
	}
	return out, rows.Err()
}
func (s *Store) setOverride(actor, key string, value *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if value == nil {
		_, e = tx.Exec(`DELETE FROM settings WHERE key=?`, key)
	} else {
		_, e = tx.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, *value)
	}
	if e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO admin_audit(at,actor,command,target) VALUES(?,?,?,?)`, time.Now().UnixNano(), actor, "setting", key); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) exceptionRows() (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query(`SELECT kind,target FROM exceptions`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var kind, target string
		if e = rows.Scan(&kind, &target); e != nil {
			return nil, e
		}
		out[kind+":"+target] = true
	}
	return out, rows.Err()
}
func (s *Store) changeException(actor, kind, target string, add bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if add {
		_, e = tx.Exec(`INSERT INTO exceptions(kind,target) VALUES(?,?) ON CONFLICT DO NOTHING`, kind, target)
	} else {
		_, e = tx.Exec(`DELETE FROM exceptions WHERE kind=? AND target=?`, kind, target)
	}
	if e != nil {
		return e
	}
	op := "exception del"
	if add {
		op = "exception add"
	}
	if _, e = tx.Exec(`INSERT INTO admin_audit(at,actor,command,target) VALUES(?,?,?,?)`, time.Now().UnixNano(), actor, op, kind+":"+target); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) listScores(window time.Duration, offset int) ([]ScoreRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query(`SELECT identity,sum(points),max(at) FROM incidents WHERE at>? GROUP BY identity ORDER BY sum(points) DESC,identity LIMIT 10 OFFSET ?`, time.Now().Add(-window).UnixNano(), offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var result []ScoreRow
	for rows.Next() {
		var r ScoreRow
		var at int64
		if e = rows.Scan(&r.Identity, &r.Points, &at); e != nil {
			return nil, e
		}
		r.Last = time.Unix(0, at)
		result = append(result, r)
	}
	return result, rows.Err()
}
func (s *Store) history(key string, offset int) ([]IncidentRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.Query(`SELECT at,points,category FROM incidents WHERE identity=? ORDER BY at DESC,id DESC LIMIT 10 OFFSET ?`, key, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var result []IncidentRow
	for rows.Next() {
		var r IncidentRow
		var at int64
		if e = rows.Scan(&at, &r.Points, &r.Category); e != nil {
			return nil, e
		}
		r.At = time.Unix(0, at)
		result = append(result, r)
	}
	return result, rows.Err()
}
func (s *Store) beginAction(key, host string, p Penalty, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	expires := int64(0)
	if p.Duration != "0" {
		d, e := time.ParseDuration(p.Duration)
		if e != nil {
			return 0, e
		}
		expires = now.Add(d).UnixNano()
	}
	result, e := s.db.Exec(`INSERT INTO actions(identity,command,mask,tier,at,expires,status) VALUES(?,?,?,?,?,?, 'pending')`, key, p.Command, "*@"+host, p.Points, now.UnixNano(), expires)
	if e != nil {
		return 0, e
	}
	return result.LastInsertId()
}
func (s *Store) actionStatus(id int64, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, e := s.db.Exec(`UPDATE actions SET status=? WHERE id=?`, status, id)
	return e
}
func (s *Store) activeActions(key string) ([]Action, error)    { return s.actionsFor(key, false) }
func (s *Store) removableActions(key string) ([]Action, error) { return s.actionsFor(key, true) }
func (s *Store) actionsFor(key string, removals bool) ([]Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	statuses := "status IN ('pending','requested')"
	if removals {
		statuses = "status IN ('pending','requested','removal_requested')"
	}
	rows, e := s.db.Query(`SELECT id,identity,command,mask,tier,at,expires,status FROM actions WHERE identity=? AND `+statuses+` AND (expires=0 OR expires>?) ORDER BY id`, key, time.Now().UnixNano())
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var result []Action
	for rows.Next() {
		var a Action
		if e = rows.Scan(&a.ID, &a.Identity, &a.Command, &a.Mask, &a.Tier, &a.At, &a.Expires, &a.Status); e != nil {
			return nil, e
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
func (s *Store) forgive(actor, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`DELETE FROM users WHERE identity=?`, key); e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO admin_audit(at,actor,command,target) VALUES(?,?, 'forgive',?)`, time.Now().UnixNano(), actor, key); e != nil {
		return e
	}
	return tx.Commit()
}

// The prior score update and its category are committed in a single transaction.
func (s *Store) addIncident(key string, points int, category string, now time.Time, window time.Duration) (Record, int, error) {
	return s.addWithCategory(key, points, category, now, window)
}
