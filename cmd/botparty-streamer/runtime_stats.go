package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

type runtimeStats struct {
	droppedQueue          atomic.Uint64
	droppedStale          atomic.Uint64
	droppedWaitKeyframe   atomic.Uint64
	publishedFrames       atomic.Uint64
	parseErrors           atomic.Uint64
	connectAttempts       atomic.Uint64
	reconnects            atomic.Uint64
	inputConnections      atomic.Uint64
	lastKeyframeUnixNanos atomic.Int64
}

type runtimeStatsSnapshot struct {
	DroppedQueue          uint64 `json:"droppedQueue"`
	DroppedStale          uint64 `json:"droppedStale"`
	DroppedWaitKeyframe   uint64 `json:"droppedWaitKeyframe"`
	PublishedFrames       uint64 `json:"publishedFrames"`
	ParseErrors           uint64 `json:"parseErrors"`
	ConnectAttempts       uint64 `json:"connectAttempts"`
	Reconnects            uint64 `json:"reconnects"`
	InputConnections      uint64 `json:"inputConnections"`
	LastKeyframeUnixNanos int64  `json:"lastKeyframeUnixNanos"`
}

func (s *runtimeStats) markKeyframe(t time.Time) {
	s.lastKeyframeUnixNanos.Store(t.UnixNano())
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
			lastKF := stats.lastKeyframeUnixNanos.Load()
			lastKFStr := "never"
			if lastKF > 0 {
				lastKFStr = time.Since(time.Unix(0, lastKF)).Round(time.Millisecond).String() + " ago"
			}
			logger.Printf(
				"metrics queue_depth=%d dropped_queue=%d dropped_stale=%d dropped_wait_keyframe=%d published=%d parse_errors=%d connect_attempts=%d reconnects=%d input_connections=%d last_keyframe=%s",
				len(frameCh),
				stats.droppedQueue.Load(),
				stats.droppedStale.Load(),
				stats.droppedWaitKeyframe.Load(),
				stats.publishedFrames.Load(),
				stats.parseErrors.Load(),
				stats.connectAttempts.Load(),
				stats.reconnects.Load(),
				stats.inputConnections.Load(),
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

	var snapshot runtimeStatsSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		logger.Printf("metrics snapshot parse failed: %v", err)
		return stats
	}

	stats.droppedQueue.Store(snapshot.DroppedQueue)
	stats.droppedStale.Store(snapshot.DroppedStale)
	stats.droppedWaitKeyframe.Store(snapshot.DroppedWaitKeyframe)
	stats.publishedFrames.Store(snapshot.PublishedFrames)
	stats.parseErrors.Store(snapshot.ParseErrors)
	stats.connectAttempts.Store(snapshot.ConnectAttempts)
	stats.reconnects.Store(snapshot.Reconnects)
	stats.inputConnections.Store(snapshot.InputConnections)
	stats.lastKeyframeUnixNanos.Store(snapshot.LastKeyframeUnixNanos)
	logger.Printf("loaded metrics snapshot from %s", path)
	return stats
}

func persistRuntimeStatsSnapshot(path string, stats *runtimeStats, logger *log.Logger) {
	if path == "" {
		return
	}

	snapshot := runtimeStatsSnapshot{
		DroppedQueue:          stats.droppedQueue.Load(),
		DroppedStale:          stats.droppedStale.Load(),
		DroppedWaitKeyframe:   stats.droppedWaitKeyframe.Load(),
		PublishedFrames:       stats.publishedFrames.Load(),
		ParseErrors:           stats.parseErrors.Load(),
		ConnectAttempts:       stats.connectAttempts.Load(),
		Reconnects:            stats.reconnects.Load(),
		InputConnections:      stats.inputConnections.Load(),
		LastKeyframeUnixNanos: stats.lastKeyframeUnixNanos.Load(),
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
