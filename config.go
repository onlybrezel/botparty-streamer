package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	LiveKitURL      string
	RoomName        string
	Identity        string
	ParticipantName string
	TrackName       string

	InputListenAddr  string
	FPS              int
	ReadTimeout      time.Duration
	FlushTimeout     time.Duration
	FrameChanSize    int
	MaxPublishStale  time.Duration
	AUMaxNALUs       int
	AUMaxBytes       int
	MetricsInterval  time.Duration
	AllowRemoteInput bool

	HealthAddr string

	StaticToken string

	TokenEndpoint   string
	TokenAuthBearer string
	DeviceID        string
	DeviceSecret    string
	TokenTimeout    time.Duration
	TokenTTLSeconds int

	ReconnectMin time.Duration
	ReconnectMax time.Duration
}

func LoadConfig() (Config, error) {
	cfg := Config{
		LiveKitURL:      strings.TrimSpace(os.Getenv("LK_URL")),
		RoomName:        strings.TrimSpace(os.Getenv("LK_ROOM")),
		Identity:        strings.TrimSpace(os.Getenv("LK_IDENTITY")),
		ParticipantName: strings.TrimSpace(os.Getenv("LK_NAME")),
		TrackName:       getenvDefault("LK_TRACK_NAME", "camera"),

		InputListenAddr:  getenvDefault("INPUT_LISTEN_ADDR", "127.0.0.1:5004"),
		FPS:              getenvInt("VIDEO_FPS", 30),
		ReadTimeout:      getenvDurationMs("INPUT_READ_TIMEOUT_MS", 500),
		FlushTimeout:     getenvDurationMs("FRAME_FLUSH_TIMEOUT_MS", 50),
		FrameChanSize:    getenvInt("FRAME_CHAN_SIZE", 4),
		MaxPublishStale:  getenvDurationMs("MAX_PUBLISH_STALE_MS", 250),
		AUMaxNALUs:       getenvInt("AU_MAX_NALUS", 64),
		AUMaxBytes:       getenvInt("AU_MAX_BYTES", 2097152),
		MetricsInterval:  getenvDurationMs("METRICS_INTERVAL_MS", 5000),
		AllowRemoteInput: getenvBool("ALLOW_REMOTE_INPUT", false),

		HealthAddr: getenvDefault("HEALTH_ADDR", "127.0.0.1:9090"),

		StaticToken:     strings.TrimSpace(os.Getenv("LK_TOKEN")),
		TokenEndpoint:   strings.TrimSpace(os.Getenv("TOKEN_ENDPOINT")),
		TokenAuthBearer: strings.TrimSpace(os.Getenv("TOKEN_AUTH_BEARER")),
		DeviceID:        strings.TrimSpace(os.Getenv("DEVICE_ID")),
		DeviceSecret:    strings.TrimSpace(os.Getenv("DEVICE_SECRET")),
		TokenTimeout:    getenvDurationMs("TOKEN_TIMEOUT_MS", 3000),
		TokenTTLSeconds: getenvInt("TOKEN_TTL_SECONDS", 300),
		ReconnectMin:    getenvDurationMs("RECONNECT_MIN_MS", 500),
		ReconnectMax:    getenvDurationMs("RECONNECT_MAX_MS", 8000),
	}

	if cfg.LiveKitURL == "" {
		return cfg, errors.New("LK_URL is required")
	}
	if cfg.RoomName == "" {
		return cfg, errors.New("LK_ROOM is required")
	}
	if cfg.Identity == "" {
		return cfg, errors.New("LK_IDENTITY is required")
	}
	if cfg.ParticipantName == "" {
		cfg.ParticipantName = cfg.Identity
	}
	if cfg.FPS <= 0 || cfg.FPS > 120 {
		return cfg, fmt.Errorf("VIDEO_FPS must be between 1 and 120, got %d", cfg.FPS)
	}
	if cfg.FrameChanSize < 1 || cfg.FrameChanSize > 32 {
		return cfg, errors.New("FRAME_CHAN_SIZE must be between 1 and 32 for low-latency mode")
	}
	if cfg.MaxPublishStale < 0 {
		return cfg, errors.New("MAX_PUBLISH_STALE_MS must be >= 0")
	}
	if cfg.AUMaxNALUs < 8 {
		return cfg, errors.New("AU_MAX_NALUS must be >= 8")
	}
	if cfg.AUMaxBytes < 128*1024 {
		return cfg, errors.New("AU_MAX_BYTES must be >= 131072")
	}
	if cfg.MetricsInterval < 500*time.Millisecond {
		return cfg, errors.New("METRICS_INTERVAL_MS must be >= 500")
	}
	if cfg.ReconnectMin <= 0 || cfg.ReconnectMax <= 0 {
		return cfg, errors.New("reconnect intervals must be > 0")
	}
	if cfg.ReconnectMin > cfg.ReconnectMax {
		return cfg, errors.New("RECONNECT_MIN_MS must be <= RECONNECT_MAX_MS")
	}

	host, _, err := net.SplitHostPort(cfg.InputListenAddr)
	if err != nil {
		return cfg, fmt.Errorf("INPUT_LISTEN_ADDR must be host:port, got %q", cfg.InputListenAddr)
	}
	if !cfg.AllowRemoteInput && !isLoopbackHost(host) {
		return cfg, fmt.Errorf("INPUT_LISTEN_ADDR=%q is not loopback; set ALLOW_REMOTE_INPUT=true to allow external binding", cfg.InputListenAddr)
	}

	if cfg.StaticToken == "" && cfg.TokenEndpoint == "" {
		return cfg, errors.New("either LK_TOKEN or TOKEN_ENDPOINT must be set")
	}

	return cfg, nil
}

func getenvDefault(key string, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func getenvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return i
}

func getenvDurationMs(key string, fallbackMs int) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return time.Duration(fallbackMs) * time.Millisecond
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return time.Duration(fallbackMs) * time.Millisecond
	}
	return time.Duration(i) * time.Millisecond
}

func getenvBool(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func isLoopbackHost(host string) bool {
	h := strings.Trim(host, "[]")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
