package main

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"testing"
	"time"
)

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
