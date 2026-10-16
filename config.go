package main

import (
	"encoding/json"
	"fmt"
	"github.com/lrstanley/girc"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Rule struct {
	Threshold float64 `json:"threshold"`
	Points    int     `json:"points"`
}
type Penalty struct {
	Points   int    `json:"points"`
	Command  string `json:"command"`
	Duration string `json:"duration"`
}
type Config struct {
	IRC struct {
		Server              string   `json:"server"`
		TLS                 bool     `json:"tls"`
		ServerPassword      string   `json:"server_password"`
		Nick                string   `json:"nick"`
		User                string   `json:"user"`
		RealName            string   `json:"real_name"`
		OperName            string   `json:"oper_name"`
		OperPassword        string   `json:"oper_password"`
		Channels            []string `json:"channels"`
		OwnerAccounts       []string `json:"owner_accounts"`
		ReconnectMinSeconds int      `json:"reconnect_min_seconds"`
		ReconnectMaxSeconds int      `json:"reconnect_max_seconds"`
	} `json:"irc"`
	OpenAI struct {
		APIKey         string `json:"api_key"`
		Model          string `json:"model"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	} `json:"openai"`
	Moderation struct {
		DryRun                bool            `json:"dry_run"`
		QueueSize             int             `json:"queue_size"`
		MaxMessageAgeSeconds  int             `json:"max_message_age_seconds"`
		ScoreWindowHours      int             `json:"score_window_hours"`
		ActionCooldownSeconds int             `json:"action_cooldown_seconds"`
		StateFile             string          `json:"state_file"`
		Reason                string          `json:"reason"`
		Rules                 map[string]Rule `json:"rules"`
		Penalties             []Penalty       `json:"penalties"`
	} `json:"moderation"`
}

func atom(s string) bool     { return s != "" && !strings.ContainsAny(s, " \t\r\n\x00:,") }
func safeText(s string) bool { return !strings.ContainsAny(s, "\r\n\x00") }

func loadConfig(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return c, fmt.Errorf("config sonunda ek veri")
	}
	return c, c.validate()
}
func (c *Config) validate() error {
	host, portText, err := net.SplitHostPort(c.IRC.Server)
	port, portErr := strconv.Atoi(portText)
	if err != nil || host == "" || portErr != nil || port < 1 || port > 65535 {
		return fmt.Errorf("irc.server host:port olmalı")
	}
	if !girc.IsValidNick(c.IRC.Nick) || !girc.IsValidUser(c.IRC.User) {
		return fmt.Errorf("geçersiz IRC nick/user")
	}
	for _, s := range []string{c.IRC.Nick, c.IRC.User, c.IRC.OperName, c.IRC.OperPassword} {
		if !atom(s) {
			return fmt.Errorf("nick/user/oper bilgileri eksik veya geçersiz")
		}
	}
	if !safeText(c.IRC.RealName) || !safeText(c.IRC.ServerPassword) {
		return fmt.Errorf("geçersiz IRC metni")
	}
	if len(c.IRC.Channels) == 0 || len(c.IRC.OwnerAccounts) == 0 {
		return fmt.Errorf("channels ve owner_accounts boş olamaz")
	}
	for _, s := range c.IRC.Channels {
		if !atom(s) || !strings.HasPrefix(s, "#") {
			return fmt.Errorf("geçersiz kanal")
		}
	}
	for _, s := range c.IRC.OwnerAccounts {
		if !atom(s) || s == "*" {
			return fmt.Errorf("geçersiz sahip hesabı")
		}
	}
	if c.OpenAI.APIKey == "" || !safeText(c.OpenAI.APIKey) {
		return fmt.Errorf("openai.api_key gerekli")
	}
	if c.OpenAI.Model == "" {
		c.OpenAI.Model = "omni-moderation-latest"
	}
	if c.OpenAI.TimeoutSeconds == 0 {
		c.OpenAI.TimeoutSeconds = 15
	}
	if c.IRC.ReconnectMinSeconds == 0 {
		c.IRC.ReconnectMinSeconds = 3
	}
	if c.IRC.ReconnectMaxSeconds == 0 {
		c.IRC.ReconnectMaxSeconds = 60
	}
	if c.IRC.ReconnectMinSeconds < 1 || c.IRC.ReconnectMaxSeconds < c.IRC.ReconnectMinSeconds {
		return fmt.Errorf("geçersiz reconnect süreleri")
	}
	m := &c.Moderation
	if m.QueueSize < 1 || m.QueueSize > 10000 || m.MaxMessageAgeSeconds < 1 || m.ScoreWindowHours < 1 || m.ScoreWindowHours > 8760 || m.ActionCooldownSeconds < 1 || c.OpenAI.TimeoutSeconds < 1 || c.OpenAI.TimeoutSeconds > 120 {
		return fmt.Errorf("geçersiz süre veya kuyruk ayarı")
	}
	if m.StateFile == "" || !safeText(m.Reason) || m.Reason == "" || len(m.Reason) > 180 {
		return fmt.Errorf("state_file ve kısa reason gerekli")
	}
	allowed := map[string]bool{"harassment": true, "harassment/threatening": true, "hate": true, "hate/threatening": true, "violence": true, "violence/graphic": true}
	if len(m.Rules) == 0 || len(m.Penalties) == 0 {
		return fmt.Errorf("rules ve penalties gerekli")
	}
	for k, r := range m.Rules {
		if !allowed[k] || r.Threshold <= 0 || r.Threshold > 1 || r.Points < 1 || r.Points > 10000 {
			return fmt.Errorf("geçersiz kural: %s", k)
		}
	}
	sort.Slice(m.Penalties, func(i, j int) bool { return m.Penalties[i].Points < m.Penalties[j].Points })
	for i, p := range m.Penalties {
		if p.Points < 1 || (i > 0 && p.Points == m.Penalties[i-1].Points) || (p.Command != "KLINE" && p.Command != "ZLINE") {
			return fmt.Errorf("geçersiz ceza")
		}
		if p.Duration != "0" {
			v, err := time.ParseDuration(p.Duration)
			if err != nil || v < time.Second || v > 365*24*time.Hour || v%time.Second != 0 {
				return fmt.Errorf("duration 0 veya 30m/24h gibi olmalı")
			}
		}
	}
	return nil
}
