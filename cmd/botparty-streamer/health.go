package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

func runHealthServer(ctx context.Context, addr string, authToken string, stats *runtimeStats, frameCh <-chan EncodedFrame, logger *log.Logger) {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"version": version,
		})
	})

	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		queueDepth := len(frameCh)
		lastKF := stats.lastKeyframeUnixNanos.Load()
		lastKFSec := 0.0
		if lastKF > 0 {
			lastKFSec = time.Since(time.Unix(0, lastKF)).Seconds()
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# HELP botparty_frames_published_total Total frames written to LiveKit\n")
		fmt.Fprintf(w, "# TYPE botparty_frames_published_total counter\n")
		fmt.Fprintf(w, "botparty_frames_published_total %d\n", stats.publishedFrames.Load())

		fmt.Fprintf(w, "# HELP botparty_frames_dropped_queue_total Frames dropped due to full channel\n")
		fmt.Fprintf(w, "# TYPE botparty_frames_dropped_queue_total counter\n")
		fmt.Fprintf(w, "botparty_frames_dropped_queue_total %d\n", stats.droppedQueue.Load())

		fmt.Fprintf(w, "# HELP botparty_frames_dropped_stale_total Frames dropped because they were too old\n")
		fmt.Fprintf(w, "# TYPE botparty_frames_dropped_stale_total counter\n")
		fmt.Fprintf(w, "botparty_frames_dropped_stale_total %d\n", stats.droppedStale.Load())

		fmt.Fprintf(w, "# HELP botparty_frames_dropped_wait_keyframe_total Frames dropped while waiting for keyframe\n")
		fmt.Fprintf(w, "# TYPE botparty_frames_dropped_wait_keyframe_total counter\n")
		fmt.Fprintf(w, "botparty_frames_dropped_wait_keyframe_total %d\n", stats.droppedWaitKeyframe.Load())

		fmt.Fprintf(w, "# HELP botparty_parse_errors_total NAL unit parse errors\n")
		fmt.Fprintf(w, "# TYPE botparty_parse_errors_total counter\n")
		fmt.Fprintf(w, "botparty_parse_errors_total %d\n", stats.parseErrors.Load())

		fmt.Fprintf(w, "# HELP botparty_connect_attempts_total Total LiveKit connection attempts\n")
		fmt.Fprintf(w, "# TYPE botparty_connect_attempts_total counter\n")
		fmt.Fprintf(w, "botparty_connect_attempts_total %d\n", stats.connectAttempts.Load())

		fmt.Fprintf(w, "# HELP botparty_reconnects_total Total LiveKit reconnections\n")
		fmt.Fprintf(w, "# TYPE botparty_reconnects_total counter\n")
		fmt.Fprintf(w, "botparty_reconnects_total %d\n", stats.reconnects.Load())

		fmt.Fprintf(w, "# HELP botparty_input_connections_total Total ffmpeg input TCP connections\n")
		fmt.Fprintf(w, "# TYPE botparty_input_connections_total counter\n")
		fmt.Fprintf(w, "botparty_input_connections_total %d\n", stats.inputConnections.Load())

		fmt.Fprintf(w, "# HELP botparty_frame_queue_depth Current frame channel depth\n")
		fmt.Fprintf(w, "# TYPE botparty_frame_queue_depth gauge\n")
		fmt.Fprintf(w, "botparty_frame_queue_depth %d\n", queueDepth)

		fmt.Fprintf(w, "# HELP botparty_last_keyframe_seconds_ago Seconds since last keyframe was received\n")
		fmt.Fprintf(w, "# TYPE botparty_last_keyframe_seconds_ago gauge\n")
		fmt.Fprintf(w, "botparty_last_keyframe_seconds_ago %.3f\n", lastKFSec)
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authToken != "" {
			auth := strings.TrimSpace(r.Header.Get("Authorization"))
			expected := "Bearer " + authToken
			if auth != expected {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("unauthorized\n"))
				return
			}
		}

		startedAt := time.Now()
		mux.ServeHTTP(w, r)
		logger.Printf("health request path=%q method=%q duration=%s remote=%q", r.URL.Path, r.Method, time.Since(startedAt), r.RemoteAddr)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	logger.Printf("health/metrics HTTP server listening on http://%s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Printf("health server error: %v", err)
	}
}
