package main

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestConcurrentSQLiteUpdates(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "scores.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, e := s.add("host:one.example", 2, time.Now(), 24*time.Hour); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if n, e := s.score("host:one.example", 24*time.Hour); e != nil || n != 40 {
		t.Fatal(n, e)
	}
}

func TestSQLiteFailedInsertRollsBackPruning(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "scores.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	if _, _, err = s.add("host:one.example", 8, now, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	// A failing constraint must roll back the preceding DELETE in the same transaction.
	if _, _, err = s.add("host:two.example", 0, now.Add(48*time.Hour), 24*time.Hour); err == nil {
		t.Fatal("invalid insert accepted")
	}
	if n, e := s.score("host:one.example", 24*time.Hour); e != nil || n != 8 {
		t.Fatal("partial transaction", n, e)
	}
}
