package main

import (
	"context"
	"errors"
	"log"
	"math"
	"math/rand/v2"
	"time"
)

type framePumpSession interface {
	WriteFrame(frame EncodedFrame) error
	Disconnected() <-chan struct{}
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
	session framePumpSession,
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
					stats.droppedStale.Add(1)
					continue
				}
			}

			if waitForKeyframe && !frame.KeyFrame {
				stats.droppedWaitKeyframe.Add(1)
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
			stats.publishedFrames.Add(1)
		}
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
