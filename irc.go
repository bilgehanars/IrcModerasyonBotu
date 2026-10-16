package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lrstanley/girc"
)

type Message struct {
	Tags            map[string]string
	Prefix, Command string
	Params          []string
}

func fromEvent(e girc.Event) Message {
	m := Message{Command: e.Command, Params: e.Params, Tags: map[string]string{}}
	if e.Source != nil {
		m.Prefix = e.Source.String()
	}
	for key := range e.Tags {
		m.Tags[key], _ = e.Tags.Get(key)
	}
	return m
}
func parseMessage(line string) (Message, error) {
	e := girc.ParseEvent(line)
	if e == nil {
		return Message{}, fmt.Errorf("geçersiz IRC mesajı")
	}
	return fromEvent(*e), nil
}
func fold(v string) string { return girc.ToRFC1459(v) }
func accountID(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
func sender(prefix string) (nick, user, host string) {
	i := strings.IndexByte(prefix, '!')
	j := strings.LastIndexByte(prefix, '@')
	if i < 1 || j <= i+1 || j == len(prefix)-1 {
		return
	}
	return prefix[:i], prefix[i+1 : j], prefix[j+1:]
}

type Job struct {
	Nick, Host, Key, Text string
	Account               string
	Revision              uint64
	At                    time.Time
}
type Session struct {
	c                        Config
	client                   *girc.Client
	store                    *Store
	moderator                *Moderator
	ctx                      context.Context
	cancel                   context.CancelFunc
	queue                    chan Job
	nick                     string
	accountTag, oper, joined bool
	runtime                  *Runtime
	actionMu                 sync.Mutex
	ready                    atomic.Bool
	out                      chan string
	joinedChannels           map[string]bool
	handshakeDone            chan struct{}
	handshakeOnce            sync.Once
}

func (s *Session) send(line string) error {
	if !safeText(line) || len(line) > 510 {
		return fmt.Errorf("IRC satırı geçersiz veya çok uzun")
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if !s.client.IsConnected() {
		return girc.ErrNotConnected
	}
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.out <- line:
		return nil
	default:
		return fmt.Errorf("IRC gönderim kuyruğu dolu")
	}

}
func (s *Session) notice(nick, text string) {
	if atom(nick) {
		text = strings.NewReplacer("\r", " ", "\n", " ", "\x00", "").Replace(text)
		if e := s.send("NOTICE " + nick + " :" + clip(text, 350)); e != nil {
			log.Print("NOTICE gönderilemedi")
		}
	}
}
func (s *Session) owner(m Message) bool {
	if !s.accountTag {
		return false
	}
	a := m.Tags["account"]
	if a == "" || a == "*" {
		return false
	}
	for _, v := range s.c.IRC.OwnerAccounts {
		if accountID(v) == accountID(a) {
			return true
		}
	}
	return false
}
func validHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if host == "" || len(host) > 253 {
		return false
	}
	for _, r := range host {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune(".-_/", r)) {
			return false
		}
	}
	return true
}
func hostKey(host string) string {
	if ip := net.ParseIP(host); ip != nil {
		return "host:" + ip.String()
	}
	return "host:" + strings.ToLower(host)
}
func banLine(p Penalty, host, reason string) (string, error) {
	if !validHost(host) {
		return "", fmt.Errorf("geçersiz host")
	}
	if p.Command == "ZLINE" && net.ParseIP(host) == nil {
		return "", fmt.Errorf("ZLINE için gerçek IP görünmüyor")
	}
	if p.Command != "KLINE" && p.Command != "ZLINE" {
		return "", fmt.Errorf("desteklenmeyen komut")
	}
	duration := "0"
	if p.Duration != "0" {
		d, e := time.ParseDuration(p.Duration)
		if e != nil || d < time.Second {
			return "", fmt.Errorf("geçersiz süre")
		}
		duration = strconv.FormatInt(int64(d/time.Second), 10)
	}
	if !safeText(reason) {
		return "", fmt.Errorf("geçersiz neden")
	}
	return p.Command + " *@" + host + " " + duration + " :" + reason, nil
}
func (s *Session) handle(m Message) error {
	switch m.Command {
	case "001":
		if !s.accountTag {
			return fmt.Errorf("güvenli master kontrolü için account-tag gerekli")
		}
		if len(m.Params) > 0 {
			s.nick = m.Params[0]
		}
		log.Printf("IRC bağlantısı kuruldu; account-tag=%t", s.accountTag)
		return s.send("OPER " + s.c.IRC.OperName + " " + s.c.IRC.OperPassword)
	case "381":
		s.oper = true
		s.ready.Store(true)
		if !s.joined {
			for _, ch := range s.c.IRC.Channels {
				if e := s.send("JOIN " + ch); e != nil {
					return e
				}
			}
			s.joined = true
			log.Print("OPER başarılı; kanallara katılım istendi")
		}
	case "471", "473", "474", "475", "477":
		return fmt.Errorf("kanala giriş reddedildi: %s", m.Command)
	case "JOIN":
		nick, _, _ := sender(m.Prefix)
		if fold(nick) == fold(s.nick) && len(m.Params) > 0 {
			s.joinedChannels[fold(m.Params[0])] = true
			all := true
			for _, ch := range s.c.IRC.Channels {
				all = all && s.joinedChannels[fold(ch)]
			}
			if all {
				s.handshakeOnce.Do(func() { close(s.handshakeDone) })
			}
		}
	case "464", "491", "481":
		return fmt.Errorf("IRC yetkilendirme hatası %s", m.Command)
	case "421":
		return fmt.Errorf("sunucu bir bot komutunu desteklemiyor (421)")
	case "MODE":
		if len(m.Params) >= 2 && fold(m.Params[0]) == fold(s.nick) {
			adding := true
			for _, mode := range m.Params[1] {
				switch mode {
				case '+':
					adding = true
				case '-':
					adding = false
				case 'o':
					if !adding {
						s.ready.Store(false)
						return fmt.Errorf("OPER yetkisi kaldırıldı")
					}
				}
			}
		}
	case "433":
		return fmt.Errorf("bot nicki kullanımda")
	case "ERROR":
		return fmt.Errorf("sunucu bağlantıyı kapattı")
	case "KICK":
		if len(m.Params) >= 2 && fold(m.Params[1]) == fold(s.nick) {
			return fmt.Errorf("bot kanaldan çıkarıldı")
		}
	case "NICK":
		nick, _, _ := sender(m.Prefix)
		if fold(nick) == fold(s.nick) && len(m.Params) > 0 {
			s.nick = m.Params[0]
		}
	case "PRIVMSG":
		if len(m.Params) != 2 {
			return nil
		}
		if fold(m.Params[0]) == fold(s.nick) {
			s.command(m)
			return nil
		}
		if !s.oper || !s.joined || !s.joinedChannels[fold(m.Params[0])] || s.owner(m) {
			return nil
		}
		watched := false
		for _, ch := range s.c.IRC.Channels {
			if fold(ch) == fold(m.Params[0]) {
				watched = true
			}
		}
		if !watched {
			return nil
		}
		nick, user, host := sender(m.Prefix)
		if nick == "" || user == "" || !validHost(host) || fold(nick) == fold(s.nick) {
			return nil
		}
		account := m.Tags["account"]
		if !s.accountTag {
			account = ""
		}
		if s.runtime.exempt(account, host) {
			return nil
		}
		text := m.Params[1]
		if strings.HasPrefix(text, "\x01ACTION ") && strings.HasSuffix(text, "\x01") {
			text = strings.TrimSuffix(strings.TrimPrefix(text, "\x01ACTION "), "\x01")
		} else if strings.HasPrefix(text, "\x01") {
			return nil
		}
		if strings.TrimSpace(text) == "" {
			return nil
		}
		select {
		case s.queue <- Job{Nick: nick, Host: host, Key: hostKey(host), Text: text, At: time.Now(), Account: account, Revision: s.runtime.revision.Load()}:
		default:
			log.Print("Moderation kuyruğu dolu; mesaj atlandı")
		}
	}
	return nil
}
func (s *Session) worker() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case j := <-s.queue:
			c := s.runtime.snapshot()
			deadline := j.At.Add(time.Duration(c.Moderation.MaxMessageAgeSeconds) * time.Second)
			if time.Now().After(deadline) || j.Revision != s.runtime.revision.Load() || s.runtime.exempt(j.Account, j.Host) {
				continue
			}
			ctx, cancel := context.WithDeadline(s.ctx, deadline)
			scores, e := s.moderator.Check(ctx, j.Text)
			cancel()
			if e != nil {
				if s.ctx.Err() == nil {
					log.Printf("Kontrol atlandı: %v", e)
				}
				continue
			}
			s.enforce(j, scores, deadline)
		}
	}
}
func (s *Session) enforce(j Job, scores map[string]float64, deadline time.Time) {
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	if s.ctx.Err() != nil || !s.ready.Load() || time.Now().After(deadline) || j.Revision != s.runtime.revision.Load() || s.runtime.exempt(j.Account, j.Host) {
		return
	}
	c := s.runtime.snapshot()
	points, category := scoreMessage(c.Moderation.Rules, scores)
	if points == 0 {
		return
	}
	now := time.Now()
	r, total, e := s.store.addIncident(j.Key, points, category, now, time.Duration(c.Moderation.ScoreWindowHours)*time.Hour)
	if e != nil {
		log.Print("SQLite puan kaydı başarısız; ceza uygulanmadı")
		return
	}
	log.Printf("İhlal host=%s kategori=%s ek=%d toplam=%d", j.Host, category, points, total)
	p := penaltyFor(c.Moderation.Penalties, total, r, now, time.Duration(c.Moderation.ActionCooldownSeconds)*time.Second)
	if p == nil {
		return
	}
	line, e := banLine(*p, j.Host, c.Moderation.Reason)
	if e != nil {
		log.Printf("Ceza atlandı: %v", e)
		return
	}
	if c.Moderation.DryRun {
		log.Printf("DRY-RUN %s", line)
		return
	}
	active, e := s.store.activeActions(j.Key)
	if e != nil {
		log.Print("Ban geçmişi okunamadı; ceza atlandı")
		return
	}
	for _, a := range active {
		if a.Tier >= p.Points {
			return
		}
	}
	id, e := s.store.beginAction(j.Key, j.Host, *p, now)
	if e != nil {
		log.Print("Ban talebi kaydedilemedi; ceza atlandı")
		return
	}
	if e = s.send(line); e != nil {
		s.store.actionStatus(id, "failed")
		log.Print("Ban talebi kuyruğa alınamadı")
		return
	}
	if e = s.store.actionStatus(id, "requested"); e != nil {
		log.Print("Ban talebi durumu kaydedilemedi")
	}
	if e = s.store.mark(j.Key, p.Points, now); e != nil {
		log.Print("Ceza geçmişi kaydedilemedi")
	}
	log.Printf("Ceza talebi IRC gönderim kuyruğuna alındı: %s", line)
}
func runSession(ctx context.Context, c Config, store *Store, mod *Moderator) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	runtime, err := newRuntime(c, store)
	if err != nil {
		return fmt.Errorf("kalıcı ayarlar yüklenemedi: %w", err)
	}
	host, portText, err := net.SplitHostPort(c.IRC.Server)
	if err != nil {
		return err
	}
	port, _ := strconv.Atoi(portText)
	client := girc.New(girc.Config{
		Server: host, Port: port, Nick: c.IRC.Nick, User: c.IRC.User, Name: c.IRC.RealName, ServerPass: c.IRC.ServerPassword,
		SSL: c.IRC.TLS, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host},
		DisableSTS: true, DisableSTSFallback: true, DisableAutoWHOOnJoin: true, DisableAutoMODEOnJoin: true,
		Debug: io.Discard, PingDelay: 60 * time.Second, PingTimeout: 90 * time.Second,
		HandleNickCollide: func(string) string { return "" },
	})
	sessionCtx, cancel := context.WithCancel(ctx)
	s := &Session{c: c, client: client, store: store, moderator: mod, ctx: sessionCtx, cancel: cancel, queue: make(chan Job, c.Moderation.QueueSize), nick: c.IRC.Nick, runtime: runtime, out: make(chan string, 128), joinedChannels: map[string]bool{}, handshakeDone: make(chan struct{})}
	var wg sync.WaitGroup
	initialized := make(chan struct{})
	connectDone := make(chan struct{})
	defer func() { cancel(); close(connectDone); wg.Wait() }()
	client.Handlers.Add(girc.INITIALIZED, func(cl *girc.Client, _ girc.Event) {
		close(initialized)
		if sessionCtx.Err() != nil {
			cl.Close()
		}
	})
	for _, command := range []string{"JOIN", "471", "473", "474", "475", "477", "001", "381", "464", "491", "481", "421", "MODE", "433", "ERROR", "KICK", "NICK", "PRIVMSG"} {
		client.Handlers.Add(command, func(cl *girc.Client, event girc.Event) {
			if sessionCtx.Err() != nil {
				return
			}
			s.accountTag = cl.HasCapability("account-tag")
			if err := s.handle(fromEvent(event)); err != nil {
				log.Printf("IRC oturumu durduruldu: %v", err)
				cancel()
				cl.Close()
			}
		})
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-sessionCtx.Done()
		select {
		case <-initialized:
			client.Close()
		case <-connectDone:
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-initialized:
		case <-sessionCtx.Done():
			return
		}
		for {
			select {
			case <-sessionCtx.Done():
				return
			case line := <-s.out:
				if sessionCtx.Err() != nil {
					return
				}
				if e := client.Cmd.SendRaw(line); e != nil {
					log.Print("IRC gönderimi başarısız")
					cancel()
					return
				}
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := time.NewTimer(45 * time.Second)
		defer timer.Stop()
		select {
		case <-sessionCtx.Done():
		case <-s.handshakeDone:
		case <-timer.C:
			log.Print("IRC kayıt/OPER/JOIN zaman aşımı")
			cancel()
		}
	}()
	wg.Add(1)
	go func() { defer wg.Done(); s.worker() }()
	if err = client.Connect(); err != nil {
		return fmt.Errorf("IRC bağlantısı kesildi veya kurulamadı")
	}
	return fmt.Errorf("IRC oturumu kapandı")
}
