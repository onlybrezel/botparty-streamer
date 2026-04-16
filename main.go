package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

type runtimeStats struct {
	droppedQueue          uint64
	droppedStale          uint64
	droppedWaitKeyframe   uint64
	publishedFrames       uint64
	parseErrors           uint64
	connectAttempts       uint64
	reconnects            uint64
	inputConnections      uint64
	lastKeyframeUnixNanos int64
}

func (s *runtimeStats) markKeyframe(t time.Time) {
	atomic.StoreInt64(&s.lastKeyframeUnixNanos, t.UnixNano())
}

// spsPPSGlobalCache carries SPS/PPS NALUs across ffmpeg connection restarts so
// that new assemblers can hit the ground running without a fresh IDR sequence.
type spsPPSGlobalCache struct {
	mu  sync.Mutex
	sps []byte
	pps []byte
}

func (c *spsPPSGlobalCache) update(sps, pps []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(sps) > 0 {
		c.sps = append([]byte(nil), sps...)
	}
	if len(pps) > 0 {
		c.pps = append([]byte(nil), pps...)
	}
}

func (c *spsPPSGlobalCache) get() ([]byte, []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.sps...), append([]byte(nil), c.pps...)
}

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
		go runHealthServer(ctx, cfg.HealthAddr, stats, frameCh, logger)
	}

	attempt := 0
	waitForKeyframe := true

	for {
		if ctx.Err() != nil {
			logger.Printf("shutdown requested")
			return
		}

		atomic.AddUint64(&stats.connectAttempts, 1)
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
		atomic.AddUint64(&stats.reconnects, 1)
		waitOrDone(ctx, cfg.ReconnectMin)
	}
}

func runInputServer(ctx context.Context, cfg Config, frameCh chan EncodedFrame, stats *runtimeStats, spspps *spsPPSGlobalCache, logger *log.Logger) {
	ln, err := net.Listen("tcp", cfg.InputListenAddr)
	if err != nil {
		logger.Fatalf("failed to listen on %s: %v", cfg.InputListenAddr, err)
	}
	defer ln.Close()

	logger.Printf("waiting for ffmpeg h264 input on tcp://%s", cfg.InputListenAddr)

	for {
		if tcpLn, ok := ln.(*net.TCPListener); ok {
			_ = tcpLn.SetDeadline(time.Now().Add(1 * time.Second))
		}

		conn, err := ln.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			if ctx.Err() != nil {
				return
			}
			logger.Printf("accept failed: %v", err)
			continue
		}

		atomic.AddUint64(&stats.inputConnections, 1)
		logger.Printf("ffmpeg connected from %s", conn.RemoteAddr())
		handleInputConnection(ctx, conn, cfg, frameCh, stats, spspps, logger)
		logger.Printf("ffmpeg disconnected")
	}
}

func handleInputConnection(ctx context.Context, conn net.Conn, cfg Config, frameCh chan EncodedFrame, stats *runtimeStats, spspps *spsPPSGlobalCache, logger *log.Logger) {
	defer conn.Close()

	reader := NewAnnexBReader(conn)
	assembler := NewAccessUnitAssembler(cfg.AUMaxNALUs, cfg.AUMaxBytes)
	// Seed SPS/PPS from a previous ffmpeg session so the first keyframe is
	// decodable even if ffmpeg restarted without sending fresh parameter sets.
	{
		cachedSPS, cachedPPS := spspps.get()
		assembler.SeedSPSPPS(cachedSPS, cachedPPS)
	}
	frameDuration := time.Second / time.Duration(cfg.FPS)

	for {
		if ctx.Err() != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(cfg.ReadTimeout))

		nalu, err := reader.NextNALU()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if frame, key, ok, reason := assembler.FlushIfStale(cfg.FlushTimeout); ok {
					sendFrame(frameCh, EncodedFrame{Data: frame, Duration: frameDuration, KeyFrame: key, CreatedAt: time.Now()}, stats)
					if key {
						stats.markKeyframe(time.Now())
					}
					logger.Printf("flushed access unit reason=%s bytes=%d key=%v", reason, len(frame), key)
				}
				continue
			}
			if errors.Is(err, io.EOF) {
				if frame, key, ok, reason := assembler.FlushFinal(); ok {
					sendFrame(frameCh, EncodedFrame{Data: frame, Duration: frameDuration, KeyFrame: key, CreatedAt: time.Now()}, stats)
					if key {
						stats.markKeyframe(time.Now())
					}
					logger.Printf("flushed access unit reason=%s bytes=%d key=%v", reason, len(frame), key)
				}
				return
			}
			atomic.AddUint64(&stats.parseErrors, 1)
			logger.Printf("input parse error: %v", err)
			return
		}

		if frame, key, ok, reason := assembler.Push(nalu); ok {
			sendFrame(frameCh, EncodedFrame{Data: frame, Duration: frameDuration, KeyFrame: key, CreatedAt: time.Now()}, stats)
			if key {
				stats.markKeyframe(time.Now())
				// Keep the global SPS/PPS cache fresh so a new assembler after a
				// reconnect can inject them into the first keyframe immediately.
				sps, pps := assembler.LastSPSPPS()
				spspps.update(sps, pps)
			}
			if reason == "max_au_bytes" || reason == "max_au_nalus" || reason == "uncertain_slice_boundary" {
				logger.Printf("guard flush reason=%s bytes=%d key=%v", reason, len(frame), key)
			}
		}
	}
}

func sendFrame(frameCh chan EncodedFrame, frame EncodedFrame, stats *runtimeStats) {
	for {
		select {
		case frameCh <- frame:
			return
		default:
		}

		select {
		case <-frameCh:
			atomic.AddUint64(&stats.droppedQueue, 1)
		default:
		}
	}
}

func drainFrameQueue(frameCh chan EncodedFrame) int {
	drained := 0
	for {
		select {
		case <-frameCh:
			drained++
		default:
			return drained
		}
	}
}

func pumpFrames(
	ctx context.Context,
	frameCh <-chan EncodedFrame,
	session *LiveKitSession,
	cfg Config,
	waitForKeyframe bool,
	stats *runtimeStats,
	logger *log.Logger,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-session.Disconnected():
			return errors.New("livekit disconnected")
		case frame := <-frameCh:
			if len(frame.Data) == 0 {
				continue
			}

			if cfg.MaxPublishStale > 0 {
				age := time.Since(frame.CreatedAt)
				if age > cfg.MaxPublishStale {
					atomic.AddUint64(&stats.droppedStale, 1)
					continue
				}
			}

			if waitForKeyframe && !frame.KeyFrame {
				atomic.AddUint64(&stats.droppedWaitKeyframe, 1)
				continue
			}
			if frame.KeyFrame {
				waitForKeyframe = false
				stats.markKeyframe(time.Now())
			}

			if err := session.WriteFrame(frame); err != nil {
				logger.Printf("write frame failed: %v", err)
				return err
			}
			atomic.AddUint64(&stats.publishedFrames, 1)
		}
	}
}

func runMetricsLogger(ctx context.Context, cfg Config, frameCh <-chan EncodedFrame, stats *runtimeStats, logger *log.Logger) {
	ticker := time.NewTicker(cfg.MetricsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			persistRuntimeStatsSnapshot(cfg.MetricsStateFile, stats, logger)
			return
		case <-ticker.C:
			lastKF := atomic.LoadInt64(&stats.lastKeyframeUnixNanos)
			lastKFStr := "never"
			if lastKF > 0 {
				lastKFStr = time.Since(time.Unix(0, lastKF)).Round(time.Millisecond).String() + " ago"
			}
			logger.Printf(
				"metrics queue_depth=%d dropped_queue=%d dropped_stale=%d dropped_wait_keyframe=%d published=%d parse_errors=%d connect_attempts=%d reconnects=%d input_connections=%d last_keyframe=%s",
				len(frameCh),
				atomic.LoadUint64(&stats.droppedQueue),
				atomic.LoadUint64(&stats.droppedStale),
				atomic.LoadUint64(&stats.droppedWaitKeyframe),
				atomic.LoadUint64(&stats.publishedFrames),
				atomic.LoadUint64(&stats.parseErrors),
				atomic.LoadUint64(&stats.connectAttempts),
				atomic.LoadUint64(&stats.reconnects),
				atomic.LoadUint64(&stats.inputConnections),
				lastKFStr,
			)
			persistRuntimeStatsSnapshot(cfg.MetricsStateFile, stats, logger)
		}
	}
}

func loadRuntimeStatsSnapshot(path string, logger *log.Logger) *runtimeStats {
	stats := &runtimeStats{}
	if path == "" {
		return stats
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logger.Printf("metrics snapshot load failed: %v", err)
		}
		return stats
	}

	var snapshot runtimeStats
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		logger.Printf("metrics snapshot parse failed: %v", err)
		return stats
	}

	atomic.StoreUint64(&stats.droppedQueue, snapshot.droppedQueue)
	atomic.StoreUint64(&stats.droppedStale, snapshot.droppedStale)
	atomic.StoreUint64(&stats.droppedWaitKeyframe, snapshot.droppedWaitKeyframe)
	atomic.StoreUint64(&stats.publishedFrames, snapshot.publishedFrames)
	atomic.StoreUint64(&stats.parseErrors, snapshot.parseErrors)
	atomic.StoreUint64(&stats.connectAttempts, snapshot.connectAttempts)
	atomic.StoreUint64(&stats.reconnects, snapshot.reconnects)
	atomic.StoreUint64(&stats.inputConnections, snapshot.inputConnections)
	atomic.StoreInt64(&stats.lastKeyframeUnixNanos, snapshot.lastKeyframeUnixNanos)
	logger.Printf("loaded metrics snapshot from %s", path)
	return stats
}

func persistRuntimeStatsSnapshot(path string, stats *runtimeStats, logger *log.Logger) {
	if path == "" {
		return
	}

	snapshot := runtimeStats{
		droppedQueue:          atomic.LoadUint64(&stats.droppedQueue),
		droppedStale:          atomic.LoadUint64(&stats.droppedStale),
		droppedWaitKeyframe:   atomic.LoadUint64(&stats.droppedWaitKeyframe),
		publishedFrames:       atomic.LoadUint64(&stats.publishedFrames),
		parseErrors:           atomic.LoadUint64(&stats.parseErrors),
		connectAttempts:       atomic.LoadUint64(&stats.connectAttempts),
		reconnects:            atomic.LoadUint64(&stats.reconnects),
		inputConnections:      atomic.LoadUint64(&stats.inputConnections),
		lastKeyframeUnixNanos: atomic.LoadInt64(&stats.lastKeyframeUnixNanos),
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		logger.Printf("metrics snapshot marshal failed: %v", err)
		return
	}

	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logger.Printf("metrics snapshot mkdir failed: %v", err)
			return
		}
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, payload, 0o644); err != nil {
		logger.Printf("metrics snapshot write failed: %v", err)
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		logger.Printf("metrics snapshot rename failed: %v", err)
	}
}

func reconnectBackoff(min time.Duration, max time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		return min
	}
	ms := float64(min.Milliseconds()) * math.Pow(1.8, float64(attempt-1))
	if ms > float64(max.Milliseconds()) {
		ms = float64(max.Milliseconds())
	}
	// ±10% jitter to prevent thundering herd when multiple streamers reconnect
	jitter := ms * 0.1 * (2*rand.Float64() - 1)
	return time.Duration(ms+jitter) * time.Millisecond
}

func waitOrDone(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
