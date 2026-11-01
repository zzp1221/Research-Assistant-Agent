// Package onboarding implements the first-run setup state machine.
package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"research-assistant-desktop/settings"
)

// Credentials are the user-supplied LLM settings.
type Credentials struct {
	BaseURL string
	Model   string
	APIKey  string
}

// Validate checks required credential fields and URL shape.
func Validate(creds Credentials) error {
	base := strings.TrimSpace(creds.BaseURL)
	if base == "" {
		return fmt.Errorf("base URL is required")
	}
	parsed, err := url.Parse(base)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return fmt.Errorf("base URL must be a valid http(s) URL")
	}
	if strings.TrimSpace(creds.Model) == "" {
		return fmt.Errorf("model is required")
	}
	if strings.TrimSpace(creds.APIKey) == "" {
		return fmt.Errorf("API key is required")
	}
	return nil
}

// Client tests an LLM connection with a minimal completion request.
type Client struct {
	HTTPClient *http.Client
}

// TestConnection calls {base}/chat/completions with a one-token prompt.
func (c *Client) TestConnection(ctx context.Context, creds Credentials) error {
	if err := Validate(creds); err != nil {
		return err
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	payload := map[string]any{
		"model":      creds.Model,
		"max_tokens": 1,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(strings.TrimSpace(creds.BaseURL), "/") + "/chat/completions"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(creds.APIKey))
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 32<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("connection test returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// IsFirstRun reports whether the settings file does not exist yet.
func IsFirstRun(settingsPath string) bool {
	if strings.TrimSpace(settingsPath) == "" {
		return true
	}
	info, err := os.Stat(settingsPath)
	return err != nil || info.IsDir()
}

// Complete persists the credentials and marks onboarding finished.
func Complete(settingsPath string, creds Credentials, cfg settings.Settings) error {
	if err := Validate(creds); err != nil {
		return err
	}
	cfg.LLM.BaseURL = strings.TrimSpace(creds.BaseURL)
	cfg.LLM.Model = strings.TrimSpace(creds.Model)
	cfg.OnboardingCompleted = true
	return settings.Save(settingsPath, cfg)
}
