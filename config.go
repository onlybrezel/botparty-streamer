package main

import (
"errors"
"fmt"
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

InputListenAddr string
FPS             int
ReadTimeout     time.Duration
FlushTimeout    time.Duration
FrameChanSize   int

StaticToken string

TokenEndpoint    string
TokenAuthBearer  string
DeviceID         string
DeviceSecret     string
TokenTimeout     time.Duration
TokenTTLSeconds  int

ReconnectMin time.Duration
ReconnectMax time.Duration
}

func LoadConfig() (Config, error) {
cfg := Config{
LiveKitURL:       strings.TrimSpace(os.Getenv("LK_URL")),
RoomName:         strings.TrimSpace(os.Getenv("LK_ROOM")),
Identity:         strings.TrimSpace(os.Getenv("LK_IDENTITY")),
ParticipantName:  strings.TrimSpace(os.Getenv("LK_NAME")),
TrackName:        getenvDefault("LK_TRACK_NAME", "camera"),
InputListenAddr:  getenvDefault("INPUT_LISTEN_ADDR", "127.0.0.1:5004"),
FPS:              getenvInt("VIDEO_FPS", 30),
ReadTimeout:      getenvDurationMs("INPUT_READ_TIMEOUT_MS", 1200),
FlushTimeout:     getenvDurationMs("FRAME_FLUSH_TIMEOUT_MS", 80),
FrameChanSize:    getenvInt("FRAME_CHAN_SIZE", 240),
StaticToken:      strings.TrimSpace(os.Getenv("LK_TOKEN")),
TokenEndpoint:    strings.TrimSpace(os.Getenv("TOKEN_ENDPOINT")),
TokenAuthBearer:  strings.TrimSpace(os.Getenv("TOKEN_AUTH_BEARER")),
DeviceID:         strings.TrimSpace(os.Getenv("DEVICE_ID")),
DeviceSecret:     strings.TrimSpace(os.Getenv("DEVICE_SECRET")),
TokenTimeout:     getenvDurationMs("TOKEN_TIMEOUT_MS", 3000),
TokenTTLSeconds:  getenvInt("TOKEN_TTL_SECONDS", 300),
ReconnectMin:     getenvDurationMs("RECONNECT_MIN_MS", 500),
ReconnectMax:     getenvDurationMs("RECONNECT_MAX_MS", 8000),
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
if cfg.FrameChanSize < 16 {
return cfg, errors.New("FRAME_CHAN_SIZE must be >= 16")
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
