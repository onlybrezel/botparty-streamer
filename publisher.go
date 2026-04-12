package main

import (
"context"
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
disconnected chan struct{}
once         sync.Once
}

func ConnectLiveKit(ctx context.Context, cfg Config, token string, logger *log.Logger) (*LiveKitSession, error) {
disconnected := make(chan struct{})
	var disconnectedOnce sync.Once
cb := &lksdk.RoomCallback{
OnDisconnected: func() {
logger.Printf("livekit disconnected")
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

_, err = room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
Name:   cfg.TrackName,
Source: livekit.TrackSource_CAMERA,
})
if err != nil {
room.Disconnect()
return nil, fmt.Errorf("publish track: %w", err)
}

logger.Printf("livekit connected: room=%s identity=%s track=%s", cfg.RoomName, cfg.Identity, cfg.TrackName)

session := &LiveKitSession{Room: room, Track: track, disconnected: disconnected}

go func() {
<-ctx.Done()
session.Close()
}()

return session, nil
}

func (s *LiveKitSession) WriteFrame(frame EncodedFrame) error {
return s.Track.WriteSample(media.Sample{Data: frame.Data, Duration: frame.Duration})
}

func (s *LiveKitSession) Disconnected() <-chan struct{} {
return s.disconnected
}

func (s *LiveKitSession) Close() {
s.once.Do(func() {
if s.Room != nil {
time.Sleep(30 * time.Millisecond)
s.Room.Disconnect()
}
})
}
