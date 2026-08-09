package main

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
	"time"
)

type fakeFramePumpSession struct {
	disconnected chan struct{}
	writes       []EncodedFrame
	writeErr     error
	onWrite      func()
}

func (f *fakeFramePumpSession) WriteFrame(frame EncodedFrame) error {
	f.writes = append(f.writes, frame)
	if f.onWrite != nil {
		f.onWrite()
	}
	return f.writeErr
}

func (f *fakeFramePumpSession) Disconnected() <-chan struct{} {
	return f.disconnected
}

func TestLiveKitSessionClose_OnlyRunsOnce(t *testing.T) {
	closeCalls := 0
	session := &LiveKitSession{
		disconnected: make(chan struct{}),
		closeFn: func() {
			closeCalls++
		},
	}

	session.Close()
	session.Close()

	if closeCalls != 1 {
		t.Fatalf("closeFn called %d times, want 1", closeCalls)
	}
}

func TestPumpFrames_PublishesKeyframeAndStopsOnDisconnect(t *testing.T) {
	frameCh := make(chan EncodedFrame, 2)
	frameCh <- EncodedFrame{
		Data:      []byte{0x65, 0x00},
		Duration:  time.Second / 30,
		KeyFrame:  true,
		CreatedAt: time.Now(),
	}

	disconnected := make(chan struct{})
	session := &fakeFramePumpSession{
		disconnected: disconnected,
		onWrite: func() {
			close(disconnected)
		},
	}
	stats := &runtimeStats{}
	cfg := Config{MaxPublishStale: 250 * time.Millisecond}

	err := pumpFrames(context.Background(), frameCh, session, cfg, true, stats, log.New(io.Discard, "", 0))
	if err == nil || !errors.Is(err, errors.New("livekit disconnected")) && err.Error() != "livekit disconnected" {
		t.Fatalf("pumpFrames error = %v, want livekit disconnected", err)
	}
	if len(session.writes) != 1 {
		t.Fatalf("published %d frames, want 1", len(session.writes))
	}
	if stats.publishedFrames.Load() != 1 {
		t.Fatalf("publishedFrames = %d, want 1", stats.publishedFrames.Load())
	}
}
