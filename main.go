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
"syscall"
"time"
)

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

ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer cancel()

	frameCh := make(chan EncodedFrame, cfg.FrameChanSize)
go runInputServer(ctx, cfg, frameCh, logger)

attempt := 0
for {
if ctx.Err() != nil {
logger.Printf("shutdown requested")
return
}

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
err = pumpFrames(ctx, frameCh, session, logger)
session.Close()
if err != nil && !errors.Is(err, context.Canceled) {
logger.Printf("session ended: %v", err)
}

waitOrDone(ctx, cfg.ReconnectMin)
}
}

func runInputServer(ctx context.Context, cfg Config, frameCh chan EncodedFrame, logger *log.Logger) {
ln, err := net.Listen("tcp", cfg.InputListenAddr)
if err != nil {
logger.Fatalf("failed to listen on %s: %v", cfg.InputListenAddr, err)
}
defer ln.Close()

logger.Printf("waiting for ffmpeg h264 input on tcp://%s", cfg.InputListenAddr)

for {
if tcpLn, ok := ln.(*net.TCPListener); ok {
tcpLn.SetDeadline(time.Now().Add(1 * time.Second))
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

logger.Printf("ffmpeg connected from %s", conn.RemoteAddr())
handleInputConnection(ctx, conn, cfg, frameCh, logger)
logger.Printf("ffmpeg disconnected")
}
}

func handleInputConnection(ctx context.Context, conn net.Conn, cfg Config, frameCh chan EncodedFrame, logger *log.Logger) {
defer conn.Close()

reader := NewAnnexBReader(conn)
assembler := NewAccessUnitAssembler()
frameDuration := time.Second / time.Duration(cfg.FPS)

for {
if ctx.Err() != nil {
return
}
_ = conn.SetReadDeadline(time.Now().Add(cfg.ReadTimeout))

nalu, err := reader.NextNALU()
if err != nil {
if ne, ok := err.(net.Error); ok && ne.Timeout() {
if frame, key, ok := assembler.FlushIfStale(cfg.FlushTimeout); ok {
sendFrame(frameCh, EncodedFrame{Data: frame, Duration: frameDuration, KeyFrame: key, CreatedAt: time.Now()}, logger)
}
continue
}
if errors.Is(err, io.EOF) {
if frame, key, ok := assembler.FlushFinal(); ok {
sendFrame(frameCh, EncodedFrame{Data: frame, Duration: frameDuration, KeyFrame: key, CreatedAt: time.Now()}, logger)
}
return
}
logger.Printf("input parse error: %v", err)
return
}

if frame, key, ok := assembler.Push(nalu); ok {
sendFrame(frameCh, EncodedFrame{Data: frame, Duration: frameDuration, KeyFrame: key, CreatedAt: time.Now()}, logger)
}
}
}

func sendFrame(frameCh chan EncodedFrame, frame EncodedFrame, logger *log.Logger) {
select {
case frameCh <- frame:
default:
select {
case <-frameCh:
default:
}
select {
case frameCh <- frame:
default:
}
logger.Printf("frame queue full, dropping old frame")
}
}

func pumpFrames(ctx context.Context, frameCh <-chan EncodedFrame, session *LiveKitSession, logger *log.Logger) error {
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
if err := session.WriteFrame(frame); err != nil {
logger.Printf("write frame failed: %v", err)
return err
}
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
