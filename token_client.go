package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

type TokenProvider interface {
	Fetch(ctx context.Context) (string, error)
}

type StaticTokenProvider struct {
	Token string
}

func (p StaticTokenProvider) Fetch(_ context.Context) (string, error) {
	if strings.TrimSpace(p.Token) == "" {
		return "", fmt.Errorf("static token is empty")
	}
	return p.Token, nil
}

type FileTokenProvider struct {
	Path string
}

func (p FileTokenProvider) Fetch(_ context.Context) (string, error) {
	path := strings.TrimSpace(p.Path)
	if path == "" {
		return "", fmt.Errorf("token file path is empty")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read token file: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("token file is empty")
	}
	return token, nil
}

type HTTPTokenProvider struct {
	Endpoint     string
	AuthBearer   string
	DeviceID     string
	DeviceSecret string
	Room         string
	Identity     string
	Name         string
	TTLSeconds   int
	Timeout      time.Duration
}

type tokenRequest struct {
	Room       string `json:"room"`
	Identity   string `json:"identity"`
	Name       string `json:"name"`
	TTLSeconds int    `json:"ttlSeconds"`
}

type tokenResponse struct {
	Token string `json:"token"`
}

func (p HTTPTokenProvider) Fetch(ctx context.Context) (string, error) {
	// Apply ±10% jitter to TTL so multiple streamer instances don't all
	// request token refreshes at exactly the same time.
	ttl := p.TTLSeconds
	if ttl > 0 {
		jitter := int(float64(ttl) * 0.1 * (2*rand.Float64() - 1))
		ttl += jitter
		if ttl < 30 {
			ttl = 30
		}
	}
	body, err := json.Marshal(tokenRequest{
		Room:       p.Room,
		Identity:   p.Identity,
		Name:       p.Name,
		TTLSeconds: ttl,
	})
	if err != nil {
		return "", fmt.Errorf("marshal token request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, p.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.AuthBearer != "" {
		req.Header.Set("Authorization", "Bearer "+p.AuthBearer)
	}
	if p.DeviceID != "" {
		req.Header.Set("X-Device-Id", p.DeviceID)
	}
	if p.DeviceSecret != "" {
		req.Header.Set("X-Device-Secret", p.DeviceSecret)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("token endpoint returned status %d", resp.StatusCode)
	}

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	tr.Token = strings.TrimSpace(tr.Token)
	if tr.Token == "" {
		return "", fmt.Errorf("token response did not contain token")
	}
	return tr.Token, nil
}
