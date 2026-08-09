package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Println(version)
		os.Exit(0)
	}

	logger := log.New(os.Stdout, "botparty-streamer ", log.LstdFlags|log.Lmicroseconds)

	cfg, err := LoadConfig()
	if err != nil {
		logger.Fatalf("invalid config: %v", err)
	}

	var provider TokenProvider
	if cfg.StaticToken != "" {
		provider = StaticTokenProvider{Token: cfg.StaticToken}
		logger.Printf("using static LiveKit token from LK_TOKEN")
	} else if cfg.TokenFile != "" {
		provider = FileTokenProvider{Path: cfg.TokenFile}
		logger.Printf("using static LiveKit token from TOKEN_FILE")
	} else {
		provider = HTTPTokenProvider{
			Endpoint:     cfg.TokenEndpoint,
			AuthBearer:   cfg.TokenAuthBearer,
			DeviceID:     cfg.DeviceID,
			DeviceSecret: cfg.DeviceSecret,
			Room:         cfg.RoomName,
			Identity:     cfg.Identity,
			Name:         cfg.ParticipantName,
			TTLSeconds:   cfg.TokenTTLSeconds,
			Timeout:      cfg.TokenTimeout,
		}
		logger.Printf("using token endpoint: %s", cfg.TokenEndpoint)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer cancel()

	stats := loadRuntimeStatsSnapshot(cfg.MetricsStateFile, logger)
	frameCh := make(chan EncodedFrame, cfg.FrameChanSize)
	spspps := &spsPPSGlobalCache{}

	go runInputServer(ctx, cfg, frameCh, stats, spspps, logger)
	go runMetricsLogger(ctx, cfg, frameCh, stats, logger)
	if cfg.HealthAddr != "" {
		go runHealthServer(ctx, cfg.HealthAddr, cfg.HealthAuthToken, stats, frameCh, logger)
	}

	attempt := 0
	waitForKeyframe := true

	for {
		if ctx.Err() != nil {
			logger.Printf("shutdown requested")
			return
		}

		stats.connectAttempts.Add(1)
		token, err := provider.Fetch(ctx)
		if err != nil {
			attempt++
			backoff := reconnectBackoff(cfg.ReconnectMin, cfg.ReconnectMax, attempt)
			logger.Printf("token fetch failed: %v (retry in %s)", err, backoff)
			waitOrDone(ctx, backoff)
			continue
		}

		session, err := ConnectLiveKit(cfg, token, logger)
		if err != nil {
			attempt++
			backoff := reconnectBackoff(cfg.ReconnectMin, cfg.ReconnectMax, attempt)
			logger.Printf("livekit connect failed: %v (retry in %s)", err, backoff)
			waitOrDone(ctx, backoff)
			continue
		}

		attempt = 0
		drained := drainFrameQueue(frameCh)
		if drained > 0 {
			logger.Printf("drained %d stale frames before publish resume", drained)
		}

		err = pumpFrames(ctx, frameCh, session, cfg, waitForKeyframe, stats, logger)
		session.Close()
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Printf("session ended: %v", err)
		}

		waitForKeyframe = true
		stats.reconnects.Add(1)
		waitOrDone(ctx, cfg.ReconnectMin)
	}
}
