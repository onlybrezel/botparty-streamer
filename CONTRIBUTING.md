# Contributing

Bug reports, fixes and focused improvements are welcome.

## Development

Requirements:

- Go 1.26 or newer
- ffmpeg for an end-to-end stream test

Before opening a pull request, run:

```bash
gofmt -w cmd/botparty-streamer
go vet ./...
go test ./...
go build ./...
```

Keep pull requests small enough to review in one pass. Add tests for behavior changes and update the README when configuration or operator-facing behavior changes.

## Commits

Use short, direct commit messages. Explain the reason for a change in the pull request rather than repeating the diff.

By contributing, you agree that your contribution is licensed under the MIT License included in this repository.
