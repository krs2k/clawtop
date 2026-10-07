package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Claude subscription limits, read from the same endpoint Claude Code's /usage uses.
// The OAuth token is re-read from ~/.claude/.credentials.json on every poll,
// so we pick up refreshes done by Claude Code itself.

type usageWindow struct {
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resets_at"`
}

type usageResponse struct {
	FiveHour       *usageWindow `json:"five_hour"`
	SevenDay       *usageWindow `json:"seven_day"`
	SevenDayOpus   *usageWindow `json:"seven_day_opus"`
	SevenDaySonnet *usageWindow `json:"seven_day_sonnet"`
}

type claudeLimit struct {
	Name     string
	Percent  float64
	ResetsAt time.Time
}

type claudeMsg struct {
	limits []claudeLimit
	err    error
}

func readToken() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(home, ".claude")
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".credentials.json"))
	if err != nil {
		return "", fmt.Errorf("brak credentials: %w", err)
	}
	var creds struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &creds); err != nil {
		return "", err
	}
	if creds.ClaudeAiOauth.AccessToken == "" {
		return "", fmt.Errorf("brak tokenu OAuth (zaloguj się w Claude Code)")
	}
	if exp := creds.ClaudeAiOauth.ExpiresAt; exp > 0 && time.Now().UnixMilli() > exp {
		return "", fmt.Errorf("token wygasł — uruchom Claude Code, żeby go odświeżyć")
	}
	return creds.ClaudeAiOauth.AccessToken, nil
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

func fetchClaudeUsage() claudeMsg {
	tok, err := readToken()
	if err != nil {
		return claudeMsg{err: err}
	}
	req, _ := http.NewRequest("GET", "https://api.anthropic.com/api/oauth/usage", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := httpClient.Do(req)
	if err != nil {
		return claudeMsg{err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return claudeMsg{err: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	var u usageResponse
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return claudeMsg{err: err}
	}
	var out []claudeLimit
	add := func(name string, w *usageWindow) {
		if w != nil {
			out = append(out, claudeLimit{name, w.Utilization, w.ResetsAt})
		}
	}
	add("Claude 5h", u.FiveHour)
	add("Claude 7d", u.SevenDay)
	add("Opus 7d", u.SevenDayOpus)
	add("Sonnet 7d", u.SevenDaySonnet)
	return claudeMsg{limits: out}
}
