package main

import (
"context"
"errors"
"io"
"log"
"math"
"net"
"os"
"os/signal"
"sync/atomic"
"time"
)

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

func main() {
logger := log.New(os.Stdout, "lk-h264-publisher ", log.LstdFlags|log.Lmicroseconds)

cfg, err := LoadConfig()
if err != nil {
logger.Fatalf("invalid config: %v", err)
}

var provider TokenProvider
if cfg.StaticToken != "" {
provider = StaticTokenProvider{Token: cfg.StaticToken}
logger.Printf("using static LiveKit token from LK_TOKEN")
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

stats := &runtimeStats{}
frameCh := make(chan EncodedFrame, cfg.FrameChanSize)

go runInputServer(ctx, cfg, frameCh, stats, logger)
go runMetricsLogger(ctx, cfg, frameCh, stats, logger)

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

session, err := ConnectLiveKit(ctx, cfg, token, logger)
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

func runInputServer(ctx context.Context, cfg Config, frameCh chan EncodedFrame, stats *runtimeStats, logger *log.Logger) {
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
handleInputConnection(ctx, conn, cfg, frameCh, stats, logger)
logger.Printf("ffmpeg disconnected")
}
}

func handleInputConnection(ctx context.Context, conn net.Conn, cfg Config, frameCh chan EncodedFrame, stats *runtimeStats, logger *log.Logger) {
defer conn.Close()

reader := NewAnnexBReader(conn)
assembler := NewAccessUnitAssembler(cfg.AUMaxNALUs, cfg.AUMaxBytes)
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
}
}
}

func reconnectBackoff(min time.Duration, max time.Duration, attempt int) time.Duration {
if attempt < 1 {
return min
}
ms := float64(min.Milliseconds()) * math.Pow(1.8, float64(attempt-1))
if ms > float64(max.Milliseconds()) {
return max
}
return time.Duration(ms) * time.Millisecond
}

func waitOrDone(ctx context.Context, d time.Duration) {
t := time.NewTimer(d)
defer t.Stop()
select {
case <-ctx.Done():
case <-t.C:
}
}
