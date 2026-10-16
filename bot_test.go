package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	c, e := loadConfig("bilgehan.conf.example")
	if e != nil {
		t.Fatal(e)
	}
	c.Moderation.StateFile = filepath.Join(t.TempDir(), "scores.json")
	return c
}
func TestScoringAndPersistence(t *testing.T) {
	c := testConfig(t)
	points, cat := scoreMessage(c.Moderation.Rules, map[string]float64{"harassment": .95, "hate/threatening": .98})
	if points != 8 || cat != "hate/threatening" {
		t.Fatal(points, cat)
	}
	s, e := openStore(c.Moderation.StateFile)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	if _, _, e = s.add("host:test", 4, now.Add(-25*time.Hour), 24*time.Hour); e != nil {
		t.Fatal(e)
	}
	r, total, e := s.add("host:test", 8, now, 24*time.Hour)
	if e != nil || total != 8 || len(r.Incidents) != 1 {
		t.Fatal(r, total, e)
	}
	s, e = openStore(c.Moderation.StateFile)
	if e != nil || s.score("host:test", 24*time.Hour) != 8 {
		t.Fatal(e)
	}
	if e = s.mark("host:test", 10, now); e != nil {
		t.Fatal(e)
	}
	r = s.Records["host:test"]
	if penaltyFor(c.Moderation.Penalties, 12, r, now, time.Hour) != nil {
		t.Fatal("cooldown ignored")
	}
	if p := penaltyFor(c.Moderation.Penalties, 20, r, now, time.Hour); p == nil || p.Points != 20 {
		t.Fatal("escalation lost")
	}
	if e = s.reset("host:test"); e != nil || s.score("host:test", 24*time.Hour) != 0 {
		t.Fatal(e)
	}
}
func TestBanTargets(t *testing.T) {
	for _, tc := range []struct {
		command, host, duration, want string
		fail                          bool
	}{
		{"KLINE", "cloak.example", "30m", "KLINE *@cloak.example 1800 :test", false},
		{"ZLINE", "203.0.113.7", "0", "ZLINE *@203.0.113.7 0 :test", false},
		{"ZLINE", "2001:db8::1", "1h", "ZLINE *@2001:db8::1 3600 :test", false},
		{"ZLINE", "cloak.example", "1h", "", true},
		{"KLINE", "*.example", "1h", "", true},
		{"KLINE", "example\r\nOPER x y", "1h", "", true},
	} {
		line, e := banLine(Penalty{Command: tc.command, Duration: tc.duration}, tc.host, "test")
		if (e != nil) != tc.fail || line != tc.want {
			t.Fatalf("%+v: %q %v", tc, line, e)
		}
	}
}
func TestOwnerCannotBeSpoofedByNickname(t *testing.T) {
	c := testConfig(t)
	s := &Session{c: c, accountTag: true}
	for _, tc := range []struct {
		line  string
		owner bool
	}{
		{":bilgehan!x@y PRIVMSG BilgehanBot :reset example", false},
		{"@account=* :bilgehan!x@y PRIVMSG BilgehanBot :help", false},
		{"@account=bilgehan :anothernick!x@y PRIVMSG BilgehanBot :help", true},
		{"@account=bilgehan\\s :anothernick!x@y PRIVMSG BilgehanBot :help", false},
	} {
		m, e := parseMessage(tc.line)
		if e != nil || s.owner(m) != tc.owner {
			t.Fatal(tc, e)
		}
	}
	s.accountTag = false
	m, _ := parseMessage("@account=bilgehan :x!y@z PRIVMSG BilgehanBot :help")
	if s.owner(m) {
		t.Fatal("unnegotiated account-tag")
	}
}
func TestAPIErrorDoesNotReturnScores(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429); fmt.Fprint(w, `{"error":"secret"}`) }))
	defer srv.Close()
	m := &Moderator{Client: srv.Client(), Key: "test", Model: "test", URL: srv.URL}
	scores, e := m.Check(context.Background(), "test")
	if e == nil || scores != nil || strings.Contains(e.Error(), "secret") {
		t.Fatal(scores, e)
	}
}
func TestConfigValidation(t *testing.T) {
	c := testConfig(t)
	c.IRC.OperPassword = "pass\r\nQUIT"
	if c.validate() == nil {
		t.Fatal("injection accepted")
	}
	path := filepath.Join(t.TempDir(), "bad")
	if e := os.WriteFile(path, []byte("null"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := loadConfig(path); e == nil {
		t.Fatal("null config accepted")
	}
}

func TestPermanentPenaltyAndWindow(t *testing.T) {
	c := testConfig(t)
	p := penaltyFor(c.Moderation.Penalties, 100, Record{}, time.Now(), time.Hour)
	if p == nil || p.Duration != "0" || p.Points != 35 {
		t.Fatal("permanent penalty not selected")
	}
	if points, _ := scoreMessage(c.Moderation.Rules, map[string]float64{"harassment": .84}); points != 0 {
		t.Fatal("below-threshold score")
	}
}

func TestMalformedStateStopsStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scores.json")
	if e := os.WriteFile(path, []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := openStore(path); e == nil {
		t.Fatal("corrupt state accepted")
	}
}

// Exercises actual TCP registration, capability negotiation, OPER, PING,
// moderation HTTP, threshold enforcement and disconnect cancellation.
func TestIRCIntegration(t *testing.T) {
	for _, dry := range []bool{true, false} {
		t.Run(fmt.Sprintf("dry=%t", dry), func(t *testing.T) {
			c := testConfig(t)
			c.IRC.TLS = false
			c.Moderation.DryRun = dry
			c.Moderation.Penalties = []Penalty{{Points: 2, Command: "KLINE", Duration: "30m"}}
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test" {
					t.Error("missing auth")
				}
				fmt.Fprint(w, `{"results":[{"category_scores":{"harassment":0.99}}]}`)
			}))
			defer api.Close()
			mod := newModerator(c)
			mod.Key = "test"
			mod.URL = api.URL
			mod.Client = api.Client()
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			c.IRC.Server = listener.Addr().String()
			store, e := openStore(c.Moderation.StateFile)
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- runSession(ctx, c, store, mod) }()
			conn, e := listener.Accept()
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			rd := bufio.NewReader(conn)
			read := func() string {
				t.Helper()
				conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				line, e := rd.ReadString('\n')
				if e != nil {
					t.Fatal(e)
				}
				return strings.TrimSpace(line)
			}
			write := func(v string) {
				t.Helper()
				if _, e := fmt.Fprintf(conn, "%s\r\n", v); e != nil {
					t.Fatal(e)
				}
			}
			if read() != "CAP LS 302" || read() != "NICK BilgehanBot" || !strings.HasPrefix(read(), "USER ") {
				t.Fatal("registration")
			}
			write(":server CAP * LS :account-tag")
			if read() != "CAP REQ :account-tag" {
				t.Fatal("cap req")
			}
			write(":server CAP * ACK :account-tag")
			if read() != "CAP END" {
				t.Fatal("cap end")
			}
			write(":server 001 BilgehanBot :Welcome")
			if !strings.HasPrefix(read(), "OPER ") {
				t.Fatal("oper")
			}
			write(":server 381 BilgehanBot :Oper")
			if read() != "JOIN #sohbet" {
				t.Fatal("join")
			}
			write("PING :test")
			if read() != "PONG :test" {
				t.Fatal("pong")
			}
			write(":user!ident@203.0.113.1 PRIVMSG #sohbet :test message")
			if !dry {
				if got := read(); !strings.HasPrefix(got, "KLINE *@203.0.113.1 1800 :") {
					t.Fatal(got)
				}
			} else {
				write("@account=bilgehan :owner!ident@safe.host PRIVMSG BilgehanBot :status")
				if got := read(); !strings.Contains(got, "dry_run=true") {
					t.Fatal(got)
				}
				conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
				if line, e := rd.ReadString('\n'); e == nil {
					t.Fatalf("dry run sent %s", line)
				}
			}
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("session did not stop")
			}
		})
	}
}
