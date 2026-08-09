# botparty-streamer

[![CI](https://github.com/onlybrezel/botparty-streamer/actions/workflows/ci.yml/badge.svg)](https://github.com/onlybrezel/botparty-streamer/actions/workflows/ci.yml)
[![CodeQL](https://github.com/onlybrezel/botparty-streamer/actions/workflows/codeql.yml/badge.svg)](https://github.com/onlybrezel/botparty-streamer/actions/workflows/codeql.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

botparty-streamer publishes an H.264 Annex-B stream from ffmpeg to a LiveKit room. It runs as a regular LiveKit participant and keeps capture and encoding outside the process.

## How it works

```text
camera -> ffmpeg -> TCP Annex-B H.264 -> botparty-streamer -> LiveKit
```

- ffmpeg handles capture and encoding.
- botparty-streamer assembles access units and publishes the video track.
- A backend issues short-lived LiveKit tokens. API secrets do not need to be stored on the streaming device.

The input listener accepts one ffmpeg connection at a time. It binds to `127.0.0.1:5004` by default and rejects non-loopback addresses unless explicitly enabled.

## Requirements

- Go 1.24 or newer when building from source
- ffmpeg with an H.264 encoder
- A LiveKit server and a token with permission to join and publish

## Install

Download an archive from [GitHub Releases](https://github.com/onlybrezel/botparty-streamer/releases), or build the current checkout:

```bash
git clone https://github.com/onlybrezel/botparty-streamer.git
cd botparty-streamer
./scripts/install-botparty-streamer.sh
```

The installer builds the native binary and installs it as `/usr/local/bin/botparty-streamer`. Use a different destination when needed:

```bash
./scripts/install-botparty-streamer.sh --target "$HOME/.local/bin/botparty-streamer"
```

A build without installation:

```bash
go build -o dist/botparty-streamer ./cmd/botparty-streamer
```

Cross-builds are available through the same script:

```bash
./scripts/install-botparty-streamer.sh --arch linux-arm64 --no-install
./scripts/install-botparty-streamer.sh --arch linux-all --no-install
./scripts/install-botparty-streamer.sh --arch all --no-install
```

The version defaults to the current Git tag or commit. Override it with `--version` for a local release build.

## Configuration

Copy the example and set the LiveKit connection details:

```bash
cp .env.example .env
set -a
. ./.env
set +a
botparty-streamer
```

Required values:

| Variable | Purpose |
| --- | --- |
| `LK_URL` | LiveKit WebSocket URL |
| `LK_ROOM` | Room name |
| `LK_IDENTITY` | Participant identity |
| `LK_TOKEN`, `TOKEN_FILE` or `TOKEN_ENDPOINT` | Token source; exactly one source is normally used |

Common options:

| Variable | Default | Purpose |
| --- | --- | --- |
| `LK_NAME` | `LK_IDENTITY` | Display name |
| `LK_TRACK_NAME` | `camera` | Published track name |
| `INPUT_LISTEN_ADDR` | `127.0.0.1:5004` | TCP input address |
| `VIDEO_FPS` | `30` | Frame duration used for publishing |
| `FRAME_CHAN_SIZE` | `4` | Low-latency frame queue size |
| `MAX_PUBLISH_STALE_MS` | `250` | Maximum frame age before dropping |
| `HEALTH_ADDR` | `127.0.0.1:9090` | Health and metrics listener |
| `HEALTH_AUTH_TOKEN` | empty | Bearer token for health and metrics endpoints |
| `METRICS_STATE_FILE` | empty | Optional counter snapshot file |
| `CONFIG_FILE` | empty | Optional env-style configuration file |

Remote input and health listeners are refused by default. Set `ALLOW_REMOTE_INPUT=true` or `ALLOW_REMOTE_HEALTH=true` only when the corresponding listener should be reachable from another host. Protect a remote health endpoint with `HEALTH_AUTH_TOKEN` and network-level access controls.

The full set of options and defaults is listed in [`.env.example`](.env.example).

### Token endpoint

Using a token endpoint keeps LiveKit API credentials off the streaming device:

```bash
TOKEN_ENDPOINT=https://tokens.example.com/livekit
DEVICE_ID=robot-1
DEVICE_SECRET=replace-with-strong-secret
TOKEN_TTL_SECONDS=300
```

botparty-streamer sends a JSON request containing the room, identity, display name and requested TTL. The endpoint must return:

```json
{"token":"..."}
```

A small Node.js implementation is available in [`examples/token-backend-node`](examples/token-backend-node). It is intended as a starting point and refuses to start without bearer or device authentication.

## Run ffmpeg

The output must be raw H.264 in Annex-B format over TCP. Start botparty-streamer before ffmpeg.

### Linux with Video4Linux and x264

```bash
ffmpeg -hide_banner -loglevel warning \
  -f v4l2 -framerate 30 -video_size 1280x720 -i /dev/video0 \
  -an -c:v libx264 -preset veryfast -tune zerolatency \
  -g 60 -keyint_min 60 -bf 0 \
  -f h264 tcp://127.0.0.1:5004
```

### Raspberry Pi with V4L2 M2M

```bash
ffmpeg -hide_banner -loglevel warning \
  -f v4l2 -framerate 30 -video_size 1280x720 -i /dev/video0 \
  -an -c:v h264_v4l2m2m -pix_fmt yuv420p \
  -g 60 -bf 0 \
  -f h264 tcp://127.0.0.1:5004
```

### macOS with VideoToolbox

```bash
ffmpeg -hide_banner -loglevel warning \
  -f avfoundation -framerate 30 -video_size 1280x720 -i '0:none' \
  -an -c:v h264_videotoolbox -realtime true \
  -g 60 -bf 0 \
  -f h264 tcp://127.0.0.1:5004
```

Other hardware encoders work as long as they produce H.264 Annex-B without B-frames. A short GOP reduces recovery time after reconnects.

## Health and metrics

The HTTP listener provides:

- `GET /health` for process health and version
- `GET /metrics` for Prometheus-compatible runtime counters

Example:

```bash
curl http://127.0.0.1:9090/health
curl http://127.0.0.1:9090/metrics
```

When `HEALTH_AUTH_TOKEN` is set, send it as a bearer token.

## systemd

Install the binary and unit files:

```bash
./scripts/install-botparty-streamer.sh
sudo install -m 0644 deploy/systemd/botparty-streamer.service /etc/systemd/system/
sudo install -m 0644 deploy/systemd/ffmpeg-h264-feed.service /etc/systemd/system/
```

Create `/etc/default/botparty-streamer` from [`.env.example`](.env.example). Create `/etc/default/ffmpeg-h264-feed` with the ffmpeg command:

```bash
FFMPEG_CMD='ffmpeg -hide_banner -loglevel warning -f v4l2 -framerate 30 -video_size 1280x720 -i /dev/video0 -an -c:v libx264 -preset veryfast -tune zerolatency -g 60 -bf 0 -f h264 tcp://127.0.0.1:5004'
```

Then enable both services:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now botparty-streamer.service
sudo systemctl enable --now ffmpeg-h264-feed.service
```

The supplied units run as the `botparty` user. Create that system account and give it access to the selected camera and hardware encoder devices before starting the services.

## Development

```bash
gofmt -w cmd/botparty-streamer
go vet ./...
go test ./...
go build ./...
```

Source code lives under `cmd/botparty-streamer`. Deployment files, examples and operator scripts stay separate from the application package.

See [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request. Report vulnerabilities as described in [SECURITY.md](SECURITY.md). Third-party licensing information is covered in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## License

botparty-streamer is available under the [MIT License](LICENSE).
