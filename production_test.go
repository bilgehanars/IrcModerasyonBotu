package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, e := openStore(filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestGoogleRequestAndLanguagePolicy(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-Goog-Api-Key") != "google-test" || r.Header.Get("Authorization") != "" || r.URL.RawQuery != "" {
			t.Error("wrong or exposed credentials")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["modelVersion"] != "MODEL_VERSION_1" {
			t.Error("wrong Google body")
		}
		fmt.Fprint(w, `{"moderationCategories":[{"name":"Toxic","confidence":0.98},{"name":"Violent"}],"languageSupported":false}`)
	}))
	defer srv.Close()
	m := &Moderator{Provider: "google", Client: srv.Client(), Key: "google-test", URL: srv.URL}
	if _, e := m.Check(context.Background(), "test"); e == nil {
		t.Fatal("unsupported language accepted without opt-in")
	}
	m.AllowUnsupportedLanguage = true
	scores, e := m.Check(context.Background(), "test")
	if e != nil || scores["Toxic"] != .98 || scores["Violent"] != 0 || calls.Load() != 2 {
		t.Fatal(scores, e)
	}
}
func TestModerationRetriesAndCancellation(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{"results":[{"category_scores":{"harassment":0.9}}]}`)
	}))
	defer srv.Close()
	m := &Moderator{Client: srv.Client(), URL: srv.URL, MaxRetries: 2}
	if _, e := m.Check(context.Background(), "test"); e != nil || calls.Load() != 2 {
		t.Fatal(calls.Load(), e)
	}
	m.RequestsPerMinute = 1
	m.next = time.Now().Add(time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, e := m.Check(ctx, "test"); e == nil || calls.Load() != 2 {
		t.Fatal("cancelled rate wait made request", e)
	}
	if retryDelay("9223372036854775807", 0) != 2*time.Minute {
		t.Fatal("Retry-After overflow")
	}
}
func TestMalformedAndRedirectedAPIResponses(t *testing.T) {
	for _, body := range []string{`{"results":[]}`, `{"results":[{"category_scores":{"hate":2}}]}`, `{"results":[{"category_scores":{"hate":0.99}}]} trailing`} {
		m := &Moderator{}
		if _, e := m.decode([]byte(body)); e == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	m := newModerator(testConfig(t))
	m.URL = source.URL
	m.MaxRetries = 0
	m.RequestsPerMinute = 0
	if _, e := m.Check(context.Background(), "test"); e == nil || leaked.Load() {
		t.Fatal("redirect followed")
	}
}
func TestRuntimeSettingsExceptionsPersistAndValidate(t *testing.T) {
	s := newTestStore(t)
	c := testConfig(t)
	r, e := newRuntime(c, s)
	if e != nil {
		t.Fatal(e)
	}
	v := "false"
	if e = r.set("master", "dry_run", &v); e != nil {
		t.Fatal(e)
	}
	bad := "NaN"
	if e = r.set("master", "rule.harassment.threshold", &bad); e == nil {
		t.Fatal("NaN accepted")
	}
	if e = r.set("master", "google|rule.Toxic.threshold", &bad); e == nil {
		t.Fatal("unvalidated foreign-provider setting accepted")
	}
	if e = r.set("master", "openai.api_key", &bad); e == nil {
		t.Fatal("secret edited through IRC")
	}
	if e = r.exception("master", "account", "victim", true); e != nil {
		t.Fatal(e)
	}
	if e = r.exception("master", "host", "*.example", true); e == nil {
		t.Fatal("wildcard exception accepted")
	}
	r, e = newRuntime(c, s)
	if e != nil || r.snapshot().Moderation.DryRun || !r.exempt("victim", "user.example") {
		t.Fatal(e)
	}
	if r.exempt("*", "user.example") {
		t.Fatal("unauthenticated exempt")
	}
	if e = r.set("master", "dry_run", nil); e != nil || !r.snapshot().Moderation.DryRun {
		t.Fatal(e)
	}
	if e = r.exception("master", "account", "victim", false); e != nil || r.exempt("victim", "user.example") {
		t.Fatal(e)
	}
}
func TestLateResultCannotUndoAmnestyOrException(t *testing.T) {
	s := newTestStore(t)
	r, e := newRuntime(testConfig(t), s)
	if e != nil {
		t.Fatal(e)
	}
	session := &Session{ctx: context.Background(), runtime: r, store: s}
	session.ready.Store(true)
	job := Job{Host: "user.example", Key: hostKey("user.example"), At: time.Now(), Revision: r.revision.Load()}
	r.revision.Add(1) // The same revision bump is performed by reset/forgive.
	session.enforce(job, map[string]float64{"harassment": .99}, time.Now().Add(time.Minute))
	if n, e := s.score(job.Key, time.Hour); e != nil || n != 0 {
		t.Fatal("stale result scored", n, e)
	}
	job.Revision = r.revision.Load()
	if e = r.exception("master", "host", "user.example", true); e != nil {
		t.Fatal(e)
	}
	session.enforce(job, map[string]float64{"harassment": .99}, time.Now().Add(time.Minute))
	if n, e := s.score(job.Key, time.Hour); e != nil || n != 0 {
		t.Fatal("exempt result scored", n, e)
	}
}
func TestActionLedgerAndSingleInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, e := openStore(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if other, e := openStore(path); e == nil {
		other.Close()
		t.Fatal("second instance acquired same state")
	}
	id, e := s.beginAction("host:user.example", "user.example", Penalty{Points: 10, Command: "KLINE", Duration: "0"}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	actions, e := s.activeActions("host:user.example")
	if e != nil || len(actions) != 1 || actions[0].ID != id {
		t.Fatal(actions, e)
	}
	if e = s.actionStatus(id, "removal_requested"); e != nil {
		t.Fatal(e)
	}
	actions, e = s.activeActions("host:user.example")
	if e != nil || len(actions) != 0 {
		t.Fatal(actions, e)
	}
}
func TestGeneratorPrivateAndNoOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bilgehan.conf")
	input := "openai\nirc.example.com:6697\nTestBot\noper\noper-secret\n\n#test\nmaster\napi-secret\n"
	var output bytes.Buffer
	if e := generateConfig(strings.NewReader(input), &output, path); e != nil {
		t.Fatal(e)
	}
	c, e := loadConfig(path)
	if e != nil || !c.Moderation.DryRun || c.OpenAI.APIKey != "api-secret" {
		t.Fatal(e)
	}
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, e)
	}
	if strings.Contains(output.String(), "api-secret") || strings.Contains(output.String(), "oper-secret") {
		t.Fatal("secret in output")
	}
	if e = generateConfig(strings.NewReader(input), &output, path); e == nil {
		t.Fatal("overwrote config")
	}
	partial := filepath.Join(t.TempDir(), "partial.conf")
	if e = generateConfig(strings.NewReader("openai\n"), &output, partial); e == nil {
		t.Fatal("partial config accepted")
	}
	if _, e = os.Stat(partial); !os.IsNotExist(e) {
		t.Fatal("partial file exists")
	}
}

func TestProviderSpecificOverrides(t *testing.T) {
	s := newTestStore(t)
	c := testConfig(t)
	r, e := newRuntime(c, s)
	if e != nil {
		t.Fatal(e)
	}
	v := "0.99"
	if e = r.set("master", "rule.harassment.threshold", &v); e != nil {
		t.Fatal(e)
	}
	c.Provider = "google"
	c.Google.APIKey = "test"
	c.Moderation.Rules = map[string]Rule{"Toxic": {Threshold: .9, Points: 2}}
	if _, e = newRuntime(c, s); e != nil {
		t.Fatal("OpenAI rule broke Google startup", e)
	}
}
func TestAccountSymbolsDoNotAlias(t *testing.T) {
	c := testConfig(t)
	c.IRC.OwnerAccounts = []string{"master["}
	s := &Session{c: c, accountTag: true}
	m, _ := parseMessage("@account=master{ :user!x@y PRIVMSG BilgehanBot :help")
	if s.owner(m) {
		t.Fatal("RFC1459 nick equivalence granted account permissions")
	}
}
func TestGeneratorGoogle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "google.conf")
	input := "google\nirc.example.com:6697\nTestBot\noper\noper-secret\n\n#test\nmaster\ngoogle-key\ntrue\n"
	var out bytes.Buffer
	if e := generateConfig(strings.NewReader(input), &out, path); e != nil {
		t.Fatal(e)
	}
	c, e := loadConfig(path)
	if e != nil || c.Provider != "google" || !c.Google.AllowUnsupportedLanguage || c.Moderation.Rules["Toxic"].Points != 2 || c.OpenAI.APIKey != "" {
		t.Fatal(c.Provider, e)
	}
}

func TestMissingAccountTagStopsBeforeOper(t *testing.T) {
	s := &Session{c: testConfig(t)}
	if e := s.handle(Message{Command: "001", Params: []string{"BilgehanBot"}}); e == nil {
		t.Fatal("session continued without secure master identity")
	}
}
func TestDatabaseFailureAndActiveHigherTierPreventBan(t *testing.T) {
	t.Run("closed database", func(t *testing.T) {
		s := newTestStore(t)
		r, e := newRuntime(testConfig(t), s)
		if e != nil {
			t.Fatal(e)
		}
		s.Close()
		session := &Session{ctx: context.Background(), store: s, runtime: r}
		session.ready.Store(true)
		session.enforce(Job{Host: "user.example", Key: hostKey("user.example"), Revision: r.revision.Load()}, map[string]float64{"harassment": .99}, time.Now().Add(time.Minute))
	})
	t.Run("higher tier active", func(t *testing.T) {
		s := newTestStore(t)
		c := testConfig(t)
		c.Moderation.DryRun = false
		c.Moderation.Penalties = []Penalty{{Points: 2, Command: "KLINE", Duration: "30m"}}
		r, e := newRuntime(c, s)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.beginAction(hostKey("user.example"), "user.example", Penalty{Points: 20, Command: "KLINE", Duration: "24h"}, time.Now()); e != nil {
			t.Fatal(e)
		}
		session := &Session{ctx: context.Background(), store: s, runtime: r}
		session.ready.Store(true)
		session.enforce(Job{Host: "user.example", Key: hostKey("user.example"), Revision: r.revision.Load()}, map[string]float64{"harassment": .99}, time.Now().Add(time.Minute))
		actions, e := s.activeActions(hostKey("user.example"))
		if e != nil || len(actions) != 1 {
			t.Fatal(actions, e)
		}
	})
}
