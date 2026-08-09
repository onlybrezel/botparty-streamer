package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config defines runtime options for the streamer process.
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
	MetricsStateFile string
	AllowRemoteInput bool

	HealthAddr        string
	HealthAuthToken   string
	AllowRemoteHealth bool

	StaticToken string
	TokenFile   string

	TokenEndpoint   string
	TokenAuthBearer string
	DeviceID        string
	DeviceSecret    string
	TokenTimeout    time.Duration
	TokenTTLSeconds int

	ReconnectMin time.Duration
	ReconnectMax time.Duration
}

// LoadConfig loads environment-based configuration and validates bounds.
func LoadConfig() (Config, error) {
	configFile := strings.TrimSpace(os.Getenv("CONFIG_FILE"))
	if configFile != "" {
		if err := loadEnvFile(configFile); err != nil {
			return Config{}, fmt.Errorf("load CONFIG_FILE: %w", err)
		}
	}

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
		MetricsStateFile: strings.TrimSpace(os.Getenv("METRICS_STATE_FILE")),
		AllowRemoteInput: getenvBool("ALLOW_REMOTE_INPUT", false),

		HealthAddr:        getenvDefault("HEALTH_ADDR", "127.0.0.1:9090"),
		HealthAuthToken:   strings.TrimSpace(os.Getenv("HEALTH_AUTH_TOKEN")),
		AllowRemoteHealth: getenvBool("ALLOW_REMOTE_HEALTH", false),

		StaticToken:     strings.TrimSpace(os.Getenv("LK_TOKEN")),
		TokenFile:       strings.TrimSpace(os.Getenv("TOKEN_FILE")),
		TokenEndpoint:   strings.TrimSpace(os.Getenv("TOKEN_ENDPOINT")),
		TokenAuthBearer: strings.TrimSpace(os.Getenv("TOKEN_AUTH_BEARER")),
		DeviceID:        strings.TrimSpace(os.Getenv("DEVICE_ID")),
		DeviceSecret:    strings.TrimSpace(os.Getenv("DEVICE_SECRET")),
		TokenTimeout:    getenvDurationMs("TOKEN_TIMEOUT_MS", 3000),
		TokenTTLSeconds: getenvInt("TOKEN_TTL_SECONDS", 900),
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

	healthHost, _, err := net.SplitHostPort(cfg.HealthAddr)
	if err != nil {
		return cfg, fmt.Errorf("HEALTH_ADDR must be host:port, got %q", cfg.HealthAddr)
	}
	if !cfg.AllowRemoteHealth && !isLoopbackHost(healthHost) {
		return cfg, fmt.Errorf("HEALTH_ADDR=%q is not loopback; set ALLOW_REMOTE_HEALTH=true to allow external binding", cfg.HealthAddr)
	}

	if cfg.TokenTTLSeconds < 60 {
		return cfg, errors.New("TOKEN_TTL_SECONDS must be >= 60")
	}

	if cfg.StaticToken == "" && cfg.TokenEndpoint == "" && cfg.TokenFile == "" {
		return cfg, errors.New("one of LK_TOKEN, TOKEN_FILE, or TOKEN_ENDPOINT must be set")
	}

	return cfg, nil
}

func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		if len(value) >= 2 {
			if (value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"') {
				value = value[1 : len(value)-1]
			}
		}
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}

	return scanner.Err()
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
