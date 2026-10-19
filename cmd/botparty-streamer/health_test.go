package main

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type healthLogWriter chan string

func (w healthLogWriter) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}

func TestRunHealthServer_EscapesRequestPathInLogs(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	logs := make(healthLogWriter, 8)
	go func() {
		runHealthServer(ctx, addr, "", &runtimeStats{}, make(chan EncodedFrame, 1), log.New(logs, "", 0))
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("health server did not stop after context cancel")
		}
	})
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, reqErr := client.Get("http://" + addr + "/health%0Aforged-entry%0D%09")
		if reqErr == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("health endpoint did not come up in time: %v", reqErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for {
		select {
		case entry := <-logs:
			if !strings.Contains(entry, "health request") {
				continue
			}
			if !strings.Contains(entry, `path="/health\nforged-entry\r\t"`) {
				t.Fatalf("request path was not escaped: %q", entry)
			}
			if strings.ContainsAny(strings.TrimSuffix(entry, "\n"), "\r\n\t") {
				t.Fatalf("request injected control characters into log entry: %q", entry)
			}
			return
		case <-time.After(3 * time.Second):
			t.Fatal("request was not logged")
		}
	}
}

func TestRunHealthServer_ShutsDownOnCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runHealthServer(ctx, addr, "", &runtimeStats{}, make(chan EncodedFrame, 1), log.New(io.Discard, "", 0))
		close(done)
	}()

	httpReadyDeadline := time.Now().Add(3 * time.Second)
	for {
		resp, reqErr := http.Get("http://" + addr + "/health") //nolint:gosec
		if reqErr == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(httpReadyDeadline) {
			t.Fatalf("health endpoint did not come up in time: %v", reqErr)
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("health server did not stop after context cancel")
	}
}
