package limiter

import (
	"testing"
	"time"
)

func TestBurstAndRefill(t *testing.T) {
	l := New(func() int { return 3 }, func() time.Duration { return 30 * time.Second })
	now := time.Unix(1000, 0)
	for i := range 3 {
		if ok, _ := l.Allow(1, now); !ok {
			t.Fatalf("request %d rejected", i)
		}
	}
	ok, retry := l.Allow(1, now)
	if ok || retry != 11*time.Second {
		t.Fatalf("ok=%v retry=%v", ok, retry)
	}
	// Other keys are independent.
	if ok, _ := l.Allow(2, now); !ok {
		t.Fatal("independent key rejected")
	}
	// One token refills every 10 s.
	if ok, _ := l.Allow(1, now.Add(10*time.Second)); !ok {
		t.Fatal("token not refilled")
	}
	if ok, _ := l.Allow(1, now.Add(10*time.Second)); ok {
		t.Fatal("only one token should have been refilled")
	}
}

func TestSweepForgetsIdleKeys(t *testing.T) {
	l := New(func() int { return 1 }, func() time.Duration { return time.Second })
	now := time.Unix(0, 0)
	for k := range int64(sweepEvery - 1) {
		l.Allow(k, now)
	}
	l.Allow(-1, now.Add(time.Hour))
	if n := l.Len(); n != 1 {
		t.Fatalf("%d keys left", n)
	}
}

func TestLimitsChangeAtRuntime(t *testing.T) {
	burst := 1
	l := New(func() int { return burst }, func() time.Duration { return time.Minute })
	now := time.Unix(0, 0)
	l.Allow(1, now)
	if ok, _ := l.Allow(1, now); ok {
		t.Fatal("should be limited")
	}
	burst = 100
	if ok, _ := l.Allow(1, now.Add(time.Second)); !ok {
		t.Fatal("new limit not applied")
	}
}
