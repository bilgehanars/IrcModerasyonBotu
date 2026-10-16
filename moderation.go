package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Moderator struct {
	Client                        *http.Client
	Key, Model, URL, Provider     string
	AllowUnsupportedLanguage      bool
	RequestsPerMinute, MaxRetries int
	mu                            sync.Mutex
	next                          time.Time
}

func newModerator(c Config) *Moderator {
	timeout := c.OpenAI.TimeoutSeconds
	key := c.OpenAI.APIKey
	endpoint := "https://api.openai.com/v1/moderations"
	if c.Provider == "google" {
		timeout = c.Google.TimeoutSeconds
		key = c.Google.APIKey
		endpoint = "https://language.googleapis.com/v2/documents:moderateText"
	}
	return &Moderator{Client: &http.Client{Timeout: time.Duration(timeout) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, Key: key, Model: c.OpenAI.Model, URL: endpoint, Provider: c.Provider, AllowUnsupportedLanguage: c.Google.AllowUnsupportedLanguage, RequestsPerMinute: c.Moderation.RequestsPerMinute, MaxRetries: c.Moderation.MaxRetries}
}
func waitContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (m *Moderator) wait(ctx context.Context) error {
	if m.RequestsPerMinute <= 0 {
		return ctx.Err()
	}
	m.mu.Lock()
	now := time.Now()
	at := m.next
	if at.Before(now) {
		at = now
	}
	m.next = at.Add(time.Minute / time.Duration(m.RequestsPerMinute))
	m.mu.Unlock()
	return waitContext(ctx, time.Until(at))
}
func retryDelay(header string, attempt int) time.Duration {
	d := time.Duration(1<<attempt) * time.Second
	if n, e := strconv.Atoi(header); e == nil && n >= 0 {
		if n > 120 {
			n = 120
		}
		d = time.Duration(n) * time.Second
	} else if at, e := http.ParseTime(header); e == nil {
		d = time.Until(at)
	}
	if d < 0 {
		return 0
	}
	if d > 2*time.Minute {
		return 2 * time.Minute
	}
	return d
}
func (m *Moderator) Check(ctx context.Context, text string) (map[string]float64, error) {
	results, err := m.CheckBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return results[0], nil
}

// CheckBatch preserves the input order. Google accepts one document per request.
func (m *Moderator) CheckBatch(ctx context.Context, texts []string) ([]map[string]float64, error) {
	return m.checkBatch(ctx, texts, nil)
}

// permit is checked after rate limiting and before every network attempt so
// revoked policy cannot sit in a pending request through a long wait/retry.
func (m *Moderator) checkBatch(ctx context.Context, texts []string, permit func() bool) ([]map[string]float64, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("boş moderation grubu")
	}
	if m.Provider == "google" && len(texts) != 1 {
		return nil, fmt.Errorf("Google tek belge kabul eder")
	}
	var payload any = map[string]any{"model": m.Model, "input": texts}
	if m.Provider == "google" {
		payload = map[string]any{"document": map[string]string{"type": "PLAIN_TEXT", "content": texts[0]}, "modelVersion": "MODEL_VERSION_1"}
	}
	body, _ := json.Marshal(payload)
	for attempt := 0; attempt <= m.MaxRetries; attempt++ {
		if err := m.wait(ctx); err != nil {
			return nil, err
		}
		if permit != nil && !permit() {
			return nil, fmt.Errorf("moderation grubu gönderilmeden geçersizleşti")
		}
		req, err := http.NewRequestWithContext(ctx, "POST", m.URL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("moderation isteği oluşturulamadı")
		}
		if m.Provider == "google" {
			req.Header.Set("X-Goog-Api-Key", m.Key)
		} else {
			req.Header.Set("Authorization", "Bearer "+m.Key)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := m.Client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt < m.MaxRetries {
				if err = waitContext(ctx, retryDelay("", attempt)); err == nil {
					continue
				}
			}
			return nil, fmt.Errorf("moderation bağlantısı başarısız")
		}
		if resp.StatusCode != http.StatusOK {
			code := resp.StatusCode
			after := resp.Header.Get("Retry-After")
			if code == 429 {
				m.mu.Lock()
				until := time.Now().Add(retryDelay(after, attempt))
				if until.After(m.next) {
					m.next = until
				}
				m.mu.Unlock()
			}
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if (code == 429 || code >= 500) && attempt < m.MaxRetries {
				if err = waitContext(ctx, retryDelay(after, attempt)); err != nil {
					return nil, err
				}
				continue
			}
			return nil, fmt.Errorf("moderation HTTP %d", code)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		resp.Body.Close()
		if err != nil || len(data) > 1<<20 {
			return nil, fmt.Errorf("moderation yanıtı okunamadı veya fazla büyük")
		}
		return m.decodeBatch(data, len(texts))
	}
	return nil, fmt.Errorf("moderation denemeleri tükendi")
}
func (m *Moderator) decode(data []byte) (map[string]float64, error) {
	r, err := m.decodeBatch(data, 1)
	if err != nil {
		return nil, err
	}
	return r[0], nil
}

func (m *Moderator) decodeBatch(data []byte, count int) ([]map[string]float64, error) {
	scores := map[string]float64{}
	results := make([]map[string]float64, 0, count)
	if m.Provider == "google" {
		var r struct {
			Categories []struct {
				Name       string   `json:"name"`
				Confidence *float64 `json:"confidence"`
			} `json:"moderationCategories"`
			LanguageSupported bool `json:"languageSupported"`
		}
		if json.Unmarshal(data, &r) != nil {
			return nil, fmt.Errorf("Google yanıtı çözülemedi")
		}
		if !r.LanguageSupported && !m.AllowUnsupportedLanguage {
			return nil, fmt.Errorf("Google bu dili doğrulanmış olarak desteklemiyor; kontrol atlandı")
		}
		for _, v := range r.Categories {
			if v.Name == "" {
				return nil, fmt.Errorf("Google kategori sonucu eksik")
			}
			if _, ok := scores[v.Name]; ok {
				return nil, fmt.Errorf("yinelenen Google kategorisi")
			}
			scores[v.Name] = 0
			if v.Confidence != nil {
				scores[v.Name] = *v.Confidence
			}
		}
	} else {
		var r struct {
			Results []struct {
				Scores map[string]float64 `json:"category_scores"`
			} `json:"results"`
		}
		if json.Unmarshal(data, &r) != nil || len(r.Results) != count {
			return nil, fmt.Errorf("OpenAI yanıtı çözülemedi")
		}
		for _, item := range r.Results {
			results = append(results, item.Scores)
		}
	}
	if m.Provider == "google" {
		results = append(results, scores)
	}
	for _, scores := range results {
		if len(scores) == 0 {
			return nil, fmt.Errorf("moderation sonucu eksik")
		}
		for k, v := range scores {
			if strings.TrimSpace(k) == "" || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
				return nil, fmt.Errorf("geçersiz moderation skoru")
			}
		}
	}
	return results, nil
}
func scoreMessage(rules map[string]Rule, scores map[string]float64) (int, string) {
	points, category := 0, ""
	for k, r := range rules {
		if s, ok := scores[k]; ok && s >= r.Threshold && (r.Points > points || (r.Points == points && k < category)) {
			points, category = r.Points, k
		}
	}
	return points, category
}
