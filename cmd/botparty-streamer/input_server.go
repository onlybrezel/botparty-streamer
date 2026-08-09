package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"runtime"
	"sync"
	"time"
)

// spsPPSGlobalCache retains parameter sets across ffmpeg reconnects.
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
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
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

		stats.inputConnections.Add(1)
		logger.Printf("ffmpeg connected from %s", conn.RemoteAddr())
		handleInputConnection(ctx, conn, cfg, frameCh, stats, spspps, logger)
		logger.Printf("ffmpeg disconnected")
	}
}

func handleInputConnection(ctx context.Context, conn net.Conn, cfg Config, frameCh chan EncodedFrame, stats *runtimeStats, spspps *spsPPSGlobalCache, logger *log.Logger) {
	defer conn.Close()

	reader := NewAnnexBReader(conn)
	assembler := NewAccessUnitAssembler(cfg.AUMaxNALUs, cfg.AUMaxBytes)
	// Reuse parameter sets when ffmpeg reconnects without sending new ones.
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
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
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
			stats.parseErrors.Add(1)
			logger.Printf("input parse error: %v", err)
			return
		}

		if frame, key, ok, reason := assembler.Push(nalu); ok {
			sendFrame(frameCh, EncodedFrame{Data: frame, Duration: frameDuration, KeyFrame: key, CreatedAt: time.Now()}, stats)
			if key {
				stats.markKeyframe(time.Now())
				// Save the latest parameter sets for the next input connection.
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
			stats.droppedQueue.Add(1)
			runtime.Gosched()
		default:
			runtime.Gosched()
		}
	}
}
