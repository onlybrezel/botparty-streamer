# botparty-streamer

A small, cross-platform Go tool that accepts already H.264-encoded Annex-B video from ffmpeg over TCP and publishes it as a normal LiveKit participant.

## Architecture

- Input: exactly one source `tcp://127.0.0.1:5004` (Annex-B NAL units).
- ffmpeg handles capture + encoding (hardware/software depending on platform).
- The Go tool only handles:
  - Annex-B parsing
  - Access-unit assembly
  - SPS/PPS handling before IDR frames
  - Publishing as H.264 video track to LiveKit
  - Reconnect, logging, clean shutdown
- Token:
  - No API key/secret on the device
  - Device uses short-lived tokens (via `LK_TOKEN` directly or via `TOKEN_ENDPOINT`)

Clear separation of concerns:

- ffmpeg = encoder
- botparty-streamer = LiveKit publisher
- Backend = token issuer

## Hardening over the prototype

- Removed destructive `bytes.Trim(..., "\x00")` logic from the NAL parser.
- TCP chunking is transparent; NAL boundaries are detected exclusively via Annex-B start codes.
- AU guards added:
  - `AU_MAX_NALUS`
  - `AU_MAX_BYTES`
  - Flush on uncertain slice boundary instead of long accumulation.
- Queue model tuned for low-latency:
  - Very small queue (`FRAME_CHAN_SIZE` default `4`)
  - Strict latest-frame / drop-oldest
  - Publish-drop for stale frames (`MAX_PUBLISH_STALE_MS`).
- Reader/flush timeouts tightened:
  - `INPUT_READ_TIMEOUT_MS=500`
  - `FRAME_FLUSH_TIMEOUT_MS=50`
- Reconnect rebase:
  - Queue is drained on session restart
  - Publisher waits for keyframe again.
- Session close path cleaned:
  - No per-session background goroutine waiting on global shutdown signal.
- Runtime metrics in log:
  - queue depth
  - dropped frames (queue/stale/wait-keyframe)
  - reconnect count
  - parse errors
  - last keyframe seen
- Token backend fail-closed:
  - Startup aborts if neither bearer nor device secrets are set.

## Directory layout

```
.
├── go.mod
├── main.go
├── config.go
├── token_client.go
├── annexb_reader.go
├── publisher.go
├── signals_unix.go
├── signals_windows.go
├── .env.example
├── scripts/
│   ├── install-botparty-streamer.sh
│   └── fetch_token.sh
├── deploy/
│   └── systemd/
│       ├── botparty-streamer.service
│       └── ffmpeg-h264-feed.service
└── examples/
    └── token-backend-node/
        ├── package.json
        ├── .env.example
        └── server.js
```

## Build

Prerequisite: Go 1.22+

```bash
go mod tidy
go build -o bin/botparty-streamer .
```

Build + system-wide installation in one step:

```bash
./scripts/install-botparty-streamer.sh
```

Targeted builds via the script:

```bash
# Linux AMD64 only
./scripts/install-botparty-streamer.sh --arch amd64 --version v0.1.0 --no-install

# Linux ARM64 only (Raspberry Pi 64-bit)
./scripts/install-botparty-streamer.sh --arch arm64 --version v0.1.0 --no-install

# All Linux release binaries at once
./scripts/install-botparty-streamer.sh --arch linux-all --version v0.1.1 --no-install

# Full multi-platform release set
./scripts/install-botparty-streamer.sh --arch all --version v0.1.1 --no-install
```

Cross-build examples:

```bash
# Linux x86_64
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/botparty-streamer-v0.1.0-linux-amd64 .

# Linux ARM64 (e.g. Raspberry Pi OS 64-bit)
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bin/botparty-streamer-v0.1.0-linux-arm64 .

# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "-X main.version=v0.1.1" -o bin/botparty-streamer-v0.1.1-darwin-arm64 .

# Windows x86_64
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-X main.version=v0.1.1" -o bin/botparty-streamer-v0.1.1-windows-amd64.exe .
```

Note on cross-platform support:

- Signal handling is split via build tags (`signals_unix.go`, `signals_windows.go`).
- The runtime itself is platform-neutral as long as ffmpeg provides H.264 Annex-B on the target platform.
- Real interop and build validation on each target platform is required before production use.

## Configuration

```bash
cp .env.example .env
```

Key low-latency parameters:

- `FRAME_CHAN_SIZE=4` (keep small)
- `MAX_PUBLISH_STALE_MS=250` (discard old frames)
- `AU_MAX_NALUS=64`
- `AU_MAX_BYTES=2097152`
- `INPUT_READ_TIMEOUT_MS=500`
- `FRAME_FLUSH_TIMEOUT_MS=50`

Input bind protection:

- Default is loopback-only (`INPUT_LISTEN_ADDR=127.0.0.1:5004`).
- Non-loopback binding is rejected by default.
- Set `ALLOW_REMOTE_INPUT=true` to explicitly allow external binding.

Recommended: fetch tokens from the backend (no API key/secret on device):

- `TOKEN_ENDPOINT`
- `DEVICE_ID`
- `DEVICE_SECRET`

Direct mode via `LK_TOKEN` is supported; keep tokens short-lived.

## Startup order

1. Start the token backend.
2. Start the publisher.
3. Start ffmpeg writing to `tcp://127.0.0.1:5004`.

Start publisher (with env):

```bash
set -a
source .env
set +a
./bin/botparty-streamer
```

Optional token-fetch script:

```bash
set -a
source .env
set +a
./scripts/fetch_token.sh /tmp/lk-token.env
set -a
source /tmp/lk-token.env
set +a
./bin/botparty-streamer
```

## ffmpeg examples (no re-encoding in the Go tool)

Important: output must be Annex-B H.264 via TCP localhost.

### Raspberry Pi 4 (h264_v4l2m2m)

```bash
ffmpeg -hide_banner -loglevel warning \
  -f v4l2 -framerate 30 -video_size 1280x720 -i /dev/video0 \
  -an \
  -c:v h264_v4l2m2m -pix_fmt yuv420p \
  -g 60 -keyint_min 60 -bf 0 -tune zerolatency \
  -f h264 tcp://127.0.0.1:5004
```

### Linux x86_64 (VAAPI)

```bash
ffmpeg -hide_banner -loglevel warning \
  -f v4l2 -framerate 30 -video_size 1280x720 -i /dev/video0 \
  -an \
  -vaapi_device /dev/dri/renderD128 \
  -vf 'format=nv12,hwupload' \
  -c:v h264_vaapi -g 60 -bf 0 \
  -f h264 tcp://127.0.0.1:5004
```

Software fallback (Linux):

```bash
ffmpeg -hide_banner -loglevel warning \
  -f v4l2 -framerate 30 -video_size 1280x720 -i /dev/video0 \
  -an -c:v libx264 -preset veryfast -tune zerolatency \
  -g 60 -keyint_min 60 -bf 0 \
  -f h264 tcp://127.0.0.1:5004
```

### macOS (VideoToolbox)

```bash
ffmpeg -hide_banner -loglevel warning \
  -f avfoundation -framerate 30 -video_size 1280x720 -i '0:none' \
  -an \
  -c:v h264_videotoolbox -realtime true \
  -g 60 -bf 0 \
  -f h264 tcp://127.0.0.1:5004
```

### Windows (NVENC / fallback)

```bash
ffmpeg -hide_banner -loglevel warning ^
  -f dshow -framerate 30 -video_size 1280x720 -i video="Integrated Camera" ^
  -an ^
  -c:v h264_nvenc -preset p4 -tune ll -g 60 -bf 0 ^
  -f h264 tcp://127.0.0.1:5004
```

AMF/QSV/software are interchangeable as long as the output remains H.264 Annex-B.

## systemd (Linux)

Publisher unit:

- `deploy/systemd/botparty-streamer.service`

FFmpeg feed unit:

- `deploy/systemd/ffmpeg-h264-feed.service`

Installation example:

```bash
./scripts/install-botparty-streamer.sh
sudo install -m 0644 deploy/systemd/botparty-streamer.service /etc/systemd/system/botparty-streamer.service
sudo install -m 0644 deploy/systemd/ffmpeg-h264-feed.service /etc/systemd/system/ffmpeg-h264-feed.service
```

Example `/etc/default/botparty-streamer`:

```bash
LK_URL=wss://livekit.example.com
LK_ROOM=robot-room-1
LK_IDENTITY=robot-streamer-1
LK_NAME=Robot Streamer 1
LK_TRACK_NAME=camera
INPUT_LISTEN_ADDR=127.0.0.1:5004
VIDEO_FPS=30
INPUT_READ_TIMEOUT_MS=500
FRAME_FLUSH_TIMEOUT_MS=50
ALLOW_REMOTE_INPUT=false
FRAME_CHAN_SIZE=4
MAX_PUBLISH_STALE_MS=250
AU_MAX_NALUS=64
AU_MAX_BYTES=2097152
TOKEN_ENDPOINT=http://127.0.0.1:8088/token
DEVICE_ID=robot-1
DEVICE_SECRET=replace-with-strong-secret
TOKEN_TTL_SECONDS=300
```

Example `/etc/default/ffmpeg-h264-feed`:

```bash
FFMPEG_CMD='ffmpeg -hide_banner -loglevel warning -f v4l2 -framerate 30 -video_size 1280x720 -i /dev/video0 -an -c:v h264_v4l2m2m -g 60 -bf 0 -f h264 tcp://127.0.0.1:5004'
```

Enable:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now botparty-streamer.service
sudo systemctl enable --now ffmpeg-h264-feed.service
```

## Windows Service / macOS LaunchAgent notes

- Windows: run the publisher as a service via NSSM or WinSW, ffmpeg as a separate service/task.
- macOS: create separate LaunchAgent/LaunchDaemon entries for the publisher and ffmpeg; use the same startup order as above.

## Minimal token backend (Node.js)

```bash
cd examples/token-backend-node
cp .env.example .env
npm install
npm start
```

Important:

- The example starts fail-closed.
- Without `TOKEN_ISSUER_BEARER` and without `DEVICE_SECRETS` the process exits intentionally with an error.

Endpoint:

- `POST /token`
- Body: `{"room":"robot-room-1","identity":"robot-streamer-1","name":"Robot Streamer 1","ttlSeconds":300}`
- Response: `{"token":"..."}`

## Troubleshooting

1. `connect to livekit` fails:
   - Check `LK_URL`, verify certificates, check token TTL.

2. No video in room:
   - Confirm ffmpeg is writing to `tcp://127.0.0.1:5004`.
   - Check publisher log for `ffmpeg connected`.

3. No image immediately after reconnect:
   - The publisher intentionally waits for the next keyframe to avoid sending stale frames.
   - Shorten the GOP (e.g. `-g 60`) if recovery is too slow.

4. High latency:
   - Keep `FRAME_CHAN_SIZE` small.
   - Do not set `MAX_PUBLISH_STALE_MS` too high.
   - Use ffmpeg low-latency encoder settings (`-tune zerolatency`, `-bf 0`).

5. Frequent parser errors:
   - Make sure ffmpeg is producing real Annex-B H.264 (`-f h264`).
   - Check the encoder/bitstream, especially with exotic hardware pipelines.
