package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Message struct {
	Tags            map[string]string
	Prefix, Command string
	Params          []string
}

func parseMessage(line string) (Message, error) {
	m := Message{Tags: map[string]string{}}
	if strings.HasPrefix(line, "@") {
		i := strings.IndexByte(line, ' ')
		if i < 0 {
			return m, fmt.Errorf("tags")
		}
		for _, tag := range strings.Split(line[1:i], ";") {
			kv := strings.SplitN(tag, "=", 2)
			if len(kv) == 2 {
				m.Tags[kv[0]] = unescapeTag(kv[1])
			}
		}
		line = strings.TrimLeft(line[i+1:], " ")
	}
	if strings.HasPrefix(line, ":") {
		i := strings.IndexByte(line, ' ')
		if i < 0 {
			return m, fmt.Errorf("prefix")
		}
		m.Prefix = line[1:i]
		line = strings.TrimLeft(line[i+1:], " ")
	}
	for line != "" {
		if line[0] == ':' {
			m.Params = append(m.Params, line[1:])
			break
		}
		i := strings.IndexByte(line, ' ')
		if i < 0 {
			m.Params = append(m.Params, line)
			break
		}
		m.Params = append(m.Params, line[:i])
		line = strings.TrimLeft(line[i+1:], " ")
	}
	if len(m.Params) == 0 {
		return m, fmt.Errorf("empty")
	}
	m.Command = strings.ToUpper(m.Params[0])
	m.Params = m.Params[1:]
	return m, nil
}
func unescapeTag(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] != '\\' {
			b.WriteByte(v[i])
			continue
		}
		i++
		if i == len(v) {
			break
		}
		switch v[i] {
		case ':':
			b.WriteByte(';')
		case 's':
			b.WriteByte(' ')
		case 'r':
			b.WriteByte('\r')
		case 'n':
			b.WriteByte('\n')
		default:
			b.WriteByte(v[i])
		}
	}
	return b.String()
}
func fold(v string) string {
	return strings.NewReplacer("[", "{", "]", "}", "\\", "|", "^", "~").Replace(strings.ToLower(v))
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
	At                    time.Time
}
type Session struct {
	c                                  Config
	conn                               net.Conn
	writeMu                            sync.Mutex
	store                              *Store
	moderator                          *Moderator
	ctx                                context.Context
	cancel                             context.CancelFunc
	queue                              chan Job
	nick                               string
	accountTag, capEnded, oper, joined bool
}

func (s *Session) send(line string) error {
	if !safeText(line) || len(line) > 510 {
		return fmt.Errorf("IRC satırı geçersiz veya çok uzun")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return err
	}
	_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := fmt.Fprintf(s.conn, "%s\r\n", line)
	if err != nil {
		s.cancel()
		s.conn.Close()
	}
	return err
}
func (s *Session) notice(nick, text string) {
	if atom(nick) {
		if e := s.send("NOTICE " + nick + " :" + text); e != nil {
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
		if fold(v) == fold(a) {
			return true
		}
	}
	return false
}
func (s *Session) command(m Message) {
	nick, _, _ := sender(m.Prefix)
	if nick == "" || !s.owner(m) {
		return
	}
	parts := strings.Fields(m.Params[1])
	if len(parts) == 0 {
		return
	}
	window := time.Duration(s.c.Moderation.ScoreWindowHours) * time.Hour
	switch strings.ToLower(parts[0]) {
	case "help":
		s.notice(nick, "Komutlar: status | score <tam-host/IP> | reset <tam-host/IP> | help")
	case "status":
		s.notice(nick, fmt.Sprintf("OPER=%t dry_run=%t kuyruk=%d/%d", s.oper, s.c.Moderation.DryRun, len(s.queue), cap(s.queue)))
	case "score", "reset":
		if len(parts) != 2 || !validHost(parts[1]) {
			s.notice(nick, "Tam host/IP belirt: score <host> veya reset <host>")
			return
		}
		key := hostKey(parts[1])
		if strings.ToLower(parts[0]) == "score" {
			s.notice(nick, fmt.Sprintf("Puan: %d", s.store.score(key, window)))
			return
		}
		if err := s.store.reset(key); err != nil {
			s.notice(nick, "Puan dosyası yazılamadı")
			return
		}
		s.notice(nick, "Puan ve ceza geçmişi sıfırlandı. Sunucudaki mevcut ban kaldırılmadı.")
	}
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
	case "PING":
		if len(m.Params) > 0 {
			return s.send("PONG :" + m.Params[len(m.Params)-1])
		}
	case "CAP":
		if len(m.Params) < 3 {
			return nil
		}
		sub := strings.ToUpper(m.Params[1])
		caps := strings.Fields(m.Params[len(m.Params)-1])
		switch sub {
		case "LS":
			for _, v := range caps {
				if v == "account-tag" {
					s.accountTag = true
				}
			}
			if len(m.Params) == 4 && m.Params[2] == "*" {
				return nil
			}
			if s.accountTag {
				s.accountTag = false
				return s.send("CAP REQ :account-tag")
			}
			return s.endCAP()
		case "ACK":
			for _, v := range caps {
				if v == "account-tag" {
					s.accountTag = true
				}
			}
			return s.endCAP()
		case "NAK":
			s.accountTag = false
			return s.endCAP()
		case "DEL":
			for _, v := range caps {
				if v == "account-tag" {
					s.accountTag = false
				}
			}
		}
	case "001":
		if len(m.Params) > 0 {
			s.nick = m.Params[0]
		}
		log.Printf("IRC bağlantısı kuruldu; account-tag=%t", s.accountTag)
		return s.send("OPER " + s.c.IRC.OperName + " " + s.c.IRC.OperPassword)
	case "381":
		s.oper = true
		if !s.joined {
			for _, ch := range s.c.IRC.Channels {
				if e := s.send("JOIN " + ch); e != nil {
					return e
				}
			}
			s.joined = true
			log.Print("OPER başarılı; kanallara katılım istendi")
		}
	case "464", "491", "481":
		return fmt.Errorf("IRC yetkilendirme hatası %s", m.Command)
	case "421":
		if len(m.Params) > 1 && strings.EqualFold(m.Params[1], "CAP") {
			return s.endCAP()
		}
		log.Print("Sunucu desteklenmeyen komut hatası (421) bildirdi")
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
		if !s.oper || !s.joined || s.owner(m) {
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
		case s.queue <- Job{nick, host, hostKey(host), text, time.Now()}:
		default:
			log.Print("Moderation kuyruğu dolu; mesaj atlandı")
		}
	}
	return nil
}
func (s *Session) endCAP() error {
	if s.capEnded {
		return nil
	}
	s.capEnded = true
	return s.send("CAP END")
}
func (s *Session) worker() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case j := <-s.queue:
			if time.Since(j.At) > time.Duration(s.c.Moderation.MaxMessageAgeSeconds)*time.Second {
				continue
			}
			scores, err := s.moderator.Check(s.ctx, j.Text)
			if err != nil {
				if s.ctx.Err() == nil {
					log.Printf("Kontrol atlandı: %v", err)
				}
				continue
			}
			if s.ctx.Err() != nil || time.Since(j.At) > time.Duration(s.c.Moderation.MaxMessageAgeSeconds)*time.Second {
				continue
			}
			points, category := scoreMessage(s.c.Moderation.Rules, scores)
			if points == 0 {
				continue
			}
			now := time.Now()
			r, total, err := s.store.add(j.Key, points, now, time.Duration(s.c.Moderation.ScoreWindowHours)*time.Hour)
			if err != nil {
				log.Print("Puan kaydı başarısız; ceza uygulanmadı")
				continue
			}
			log.Printf("İhlal host=%s kategori=%s ek=%d toplam=%d", j.Host, category, points, total)
			p := penaltyFor(s.c.Moderation.Penalties, total, r, now, time.Duration(s.c.Moderation.ActionCooldownSeconds)*time.Second)
			if p == nil {
				continue
			}
			line, err := banLine(*p, j.Host, s.c.Moderation.Reason)
			if err != nil {
				log.Printf("Ceza atlandı: %v", err)
				continue
			}
			if s.c.Moderation.DryRun {
				log.Printf("DRY-RUN %s", line)
			} else {
				if err = s.send(line); err != nil {
					continue
				}
				log.Printf("Ceza talebi sunucuya gönderildi: %s", line)
			}
			if err = s.store.mark(j.Key, p.Points, now); err != nil {
				log.Print("Ceza geçmişi kaydedilemedi")
			}
		}
	}
}
func runSession(ctx context.Context, c Config, store *Store, mod *Moderator) error {
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if c.IRC.TLS {
		td := tls.Dialer{NetDialer: dialer, Config: &tls.Config{MinVersion: tls.VersionTLS12}}
		conn, err = td.DialContext(ctx, "tcp", c.IRC.Server)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", c.IRC.Server)
	}
	if err != nil {
		return fmt.Errorf("IRC bağlantısı kurulamadı: %w", err)
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	s := &Session{c: c, conn: conn, store: store, moderator: mod, ctx: sessionCtx, cancel: cancel, queue: make(chan Job, c.Moderation.QueueSize), nick: c.IRC.Nick}
	var wg sync.WaitGroup
	defer func() { cancel(); conn.Close(); wg.Wait() }()
	wg.Add(1)
	go func() { defer wg.Done(); <-sessionCtx.Done(); conn.Close() }()
	for _, line := range []string{"CAP LS 302"} {
		if err = s.send(line); err != nil {
			return err
		}
	}
	if c.IRC.ServerPassword != "" {
		if err = s.send("PASS :" + c.IRC.ServerPassword); err != nil {
			return err
		}
	}
	if err = s.send("NICK " + c.IRC.Nick); err != nil {
		return err
	}
	if err = s.send("USER " + c.IRC.User + " 0 * :" + c.IRC.RealName); err != nil {
		return err
	}
	wg.Add(1)
	go func() { defer wg.Done(); s.worker() }()
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-sessionCtx.Done():
				return
			case <-t.C:
				if e := s.send("PING :bilgehan"); e != nil {
					return
				}
			}
		}
	}()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 16384)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(150 * time.Second))
		if !scanner.Scan() {
			break
		}
		m, e := parseMessage(strings.TrimSuffix(scanner.Text(), "\r"))
		if e != nil {
			continue
		}
		if e = s.handle(m); e != nil {
			return e
		}
	}
	if err = scanner.Err(); err != nil {
		return fmt.Errorf("IRC okuma kesildi: %w", err)
	}
	return fmt.Errorf("IRC bağlantısı kapandı")
}
