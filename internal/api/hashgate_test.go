package api

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"rivosnap/internal/config"
)

func newGateServer(wait time.Duration) *Server {
	s := NewServer(config.Env{}, nil, nil, nil)
	s.hashWait = wait
	return s
}

func TestAcquireHashWaitsThenGivesUp(t *testing.T) {
	s := newGateServer(50 * time.Millisecond)
	releaseFirst, ok := s.acquireHash(context.Background())
	if !ok {
		t.Fatal("first slot must be free")
	}
	releaseSecond, ok := s.acquireHash(context.Background())
	if !ok {
		t.Fatal("second slot must be free")
	}
	if _, ok := s.acquireHash(context.Background()); ok {
		t.Fatal("third hash acquired while both slots are held")
	}
	releaseFirst()
	releaseThird, ok := s.acquireHash(context.Background())
	if !ok {
		t.Fatal("released slot must be acquirable")
	}
	releaseSecond()
	releaseThird()
}

func TestAcquireHashStopsOnCancelledRequest(t *testing.T) {
	s := newGateServer(time.Minute)
	releaseFirst, _ := s.acquireHash(context.Background())
	releaseSecond, _ := s.acquireHash(context.Background())
	defer releaseFirst()
	defer releaseSecond()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, ok := s.acquireHash(ctx); ok {
		t.Fatal("cancelled request acquired a hash slot")
	}
	if waited := time.Since(start); waited > time.Second {
		t.Fatalf("cancelled request waited %s", waited)
	}
}

func TestAcquireHashPeakConcurrencyIsTwo(t *testing.T) {
	s := newGateServer(10 * time.Second)
	var current, peak atomic.Int32
	var busy atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			release, ok := s.acquireHash(context.Background())
			if !ok {
				busy.Add(1)
				return
			}
			n := current.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			current.Add(-1)
			release()
		})
	}
	wg.Wait()
	if busy.Load() != 0 {
		t.Fatalf("%d hashes gave up waiting for a slot", busy.Load())
	}
	if got := peak.Load(); got != 2 {
		t.Fatalf("peak concurrent hashes = %d, want 2", got)
	}
}
