package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Moderator struct {
	Client          *http.Client
	Key, Model, URL string
}

func newModerator(c Config) *Moderator {
	return &Moderator{Client: &http.Client{Timeout: time.Duration(c.OpenAI.TimeoutSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, Key: c.OpenAI.APIKey, Model: c.OpenAI.Model, URL: "https://api.openai.com/v1/moderations"}
}
func (m *Moderator) Check(ctx context.Context, text string) (map[string]float64, error) {
	body, _ := json.Marshal(map[string]string{"model": m.Model, "input": text})
	req, err := http.NewRequestWithContext(ctx, "POST", m.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("moderation bağlantısı başarısız")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("moderation HTTP %d", resp.StatusCode)
	}
	var result struct {
		Results []struct {
			Scores map[string]float64 `json:"category_scores"`
		} `json:"results"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("moderation yanıtı çözülemedi")
	}
	if len(result.Results) != 1 || len(result.Results[0].Scores) == 0 {
		return nil, fmt.Errorf("moderation sonucu eksik")
	}
	for _, s := range result.Results[0].Scores {
		if s < 0 || s > 1 {
			return nil, fmt.Errorf("geçersiz moderation skoru")
		}
	}
	return result.Results[0].Scores, nil
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
