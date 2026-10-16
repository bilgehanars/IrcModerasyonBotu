package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
func pageOffset(parts []string, index int) (int, error) {
	if len(parts) <= index {
		return 0, nil
	}
	n, e := strconv.Atoi(parts[index])
	if e != nil || n < 1 || n > 10000 {
		return 0, fmt.Errorf("sayfa 1-10000 arasında olmalı")
	}
	return (n - 1) * 10, nil
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
	// Keep mutation and enforcement serial. In-flight/queued API results become stale.
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	c := s.runtime.snapshot()
	window := time.Duration(c.Moderation.ScoreWindowHours) * time.Hour
	actor := m.Tags["account"]
	reply := func(v string) { s.notice(nick, v) }
	fail := func(e error) { reply("İşlem başarısız: " + clip(e.Error(), 180)) }
	switch strings.ToLower(parts[0]) {
	case "help":
		reply("status | score <host> | list [sayfa] | query <host> [sayfa] | forgive <host> | reset <host>")
		reply("settings | set <ayar> <değer> | unset <ayar> | except list [sayfa] | except add/del account/host <değer>")
	case "status":
		reply(fmt.Sprintf("OPER=%t provider=%s dry_run=%t kuyruk=%d/%d", s.ready.Load(), c.Provider, c.Moderation.DryRun, len(s.queue), cap(s.queue)))
	case "score", "reset", "forgive", "query":
		if len(parts) < 2 || !validHost(parts[1]) {
			reply("Tam host/IP belirt; nick veya wildcard kullanma.")
			return
		}
		key := hostKey(parts[1])
		switch strings.ToLower(parts[0]) {
		case "score":
			n, e := s.store.score(key, window)
			if e != nil {
				fail(e)
				return
			}
			reply(fmt.Sprintf("%s puan=%d", parts[1], n))
		case "reset":
			if e := s.store.forgive(actor, key); e != nil {
				fail(e)
				return
			}
			s.runtime.revision.Add(1)
			reply("Puanlar temizlendi; sunucudaki banlar kaldırılmadı.")
		case "forgive":
			if !s.ready.Load() {
				reply("OPER hazır değil.")
				return
			}
			actions, e := s.store.removableActions(key)
			if e != nil {
				fail(e)
				return
			}
			s.runtime.revision.Add(1)
			for _, a := range actions {
				if !strings.HasPrefix(a.Mask, "*@") || !validHost(strings.TrimPrefix(a.Mask, "*@")) || hostKey(strings.TrimPrefix(a.Mask, "*@")) != key {
					reply("Kayıtlı ban maskesi hedefle eşleşmiyor; işlem durdu.")
					return
				}
				if a.Command != "KLINE" && a.Command != "ZLINE" {
					reply("Geçersiz kayıtlı komut; işlem durdu.")
					return
				}
				if e = s.send(a.Command + " -" + a.Mask); e != nil {
					fail(e)
					return
				}
				if e = s.store.actionStatus(a.ID, "removal_requested"); e != nil {
					fail(e)
					return
				}
			}
			if e = s.store.forgive(actor, key); e != nil {
				fail(e)
				return
			}
			reply(fmt.Sprintf("Puanlar temizlendi; botun %d aktif banı için kaldırma talebi kuyruğa alındı. Sunucu sonucunu kontrol et.", len(actions)))
		case "query":
			offset, e := pageOffset(parts, 2)
			if e != nil {
				fail(e)
				return
			}
			rows, e := s.store.history(key, offset)
			if e != nil {
				fail(e)
				return
			}
			if len(rows) == 0 {
				reply("İhlal kaydı yok.")
			}
			for _, r := range rows {
				reply(fmt.Sprintf("%s puan=%d kategori=%s", r.At.UTC().Format(time.RFC3339), r.Points, r.Category))
			}
		}
	case "list":
		offset, e := pageOffset(parts, 1)
		if e != nil {
			fail(e)
			return
		}
		rows, e := s.store.listScores(window, offset)
		if e != nil {
			fail(e)
			return
		}
		if len(rows) == 0 {
			reply("Puan kaydı yok.")
		}
		for _, r := range rows {
			reply(fmt.Sprintf("%s puan=%d son=%s", strings.TrimPrefix(r.Identity, "host:"), r.Points, r.Last.UTC().Format(time.RFC3339)))
		}
	case "settings":
		reply(fmt.Sprintf("dry_run=%t score_window_hours=%d action_cooldown_seconds=%d max_message_age_seconds=%d", c.Moderation.DryRun, c.Moderation.ScoreWindowHours, c.Moderation.ActionCooldownSeconds, c.Moderation.MaxMessageAgeSeconds))
		keys := make([]string, 0, len(c.Moderation.Rules))
		for k := range c.Moderation.Rules {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			r := c.Moderation.Rules[k]
			reply(fmt.Sprintf("rule.%s.threshold=%g rule.%s.points=%d", k, r.Threshold, k, r.Points))
		}
		for _, p := range c.Moderation.Penalties {
			reply(fmt.Sprintf("penalty.%d.command=%s penalty.%d.duration=%s", p.Points, p.Command, p.Points, p.Duration))
		}
	case "set":
		if len(parts) < 3 {
			reply("set <ayar> <değer>")
			return
		}
		v := strings.Join(parts[2:], " ")
		if e := s.runtime.set(actor, parts[1], &v); e != nil {
			fail(e)
			return
		}
		reply("Ayar uygulandı ve SQLite'a kaydedildi.")
	case "unset":
		if len(parts) != 2 {
			reply("unset <ayar>")
			return
		}
		if e := s.runtime.set(actor, parts[1], nil); e != nil {
			fail(e)
			return
		}
		reply("Kalıcı değişiklik kaldırıldı; conf değeri kullanılıyor.")
	case "except", "exception":
		if len(parts) < 2 {
			reply("except list | except add/del account/host <değer>")
			return
		}
		if parts[1] == "list" {
			offset, e := pageOffset(parts, 2)
			if e != nil {
				fail(e)
				return
			}
			rows := s.runtime.exceptionList()
			if offset >= len(rows) {
				reply("Exception kaydı yok.")
				return
			}
			for _, row := range rows[offset:min(offset+10, len(rows))] {
				reply(row)
			}
			return
		}
		if len(parts) != 4 || (parts[1] != "add" && parts[1] != "del") {
			reply("except add/del account/host <değer>")
			return
		}
		if e := s.runtime.exception(actor, parts[2], parts[3], parts[1] == "add"); e != nil {
			fail(e)
			return
		}
		reply("Exception listesi güncellendi.")
	default:
		reply("Bilinmeyen komut. help yaz.")
	}
}
