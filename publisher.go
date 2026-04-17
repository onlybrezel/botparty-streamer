package main

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type LiveKitSession struct {
	Room         *lksdk.Room
	Track        *lksdk.LocalSampleTrack
	pub          *lksdk.LocalTrackPublication
	disconnected chan struct{}
	once         sync.Once
}

func ConnectLiveKit(cfg Config, token string, logger *log.Logger) (*LiveKitSession, error) {
	disconnected := make(chan struct{})
	var disconnectedOnce sync.Once
	cb := &lksdk.RoomCallback{
		OnDisconnectedWithReason: func(reason lksdk.DisconnectionReason) {
			logger.Printf("livekit disconnected reason=%s", reason)
			disconnectedOnce.Do(func() {
				close(disconnected)
			})
		},
	}

	room, err := lksdk.ConnectToRoomWithToken(cfg.LiveKitURL, token, cb)
	if err != nil {
		return nil, fmt.Errorf("connect to livekit: %w", err)
	}

	track, err := lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264})
	if err != nil {
		room.Disconnect()
		return nil, fmt.Errorf("create h264 sample track: %w", err)
	}

	pub, err := room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
		Name:   cfg.TrackName,
		Source: livekit.TrackSource_CAMERA,
	})
	if err != nil {
		room.Disconnect()
		return nil, fmt.Errorf("publish track: %w", err)
	}

	logger.Printf("livekit connected: room=%s identity=%s track=%s", cfg.RoomName, cfg.Identity, cfg.TrackName)

	return &LiveKitSession{Room: room, Track: track, pub: pub, disconnected: disconnected}, nil
}

// WriteFrame writes an encoded H264 frame to the LiveKit track.
// It uses a 200ms deadline to avoid blocking indefinitely on network congestion;
// if the deadline expires the frame is dropped and an error is returned so the
// caller can decide whether to reconnect.
func (s *LiveKitSession) WriteFrame(frame EncodedFrame) error {
	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		done <- result{s.Track.WriteSample(media.Sample{Data: frame.Data, Duration: frame.Duration}, nil)}
	}()
	select {
	case r := <-done:
		return r.err
	case <-time.After(200 * time.Millisecond):
		return fmt.Errorf("WriteSample timeout: network congestion or blocked track")
	}
}

func (s *LiveKitSession) Disconnected() <-chan struct{} {
	return s.disconnected
}

func (s *LiveKitSession) Close() {
	s.once.Do(func() {
		if s.Room != nil {
			if s.pub != nil {
				// Explicitly unpublish the track before disconnecting so LiveKit
				// notifies viewers sooner instead of relying on the disconnect timeout.
				_ = s.Room.LocalParticipant.UnpublishTrack(s.pub.SID())
			}
			s.Room.Disconnect()
		}
	})
}
