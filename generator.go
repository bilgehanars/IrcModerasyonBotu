package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

//go:embed bilgehan.conf.example
var defaultConfigJSON []byte

func generateConfig(in io.Reader, out io.Writer, path string) error {
	if _, e := os.Stat(path); e == nil {
		return fmt.Errorf("%s zaten var; üzerine yazılmadı", path)
	} else if !os.IsNotExist(e) {
		return e
	}
	var c Config
	if e := json.Unmarshal(defaultConfigJSON, &c); e != nil {
		return e
	}
	prompt := func(label, def string, secret bool) (string, error) {
		if def != "" {
			fmt.Fprintf(out, "%s [%s]: ", label, def)
		} else {
			fmt.Fprintf(out, "%s: ", label)
		}
		var raw string
		if f, ok := in.(*os.File); ok && secret && term.IsTerminal(int(f.Fd())) {
			b, e := term.ReadPassword(int(f.Fd()))
			fmt.Fprintln(out)
			if e != nil {
				return "", e
			}
			raw = string(b)
		} else {
			v, e := readSetupLine(in)
			if e != nil {
				return "", fmt.Errorf("kurulum tamamlanmadı; dosya yazılmadı")
			}
			raw = v
		}
		v := strings.TrimSpace(raw)
		if v == "" {
			v = def
		}
		return v, nil
	}
	var e error
	fmt.Fprintln(out, "Bilgehan IRC Botu kurulum sihirbazı. Şifreler terminalde gizlenir. Çıkmak için Ctrl+C. Dosya yalnızca tüm bilgiler doğrulanırsa oluşturulur.")
	if c.Provider, e = prompt("Sağlayıcı (openai/google)", "openai", false); e != nil {
		return e
	}
	if c.Provider != "openai" && c.Provider != "google" {
		return fmt.Errorf("sağlayıcı openai veya google olmalı")
	}
	if c.IRC.Server, e = prompt("IRC sunucusu host:port", "irc.example.com:6697", false); e != nil {
		return e
	}
	if c.IRC.Nick, e = prompt("Bot nicki", "BilgehanBot", false); e != nil {
		return e
	}
	if c.IRC.OperName, e = prompt("OPER hesap adı", "", false); e != nil {
		return e
	}
	if c.IRC.OperPassword, e = prompt("OPER şifresi", "", true); e != nil {
		return e
	}
	if c.IRC.ServerPassword, e = prompt("Sunucu bağlantı şifresi (gerekmiyorsa Enter)", "", true); e != nil {
		return e
	}
	channels, e := prompt("Kanallar (virgülle ayır)", "#sohbet", false)
	if e != nil {
		return e
	}
	c.IRC.Channels = splitList(channels)
	owners, e := prompt("Master IRC hesap adları (nick değil, virgülle ayır)", "", false)
	if e != nil {
		return e
	}
	c.IRC.OwnerAccounts = splitList(owners)
	if c.Provider == "openai" {
		if c.OpenAI.APIKey, e = prompt("OpenAI API anahtarı", "", true); e != nil {
			return e
		}
		c.Google.APIKey = ""
	} else {
		fmt.Fprintln(out, "Google Cloud Natural Language API ve faturalandırma gerekir. Türkçe kalitesi Google tarafından doğrulanmış değil. Politika/tehdit kategorileri OpenAI ile birebir eşleşmez.")
		if c.Google.APIKey, e = prompt("Google Cloud Natural Language API anahtarı", "", true); e != nil {
			return e
		}
		c.OpenAI.APIKey = ""
		allow, e := prompt("Doğrulanmamış dillerde (Türkçe dahil) sonuç kullanılsın mı? true/false", "false", false)
		if e != nil {
			return e
		}
		if allow != "true" && allow != "false" {
			return fmt.Errorf("true/false gerekli")
		}
		c.Google.AllowUnsupportedLanguage = allow == "true"
		c.Moderation.Rules = map[string]Rule{"Toxic": {Threshold: .90, Points: 2}, "Insult": {Threshold: .90, Points: 2}, "Derogatory": {Threshold: .90, Points: 4}, "Violent": {Threshold: .95, Points: 3}}
	}
	if e = c.validate(); e != nil {
		return e
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	if e = enc.Encode(c); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".bilgehan-*.conf")
	if e != nil {
		return e
	}
	temp := f.Name()
	defer os.Remove(temp)
	defer f.Close()
	if _, e = f.Write(b.Bytes()); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	// A hard link publishes the complete file atomically and never replaces an existing path.
	if e = os.Link(temp, path); e != nil {
		return e
	}
	fmt.Fprintf(out, "%s oluşturuldu. TLS ve dry_run açık. -check ile doğrula; ilk çalıştırmada sonuçları incele.\n", path)
	return nil
}
func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Read exactly one line without reading ahead into a later hidden password prompt.
func readSetupLine(in io.Reader) (string, error) {
	var b bytes.Buffer
	var one [1]byte
	for b.Len() < 4096 {
		if _, e := io.ReadFull(in, one[:]); e != nil {
			return "", e
		}
		if one[0] == '\n' {
			return b.String(), nil
		}
		b.WriteByte(one[0])
	}
	return "", fmt.Errorf("girdi çok uzun")
}
