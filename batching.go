package main

import (
	"log"
	"strings"
	"time"
)

const (
	groupIdle     = 3 * time.Second
	groupMaxAge   = 10 * time.Second
	groupMaxLines = 8
	groupMaxBytes = 1500
)

type messageGroup struct {
	job   Job
	last  time.Time
	lines int
}

func groupKey(j Job) string {
	// Channel and nick separate users sharing a host; account changes also
	// start a new group. Scores and bans retain their existing host identity.
	return j.Key + "\x00" + j.Channel + "\x00" + fold(j.Nick) + "\x00" + accountID(j.Account)
}

func (s *Session) aggregate() {
	groups := make(map[string]*messageGroup)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	flush := func(key string) {
		g := groups[key]
		if g == nil {
			return
		}
		delete(groups, key)
		if g.job.Revision != s.runtime.revision.Load() || s.runtime.exempt(g.job.Account, g.job.Host) {
			return
		}
		select {
		case <-s.ctx.Done():
		case s.groups <- g.job:
		default:
			log.Print("Moderation grup kuyruğu dolu; grup atlandı")
		}
	}
	for {
		select {
		case <-s.ctx.Done():
			return
		case j := <-s.queue:
			if j.Revision != s.runtime.revision.Load() || s.runtime.exempt(j.Account, j.Host) {
				continue
			}
			key := groupKey(j)
			g := groups[key]
			if g != nil && (j.At.Sub(g.last) >= groupIdle || j.At.Sub(g.job.At) >= groupMaxAge || len(g.job.Text)+1+len(j.Text) > groupMaxBytes) {
				flush(key)
				g = nil
			}
			if g == nil {
				if len(groups) >= cap(s.queue) {
					oldKey := ""
					var oldest time.Time
					for k, item := range groups {
						if oldKey == "" || item.job.At.Before(oldest) {
							oldKey, oldest = k, item.job.At
						}
					}
					flush(oldKey)
				}
				g = &messageGroup{job: j}
				groups[key] = g
			} else {
				g.job.Text = strings.Join([]string{g.job.Text, j.Text}, "\n")
			}
			g.last = j.At
			g.lines++
			if g.lines >= groupMaxLines || len(g.job.Text) >= groupMaxBytes {
				flush(key)
			}
		case <-tick.C:
			now := time.Now()
			for key, g := range groups {
				if now.Sub(g.last) >= groupIdle || now.Sub(g.job.At) >= groupMaxAge {
					flush(key)
				}
			}
		}
	}
}
