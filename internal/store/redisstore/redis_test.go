package redisstore

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestOpenBoundsStalledRedisPing(t *testing.T) {
	address, stop := startStalledRedis(t)
	defer stop()

	started := time.Now()
	client, err := open(context.Background(), address, "", 0, 50*time.Millisecond)
	if client != nil {
		_ = client.Close()
	}
	if err == nil {
		t.Fatal("open() succeeded against a stalled Redis endpoint")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("open() took %s, want at most 1s", elapsed)
	}
}

func TestOpenPreservesEarlierCallerDeadline(t *testing.T) {
	address, stop := startStalledRedis(t)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	client, err := open(ctx, address, "", 0, time.Second)
	if client != nil {
		_ = client.Close()
	}
	if err == nil {
		t.Fatal("open() succeeded against a stalled Redis endpoint")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("open() took %s, want at most 1s", elapsed)
	}
}

func startStalledRedis(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				<-stop
				_ = conn.Close()
			}()
		}
	}()
	return listener.Addr().String(), func() {
		close(stop)
		_ = listener.Close()
		<-done
	}
}
