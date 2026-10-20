package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// gate blocks the single worker so that tests can arrange the queue.
type gate struct {
	started chan struct{}
	release chan struct{}
}

func newGate() *gate { return &gate{started: make(chan struct{}), release: make(chan struct{})} }

func (g *gate) job(chat int64) *Job {
	return &Job{
		ChatID: chat,
		Run: func(ctx context.Context) error {
			close(g.started)
			select {
			case <-g.release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		Done: func(error) {},
	}
}

type recorder struct {
	mu    sync.Mutex
	order []int64
	errs  map[int64][]error
	wg    sync.WaitGroup
}

func newRecorder() *recorder { return &recorder{errs: map[int64][]error{}} }

func (r *recorder) job(chat int64) *Job {
	r.wg.Add(1)
	return &Job{
		ChatID: chat,
		Run: func(context.Context) error {
			r.mu.Lock()
			r.order = append(r.order, chat)
			r.mu.Unlock()
			return nil
		},
		Done: func(err error) {
			r.mu.Lock()
			r.errs[chat] = append(r.errs[chat], err)
			r.mu.Unlock()
			r.wg.Done()
		},
	}
}

func mustSubmit(t *testing.T, p *Pool, j *Job) uint64 {
	t.Helper()
	id, err := p.Submit(j)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRoundRobinFairness(t *testing.T) {
	p := New(1, 0, Limits{})
	defer p.Shutdown(context.Background())
	g := newGate()
	mustSubmit(t, p, g.job(0))
	<-g.started

	r := newRecorder()
	for _, chat := range []int64{1, 1, 1, 3, 2, 2} {
		mustSubmit(t, p, r.job(chat))
	}
	close(g.release)
	r.wg.Wait()

	want := []int64{1, 3, 2, 1, 2, 1}
	if !equal(r.order, want) {
		t.Fatalf("order %v, want %v", r.order, want)
	}
}

func TestPriorityChatsGoFirst(t *testing.T) {
	p := New(1, 0, Limits{IsPriority: func(c int64) bool { return c == 9 }})
	defer p.Shutdown(context.Background())
	g := newGate()
	mustSubmit(t, p, g.job(0))
	<-g.started

	r := newRecorder()
	for _, chat := range []int64{1, 2, 9, 9} {
		mustSubmit(t, p, r.job(chat))
	}
	close(g.release)
	r.wg.Wait()
	if want := []int64{9, 9, 1, 2}; !equal(r.order, want) {
		t.Fatalf("order %v, want %v", r.order, want)
	}
}

func TestLimits(t *testing.T) {
	p := New(1, 0, Limits{
		MaxQueued:  func() int { return 3 },
		MaxPerChat: func() int { return 2 },
		IsPriority: func(c int64) bool { return c == 9 },
	})
	defer p.Shutdown(context.Background())
	g := newGate()
	mustSubmit(t, p, g.job(1)) // running, counts towards chat 1
	<-g.started

	mustSubmit(t, p, &Job{ChatID: 1, Run: noop, Done: func(error) {}})
	if _, err := p.Submit(&Job{ChatID: 1, Run: noop, Done: func(error) {}}); !errors.Is(err, ErrChatLimit) {
		t.Fatalf("expected chat limit, got %v", err)
	}
	mustSubmit(t, p, &Job{ChatID: 2, Run: noop, Done: func(error) {}})
	mustSubmit(t, p, &Job{ChatID: 3, Run: noop, Done: func(error) {}})
	if _, err := p.Submit(&Job{ChatID: 4, Run: noop, Done: func(error) {}}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected queue full, got %v", err)
	}
	// Priority chats bypass both limits.
	mustSubmit(t, p, &Job{ChatID: 9, Run: noop, Done: func(error) {}})
	close(g.release)
}

func TestRejectedSubmitDoesNotChangeCounters(t *testing.T) {
	p := New(1, 0, Limits{MaxPerChat: func() int { return 1 }})
	defer p.Shutdown(context.Background())
	g := newGate()
	mustSubmit(t, p, g.job(1))
	<-g.started
	for range 10 {
		if _, err := p.Submit(&Job{ChatID: 1, Run: noop, Done: func(error) {}}); !errors.Is(err, ErrChatLimit) {
			t.Fatalf("got %v", err)
		}
	}
	close(g.release)
	r := newRecorder()
	waitFor(t, func() bool { return p.Snapshot().Running == 0 })
	mustSubmit(t, p, r.job(1))
	r.wg.Wait()
}

func TestCancelQueuedAndRunning(t *testing.T) {
	p := New(1, 0, Limits{})
	defer p.Shutdown(context.Background())

	runErr := make(chan error, 1)
	started := make(chan struct{})
	running := &Job{
		ChatID: 1,
		Run: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
		Done: func(err error) { runErr <- err },
	}
	runID := mustSubmit(t, p, running)
	<-started

	queuedErr := make(chan error, 1)
	queuedID := mustSubmit(t, p, &Job{ChatID: 2, Run: noop, Done: func(err error) { queuedErr <- err }})
	if pos, _, ok := p.Position(queuedID); !ok || pos != 1 {
		t.Fatalf("position %d ok=%v", pos, ok)
	}

	if !p.Cancel(queuedID) || !errors.Is(<-queuedErr, ErrCanceled) {
		t.Fatal("queued job not canceled")
	}
	if !p.Cancel(runID) || !errors.Is(<-runErr, ErrCanceled) {
		t.Fatal("running job not canceled with ErrCanceled cause")
	}
	if p.Cancel(12345) {
		t.Fatal("unknown job canceled")
	}
}

func TestCancelChat(t *testing.T) {
	p := New(1, 0, Limits{})
	defer p.Shutdown(context.Background())
	g := newGate()
	mustSubmit(t, p, g.job(5))
	<-g.started
	r := newRecorder()
	mustSubmit(t, p, r.job(5))
	mustSubmit(t, p, r.job(6))
	if n := p.CancelChat(5); n != 2 {
		t.Fatalf("canceled %d", n)
	}
	r.wg.Wait()
	if !errors.Is(r.errs[5][0], ErrCanceled) || r.errs[6][0] != nil {
		t.Fatalf("errs %v", r.errs)
	}
	if s := p.Snapshot(); s.Queued != 0 {
		t.Fatalf("snapshot %+v", s)
	}
}

func TestPanicIsRecovered(t *testing.T) {
	p := New(1, 0, Limits{})
	defer p.Shutdown(context.Background())
	errc := make(chan error, 1)
	mustSubmit(t, p, &Job{ChatID: 1, Run: func(context.Context) error { panic("boom") }, Done: func(err error) { errc <- err }})
	var pe *PanicError
	if err := <-errc; !errors.As(err, &pe) {
		t.Fatalf("got %v", err)
	}
	// The worker survived.
	r := newRecorder()
	mustSubmit(t, p, r.job(2))
	r.wg.Wait()
}

func TestTimeout(t *testing.T) {
	p := New(1, 50*time.Millisecond, Limits{})
	defer p.Shutdown(context.Background())
	errc := make(chan error, 1)
	mustSubmit(t, p, &Job{ChatID: 1, Run: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}, Done: func(err error) { errc <- err }})
	if err := <-errc; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestShutdownGraceAndForce(t *testing.T) {
	p := New(1, 0, Limits{})
	errc := make(chan error, 2)
	started := make(chan struct{})
	mustSubmit(t, p, &Job{ChatID: 1, Run: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, Done: func(err error) { errc <- err }})
	<-started
	mustSubmit(t, p, &Job{ChatID: 2, Run: noop, Done: func(err error) { errc <- err }})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	p.Shutdown(ctx)

	for range 2 {
		if err := <-errc; !errors.Is(err, ErrShutdown) {
			t.Fatalf("got %v", err)
		}
	}
	if _, err := p.Submit(&Job{ChatID: 3, Run: noop, Done: func(error) {}}); !errors.Is(err, ErrClosed) {
		t.Fatalf("got %v", err)
	}
}

func TestShutdownWaitsForRunningJobs(t *testing.T) {
	p := New(2, 0, Limits{})
	finished := make(chan struct{})
	started := make(chan struct{})
	mustSubmit(t, p, &Job{ChatID: 1, Run: func(context.Context) error {
		close(started)
		time.Sleep(30 * time.Millisecond)
		return nil
	}, Done: func(err error) {
		if err != nil {
			t.Errorf("got %v", err)
		}
		close(finished)
	}})
	<-started
	p.Shutdown(context.Background())
	select {
	case <-finished:
	default:
		t.Fatal("Shutdown returned before the running job finished")
	}
}

func TestPositionAndETA(t *testing.T) {
	p := New(1, 0, Limits{})
	defer p.Shutdown(context.Background())

	// Teach the pool the average duration.
	r := newRecorder()
	mustSubmit(t, p, r.job(0))
	r.wg.Wait()

	g := newGate()
	mustSubmit(t, p, g.job(0))
	<-g.started
	a1 := mustSubmit(t, p, &Job{ChatID: 1, Run: noop, Done: func(error) {}})
	a2 := mustSubmit(t, p, &Job{ChatID: 1, Run: noop, Done: func(error) {}})
	b1 := mustSubmit(t, p, &Job{ChatID: 2, Run: noop, Done: func(error) {}})

	for id, want := range map[uint64]int{a1: 1, b1: 2, a2: 3} {
		if pos, _, _ := p.Position(id); pos != want {
			t.Errorf("job %d position %d, want %d", id, pos, want)
		}
	}
	close(g.release)
}

func TestConcurrentUse(t *testing.T) {
	p := New(4, 0, Limits{MaxPerChat: func() int { return 1000 }})
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	for c := range int64(8) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				id, err := p.Submit(&Job{ChatID: c, Run: noop, Done: func(error) {
					mu.Lock()
					done++
					mu.Unlock()
				}})
				if err != nil {
					t.Error(err)
					return
				}
				p.Position(id)
				p.Snapshot()
			}
		}()
	}
	wg.Wait()
	p.Shutdown(context.Background())
	if done != 400 {
		t.Fatalf("done %d", done)
	}
}

func noop(context.Context) error { return nil }

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(time.Millisecond)
	}
}

// The estimate scales with the size of each job, not with how many jobs
// there are: a long video ahead weighs more than a short one.
func TestEstimateIsNormalisedByCost(t *testing.T) {
	p := New(1, 0, Limits{})
	defer p.Shutdown(context.Background())
	g := newGate()
	running := g.job(0)
	running.Cost = 10
	mustSubmit(t, p, running)
	<-g.started
	defer close(g.release)

	p.mu.Lock()
	p.rate = 1 // a second of work per second of media
	p.mu.Unlock()

	long := mustSubmit(t, p, &Job{ChatID: 1, Cost: 20, Run: noop, Done: func(error) {}})
	short := mustSubmit(t, p, &Job{ChatID: 2, Cost: 5, Run: noop, Done: func(error) {}})

	near := func(got, want time.Duration) bool { return got > want-time.Second && got <= want }
	if _, eta, _ := p.Position(long); !near(eta, 30*time.Second) { // 10 left running + 20 own
		t.Errorf("long: %v", eta)
	}
	if _, eta, _ := p.Position(short); !near(eta, 35*time.Second) { // 10 + 20 ahead + 5 own
		t.Errorf("short: %v", eta)
	}
}

func TestEstimateSharesWorkBetweenWorkers(t *testing.T) {
	p := New(2, 0, Limits{})
	defer p.Shutdown(context.Background())
	g1, g2 := newGate(), newGate()
	mustSubmit(t, p, g1.job(0))
	mustSubmit(t, p, g2.job(0))
	<-g1.started
	<-g2.started
	defer close(g1.release)
	defer close(g2.release)

	p.mu.Lock()
	p.rate = 1
	p.mu.Unlock()
	a := mustSubmit(t, p, &Job{ChatID: 1, Cost: 10, Run: noop, Done: func(error) {}})
	b := mustSubmit(t, p, &Job{ChatID: 2, Cost: 10, Run: noop, Done: func(error) {}})
	// Running gates cost 1 each: (1 + 1 + 10) / 2 workers + 10 own = 16 s.
	if _, eta, _ := p.Position(b); eta <= 15*time.Second || eta > 16*time.Second {
		t.Errorf("b: %v", eta)
	}
	if _, eta, _ := p.Position(a); eta <= 10*time.Second || eta > 11*time.Second {
		t.Errorf("a: %v", eta)
	}
}

func TestNoEstimateBeforeAnyJobFinished(t *testing.T) {
	p := New(1, 0, Limits{})
	defer p.Shutdown(context.Background())
	g := newGate()
	mustSubmit(t, p, g.job(0))
	<-g.started
	defer close(g.release)
	id := mustSubmit(t, p, &Job{ChatID: 1, Cost: 30, Run: noop, Done: func(error) {}})
	if pos, eta, ok := p.Position(id); !ok || pos != 1 || eta != 0 {
		t.Fatalf("pos %d eta %v ok %v", pos, eta, ok)
	}
}
