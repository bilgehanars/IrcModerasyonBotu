package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestThirtyUsersOneRequestAndSplitSentenceOneScore(t *testing.T) {
	c := testConfig(t)
	store, err := openStore(c.Moderation.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runtime, err := newRuntime(c, store)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var payload struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if len(payload.Input) != 30 {
			t.Errorf("want 30 independent inputs, got %d", len(payload.Input))
		}
		results := make([]any, len(payload.Input))
		for i, text := range payload.Input {
			score := .01
			if strings.Contains(text, "ben\nsenin\nananı\nsikeyim") {
				score = .99
			}
			results[i] = map[string]any{"category_scores": map[string]float64{"harassment": score}}
		}
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	mod := newModerator(c)
	mod.URL, mod.Client = server.URL, server.Client()
	s := &Session{ctx: ctx, runtime: runtime, store: store, moderator: mod, queue: make(chan Job, 200), groups: make(chan Job, 200)}
	s.ready.Store(true)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.aggregate() }()
	go func() { defer wg.Done(); s.worker() }()
	defer func() { cancel(); wg.Wait() }()
	at := time.Now().Add(-4 * time.Second)
	for i := 0; i < 30; i++ {
		host := fmt.Sprintf("user%d.example", i)
		lines := []string{"normal"}
		if i == 7 {
			lines = []string{"ben", "senin", "ananı", "sikeyim"}
		}
		for _, line := range lines {
			s.queue <- Job{Host: host, Key: hostKey(host), Nick: fmt.Sprintf("nick%d", i), Channel: "#sohbet", Text: line, At: at, Revision: runtime.revision.Load()}
		}
	}
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		score, err := store.score(hostKey("user7.example"), time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if score == 2 {
			if requests.Load() != 1 {
				t.Fatalf("requests=%d", requests.Load())
			}
			for i := 0; i < 30; i++ {
				if i == 7 {
					continue
				}
				if n, err := store.score(hostKey(fmt.Sprintf("user%d.example", i)), time.Hour); err != nil || n != 0 {
					t.Fatalf("user %d incorrectly scored: %d %v", i, n, err)
				}
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("split sentence was not scored")
}

func TestBatchResponseCountAndGoogleDocumentGuard(t *testing.T) {
	m := &Moderator{Provider: "openai"}
	if _, err := m.decodeBatch([]byte(`{"results":[{"category_scores":{"harassment":0.9}}]}`), 2); err == nil {
		t.Fatal("missing result accepted")
	}
	if _, err := m.decodeBatch([]byte(`{"results":[{"category_scores":{"harassment":0.9}},{"category_scores":{"harassment":2}}]}`), 2); err == nil {
		t.Fatal("invalid score accepted")
	}
	m.Provider = "google"
	if _, err := m.CheckBatch(context.Background(), []string{"a", "b"}); err == nil {
		t.Fatal("multiple Google documents accepted")
	}
	if groupKey(Job{Key: "host:a", Nick: "one", Channel: "#a"}) == groupKey(Job{Key: "host:a", Nick: "two", Channel: "#a"}) || groupKey(Job{Key: "host:a", Nick: "one", Channel: "#a"}) == groupKey(Job{Key: "host:a", Nick: "one", Channel: "#b"}) {
		t.Fatal("independent users/channels merged")
	}
}
