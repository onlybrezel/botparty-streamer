package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func withEnv(t *testing.T, values map[string]string) {
	t.Helper()
	for key, value := range values {
		original, hadOriginal := os.LookupEnv(key)
		if value == "" {
			_ = os.Unsetenv(key)
		} else {
			_ = os.Setenv(key, value)
		}
		t.Cleanup(func() {
			if hadOriginal {
				_ = os.Setenv(key, original)
				return
			}
			_ = os.Unsetenv(key)
		})
	}
}

func TestLoadConfig_UsesDefaultsWithStaticToken(t *testing.T) {
	withEnv(t, map[string]string{
		"CONFIG_FILE":          "",
		"LK_URL":               "wss://livekit.example.test",
		"LK_ROOM":              "robot-room",
		"LK_IDENTITY":          "robot-1",
		"LK_NAME":              "",
		"LK_TOKEN":             "static-token",
		"TOKEN_FILE":           "",
		"TOKEN_ENDPOINT":       "",
		"ALLOW_REMOTE_INPUT":   "false",
		"ALLOW_REMOTE_HEALTH":  "false",
		"INPUT_LISTEN_ADDR":    "127.0.0.1:5004",
		"HEALTH_ADDR":          "127.0.0.1:9090",
		"RECONNECT_MIN_MS":     "500",
		"RECONNECT_MAX_MS":     "8000",
		"TOKEN_TTL_SECONDS":    "900",
		"METRICS_INTERVAL_MS":  "5000",
		"FRAME_CHAN_SIZE":      "4",
		"MAX_PUBLISH_STALE_MS": "250",
	})

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if cfg.ParticipantName != "robot-1" {
		t.Fatalf("ParticipantName = %q, want robot-1", cfg.ParticipantName)
	}
	if cfg.TrackName != "camera" {
		t.Fatalf("TrackName = %q, want camera", cfg.TrackName)
	}
	if cfg.InputListenAddr != "127.0.0.1:5004" {
		t.Fatalf("InputListenAddr = %q", cfg.InputListenAddr)
	}
	if cfg.HealthAddr != "127.0.0.1:9090" {
		t.Fatalf("HealthAddr = %q", cfg.HealthAddr)
	}
	if cfg.ReadTimeout != 500*time.Millisecond {
		t.Fatalf("ReadTimeout = %s, want 500ms", cfg.ReadTimeout)
	}
	if cfg.StaticToken != "static-token" {
		t.Fatalf("StaticToken = %q", cfg.StaticToken)
	}
	if cfg.AllowRemoteInput {
		t.Fatal("AllowRemoteInput should default to false")
	}
}

func TestLoadConfig_RejectsNonLoopbackInputWithoutOverride(t *testing.T) {
	withEnv(t, map[string]string{
		"CONFIG_FILE":         "",
		"LK_URL":              "wss://livekit.example.test",
		"LK_ROOM":             "robot-room",
		"LK_IDENTITY":         "robot-1",
		"LK_TOKEN":            "static-token",
		"INPUT_LISTEN_ADDR":   "0.0.0.0:5004",
		"ALLOW_REMOTE_INPUT":  "false",
		"HEALTH_ADDR":         "127.0.0.1:9090",
		"ALLOW_REMOTE_HEALTH": "false",
	})

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected non-loopback input binding to fail")
	}
	if !strings.Contains(err.Error(), "ALLOW_REMOTE_INPUT=true") {
		t.Fatalf("expected remote input hint, got %v", err)
	}
}

func TestLoadConfig_RejectsReconnectMinGreaterThanMax(t *testing.T) {
	withEnv(t, map[string]string{
		"CONFIG_FILE":         "",
		"LK_URL":              "wss://livekit.example.test",
		"LK_ROOM":             "robot-room",
		"LK_IDENTITY":         "robot-1",
		"LK_TOKEN":            "static-token",
		"INPUT_LISTEN_ADDR":   "127.0.0.1:5004",
		"ALLOW_REMOTE_INPUT":  "false",
		"HEALTH_ADDR":         "127.0.0.1:9090",
		"ALLOW_REMOTE_HEALTH": "false",
		"RECONNECT_MIN_MS":    "9000",
		"RECONNECT_MAX_MS":    "1000",
	})

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected reconnect interval validation to fail")
	}
	if !strings.Contains(err.Error(), "RECONNECT_MIN_MS must be <= RECONNECT_MAX_MS") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfig_AcceptsTokenEndpointMode(t *testing.T) {
	withEnv(t, map[string]string{
		"CONFIG_FILE":         "",
		"LK_URL":              "wss://livekit.example.test",
		"LK_ROOM":             "robot-room",
		"LK_IDENTITY":         "robot-1",
		"LK_TOKEN":            "",
		"TOKEN_FILE":          "",
		"TOKEN_ENDPOINT":      "https://tokens.example.test/livekit",
		"INPUT_LISTEN_ADDR":   "127.0.0.1:5004",
		"ALLOW_REMOTE_INPUT":  "false",
		"HEALTH_ADDR":         "127.0.0.1:9090",
		"ALLOW_REMOTE_HEALTH": "false",
	})

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if cfg.TokenEndpoint != "https://tokens.example.test/livekit" {
		t.Fatalf("TokenEndpoint = %q", cfg.TokenEndpoint)
	}
}
