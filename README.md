# lk-h264-publisher

Kleines, plattformneutrales Go-Tool, das bereits H.264-encodiertes Annex-B Video von ffmpeg per TCP annimmt und als normaler LiveKit Participant published.

## Architektur (beibehalten)

- Eingabe: genau eine Quelle `tcp://127.0.0.1:5004` (Annex-B NAL Units).
- ffmpeg macht Capture + Encoding (Hardware/Software je nach Plattform).
- Go-Tool macht nur:
  - Annex-B Parsing
  - Access-Unit Assembly
  - SPS/PPS Handling vor IDR
  - Publish als H.264 Video Track zu LiveKit
  - Reconnect, Logging, sauberes Shutdown
- Token:
  - kein API-Key/Secret auf dem Gerät
  - Gerät verwendet kurzlebige Tokens (direkt via `LK_TOKEN` oder via `TOKEN_ENDPOINT`)

Klare Trennung:

- ffmpeg = Encoder
- lk-h264-publisher = LiveKit Publisher
- Backend = Token-Aussteller

## Was gegenüber dem Prototyp gehärtet wurde

- Keine destruktive `bytes.Trim(..., "\\x00")` Logik mehr im NAL-Parser.
- TCP-Chunking bleibt transparent, NAL-Grenzen werden ausschließlich über Annex-B Startcodes erkannt.
- AU-Guards hinzugefügt:
  - `AU_MAX_NALUS`
  - `AU_MAX_BYTES`
  - Flush bei unsicherer Slice-Grenze statt langem Sammeln.
- Queue-Modell auf low-latency ausgelegt:
  - sehr kleine Queue (`FRAME_CHAN_SIZE` Default `4`)
  - strict latest-frame / drop-oldest
  - Publish-Drop für veraltete Frames (`MAX_PUBLISH_STALE_MS`).
- Reconnect-Rebase:
  - Queue wird bei Session-Neustart geleert
  - Publish wartet erneut auf Keyframe.
- Laufzeitmetriken im Log:
  - queue depth
  - dropped frames (queue/stale/wait keyframe)
  - reconnect count
  - parse errors
  - last keyframe seen
- Token-Backend fail-closed:
  - Start bricht ab, wenn weder Bearer noch Device-Secrets gesetzt sind.

## Verzeichnislayout

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
│   └── fetch_token.sh
├── deploy/
│   └── systemd/
│       ├── lk-h264-publisher.service
│       └── ffmpeg-h264-feed.service
└── examples/
    └── token-backend-node/
        ├── package.json
        ├── .env.example
        └── server.js
```

## Build

Voraussetzung: Go 1.22+

```bash
go mod tidy
go build -o bin/lk-h264-publisher .
```

Cross-Build Beispiele:

```bash
# Linux x86_64
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/lk-h264-publisher-linux-amd64 .

# Linux ARM64 (z.B. Raspberry Pi OS 64-bit)
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bin/lk-h264-publisher-linux-arm64 .

# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o bin/lk-h264-publisher-darwin-arm64 .

# Windows x86_64
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/lk-h264-publisher-windows-amd64.exe .
```

Hinweis zur Cross-Platform Aussage:

- Signal-Handling ist über Build-Tags getrennt (`signals_unix.go`, `signals_windows.go`).
- Die Laufzeit bleibt plattformneutral, sofern ffmpeg auf der Zielplattform H.264 Annex-B liefert.
- Reale Interop- und Build-Validierung auf jeder Zielplattform bleibt vor Produktion Pflicht.

## Konfiguration

```bash
cp .env.example .env
```

Wichtige Low-Latency Parameter:

- `FRAME_CHAN_SIZE=4` (klein halten)
- `MAX_PUBLISH_STALE_MS=250` (alte Frames verwerfen)
- `AU_MAX_NALUS=64`
- `AU_MAX_BYTES=2097152`

Empfohlen: Token über Backend beziehen (ohne API-Key/Secret auf Gerät):

- `TOKEN_ENDPOINT`
- `DEVICE_ID`
- `DEVICE_SECRET`

Direktmodus über `LK_TOKEN` ist möglich, Token muss kurzlebig bleiben.

## Startreihenfolge

1. Token-Backend starten.
2. Publisher starten.
3. ffmpeg starten und nach `tcp://127.0.0.1:5004` schreiben.

Publisher starten (mit env):

```bash
set -a
source .env
set +a
./bin/lk-h264-publisher
```

Optionales Token-Fetch Skript:

```bash
set -a
source .env
set +a
./scripts/fetch_token.sh /tmp/lk-token.env
set -a
source /tmp/lk-token.env
set +a
./bin/lk-h264-publisher
```

## ffmpeg Beispiele (kein Re-Encode im Go-Tool)

Wichtig: Ausgabe ist Annex-B H.264 via TCP localhost.

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

Software-Fallback Linux:

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

AMF/QSV/Software sind analog austauschbar, solange Ausgang H.264 Annex-B bleibt.

## systemd (Linux)

Publisher Unit:

- `deploy/systemd/lk-h264-publisher.service`

FFmpeg Feed Unit:

- `deploy/systemd/ffmpeg-h264-feed.service`

Installationsbeispiel:

```bash
sudo install -m 0755 bin/lk-h264-publisher /usr/local/bin/lk-h264-publisher
sudo install -m 0644 deploy/systemd/lk-h264-publisher.service /etc/systemd/system/lk-h264-publisher.service
sudo install -m 0644 deploy/systemd/ffmpeg-h264-feed.service /etc/systemd/system/ffmpeg-h264-feed.service
```

Beispiel `/etc/default/lk-h264-publisher`:

```bash
LK_URL=wss://livekit.example.com
LK_ROOM=robot-room-1
LK_IDENTITY=robot-streamer-1
LK_NAME=Robot Streamer 1
LK_TRACK_NAME=camera
INPUT_LISTEN_ADDR=127.0.0.1:5004
VIDEO_FPS=30
FRAME_CHAN_SIZE=4
MAX_PUBLISH_STALE_MS=250
AU_MAX_NALUS=64
AU_MAX_BYTES=2097152
TOKEN_ENDPOINT=http://127.0.0.1:8088/token
DEVICE_ID=robot-1
DEVICE_SECRET=replace-with-strong-secret
TOKEN_TTL_SECONDS=300
```

Beispiel `/etc/default/ffmpeg-h264-feed`:

```bash
FFMPEG_CMD='ffmpeg -hide_banner -loglevel warning -f v4l2 -framerate 30 -video_size 1280x720 -i /dev/video0 -an -c:v h264_v4l2m2m -g 60 -bf 0 -f h264 tcp://127.0.0.1:5004'
```

Aktivieren:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now lk-h264-publisher.service
sudo systemctl enable --now ffmpeg-h264-feed.service
```

## Windows Service / macOS LaunchAgent Hinweise

- Windows: über NSSM oder WinSW den Publisher als Dienst starten, ffmpeg als separaten Dienst/Task.
- macOS: LaunchAgent/LaunchDaemon für Publisher und ffmpeg getrennt anlegen, gleiche Startreihenfolge wie oben.

## Minimales Token-Backend (Node.js)

```bash
cd examples/token-backend-node
cp .env.example .env
npm install
npm start
```

Wichtig:

- Das Beispiel startet fail-closed.
- Ohne `TOKEN_ISSUER_BEARER` und ohne `DEVICE_SECRETS` beendet sich der Prozess absichtlich mit Fehler.

Endpoint:

- `POST /token`
- Body: `{"room":"robot-room-1","identity":"robot-streamer-1","name":"Robot Streamer 1","ttlSeconds":300}`
- Antwort: `{"token":"..."}`

## Troubleshooting

1. `connect to livekit` schlägt fehl:
- URL prüfen (`LK_URL`), Zertifikate prüfen, Token-TTL prüfen.

2. Kein Video im Room:
- Prüfen, ob ffmpeg wirklich nach `tcp://127.0.0.1:5004` schreibt.
- Publisher-Log auf `ffmpeg connected` prüfen.

3. Reconnect zeigt zunächst kein Bild:
- Publisher wartet absichtlich auf nächstes Keyframe, um alte/falsche Frames nicht zu senden.
- GOP verkürzen (z.B. `-g 60`) falls Recovery zu langsam ist.

4. Hohe Latenz:
- `FRAME_CHAN_SIZE` klein halten.
- `MAX_PUBLISH_STALE_MS` nicht zu hoch setzen.
- ffmpeg low-latency Encoder-Settings nutzen (`-tune zerolatency`, `-bf 0`).

5. Häufige Parser-Fehler:
- Sicherstellen, dass ffmpeg wirklich Annex-B H.264 liefert (`-f h264`).
- Encoder/Bitstream prüfen, insbesondere bei exotischen Hardware-Pipelines.
