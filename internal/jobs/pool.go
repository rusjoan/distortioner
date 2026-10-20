// Package jobs runs distortion jobs on a fixed set of workers, serving
// chats round-robin so that one busy chat cannot starve the others.
package jobs

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

var (
	ErrQueueFull = errors.New("queue is full")
	ErrChatLimit = errors.New("too many jobs from this chat")
	ErrClosed    = errors.New("pool is shut down")
	// ErrCanceled is the cause of a job canceled by the user or an admin.
	ErrCanceled = errors.New("job canceled")
	// ErrShutdown is the cause of a job dropped or interrupted by shutdown.
	ErrShutdown = errors.New("bot is shutting down")
)

// PanicError wraps a panic recovered from a job.
type PanicError struct{ Value any }

func (e *PanicError) Error() string { return fmt.Sprintf("job panicked: %v", e.Value) }

// Job is a unit of work. Done is called exactly once: with Run's result,
// with a *PanicError, or with ErrCanceled/ErrShutdown if the job never ran.
type Job struct {
	ChatID int64
	Run    func(ctx context.Context) error
	Done   func(err error)
	// Cost is the size of the work, e.g. seconds of media; it scales the
	// time estimates. Zero counts as 1.
	Cost float64

	id      uint64
	ctx     context.Context
	cancel  context.CancelCauseFunc
	started time.Time
}

func (j *Job) cost() float64 { return max(j.Cost, 1) }

// ID returns the id assigned by Submit.
func (j *Job) ID() uint64 { return j.id }

// Limits are read on every Submit, so they can change at runtime.
type Limits struct {
	MaxQueued  func() int
	MaxPerChat func() int
	IsPriority func(chatID int64) bool
}

// Pool is a fair job queue with a fixed number of workers.
type Pool struct {
	workers int
	timeout time.Duration
	limits  Limits

	mu      sync.Mutex
	cond    *sync.Cond
	queues  map[int64][]*Job // pending jobs per chat, FIFO
	normal  []int64          // chats with pending jobs, round-robin order
	prio    []int64          // priority chats with pending jobs
	queued  int
	running map[uint64]*Job
	perChat map[int64]int // queued + running
	nextID  uint64
	avg     time.Duration // moving average of job duration
	rate    float64       // moving average of seconds of work per unit of cost
	closed  bool

	baseCtx context.Context
	stop    context.CancelCauseFunc
	wg      sync.WaitGroup
}

// New starts a pool. timeout bounds each job's run time (0 means none).
func New(workers int, timeout time.Duration, limits Limits) *Pool {
	ctx, stop := context.WithCancelCause(context.Background())
	p := &Pool{
		workers: workers,
		timeout: timeout,
		limits:  limits,
		queues:  map[int64][]*Job{},
		running: map[uint64]*Job{},
		perChat: map[int64]int{},
		baseCtx: ctx,
		stop:    stop,
	}
	p.cond = sync.NewCond(&p.mu)
	p.wg.Add(workers)
	for range workers {
		go p.worker()
	}
	return p
}

// Workers returns the number of workers.
func (p *Pool) Workers() int { return p.workers }

// Submit enqueues a job and returns its id.
func (p *Pool) Submit(job *Job) (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, ErrClosed
	}
	priority := p.limits.IsPriority != nil && p.limits.IsPriority(job.ChatID)
	if !priority {
		if p.limits.MaxQueued != nil && p.queued >= p.limits.MaxQueued() {
			return 0, ErrQueueFull
		}
		if p.limits.MaxPerChat != nil && p.perChat[job.ChatID] >= p.limits.MaxPerChat() {
			return 0, ErrChatLimit
		}
	}

	p.nextID++
	job.id = p.nextID
	job.ctx, job.cancel = context.WithCancelCause(p.baseCtx)

	q := p.queues[job.ChatID]
	if len(q) == 0 {
		if priority {
			p.prio = append(p.prio, job.ChatID)
		} else {
			p.normal = append(p.normal, job.ChatID)
		}
	}
	p.queues[job.ChatID] = append(q, job)
	p.queued++
	p.perChat[job.ChatID]++
	p.cond.Signal()
	return job.id, nil
}

// pop takes the next job: priority chats first, then round-robin. Caller holds mu.
func (p *Pool) pop() *Job {
	ring := &p.prio
	if len(*ring) == 0 {
		ring = &p.normal
	}
	if len(*ring) == 0 {
		return nil
	}
	chat := (*ring)[0]
	q := p.queues[chat]
	job := q[0]
	q[0] = nil
	q = q[1:]
	*ring = (*ring)[1:]
	if len(q) == 0 {
		delete(p.queues, chat)
	} else {
		p.queues[chat] = q
		*ring = append(*ring, chat) // back of the line until everyone had a turn
	}
	p.queued--
	return job
}

func (p *Pool) worker() {
	defer p.wg.Done()
	for {
		p.mu.Lock()
		for p.queued == 0 && !p.closed {
			p.cond.Wait()
		}
		job := p.pop()
		if job == nil { // closed and drained
			p.mu.Unlock()
			return
		}
		job.started = time.Now()
		p.running[job.id] = job
		p.mu.Unlock()

		err := p.exec(job)

		p.mu.Lock()
		delete(p.running, job.id)
		p.release(job.ChatID)
		if err == nil {
			p.observe(time.Since(job.started), job.cost())
		}
		p.mu.Unlock()

		job.cancel(nil)
		job.Done(err)
	}
}

func (p *Pool) exec(job *Job) (err error) {
	ctx := job.ctx
	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Value: r}
		}
	}()
	err = job.Run(ctx)
	// Report why the job was interrupted rather than a bare context.Canceled.
	if err != nil && errors.Is(err, context.Canceled) {
		if cause := context.Cause(job.ctx); cause != nil {
			err = cause
		}
	}
	return err
}

// observe updates the moving average used for ETAs. Caller holds mu.
func (p *Pool) observe(d time.Duration, cost float64) {
	r := d.Seconds() / cost
	if p.avg == 0 {
		p.avg, p.rate = d, r
		return
	}
	p.avg = (p.avg*4 + d) / 5
	p.rate = (p.rate*4 + r) / 5
}

func (p *Pool) release(chat int64) {
	if p.perChat[chat] <= 1 {
		delete(p.perChat, chat)
	} else {
		p.perChat[chat]--
	}
}

// Position returns the 1-based place of a pending job in line and an
// estimate of the wait. ok is false if the job is not pending any more.
func (p *Pool) Position(id uint64) (pos int, eta time.Duration, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.positionLocked(id)
}

func (p *Pool) positionLocked(id uint64) (pos int, eta time.Duration, ok bool) {

	var chat int64
	idx := -1
	for c, q := range p.queues {
		for i, j := range q {
			if j.id == id {
				chat, idx = c, i
				break
			}
		}
		if idx >= 0 {
			break
		}
	}
	if idx < 0 {
		return 0, 0, false
	}

	// Each chat in a ring gets one turn per round and this job is served in
	// round idx+1: chats up to and including ours in ring order contribute
	// idx+1 jobs at most, chats after ours only idx.
	var before []*Job // served before ours, ours included
	collect := func(ring []int64, all bool) {
		rounds := idx + 1
		for _, c := range ring {
			q := p.queues[c]
			if !all {
				q = q[:min(len(q), rounds)]
			}
			before = append(before, q...)
			if c == chat {
				rounds = idx
			}
		}
	}
	if slices.Contains(p.prio, chat) {
		collect(p.prio, false)
	} else {
		collect(p.prio, true)
		collect(p.normal, false)
	}
	pos = len(before)
	return pos, p.estimate(before, id), true
}

// Waits reports, atomically with its place in line, whether a pending job
// really waits: it does not when a worker is free to take it right away.
func (p *Pool) Waits(id uint64) (pos int, eta time.Duration, waits bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pos, eta, ok := p.positionLocked(id)
	if !ok {
		return 0, 0, false
	}
	return pos, eta, len(p.running)+pos > p.workers
}

// estimate is the time until the job is done: the work of the jobs served
// before it and what is left of the running ones, shared by the workers,
// plus its own work. Work is cost × the learned rate. Caller holds mu.
func (p *Pool) estimate(before []*Job, id uint64) time.Duration {
	if p.rate == 0 {
		return 0
	}
	var work, own float64
	for _, j := range before {
		if j.id == id {
			own = j.cost() * p.rate
			continue
		}
		work += j.cost() * p.rate
	}
	for _, j := range p.running {
		work += max(j.cost()*p.rate-time.Since(j.started).Seconds(), 0)
	}
	return time.Duration((work/float64(p.workers) + own) * float64(time.Second))
}

// Cancel cancels a pending or running job. It reports whether it was found.
func (p *Pool) Cancel(id uint64) bool {
	p.mu.Lock()
	if job, ok := p.running[id]; ok {
		p.mu.Unlock()
		job.cancel(ErrCanceled)
		return true
	}
	for chat, q := range p.queues {
		for i, job := range q {
			if job.id == id {
				p.removeQueued(chat, i)
				p.mu.Unlock()
				job.cancel(ErrCanceled)
				job.Done(ErrCanceled)
				return true
			}
		}
	}
	p.mu.Unlock()
	return false
}

// CancelChat cancels every job of a chat and returns how many were affected.
func (p *Pool) CancelChat(chat int64) int {
	p.mu.Lock()
	// Clone: removeQueued shifts and clears the underlying array.
	dropped := slices.Clone(p.queues[chat])
	for range dropped {
		p.removeQueued(chat, 0)
	}
	var running []*Job
	for _, job := range p.running {
		if job.ChatID == chat {
			running = append(running, job)
		}
	}
	p.mu.Unlock()

	for _, job := range dropped {
		job.cancel(ErrCanceled)
		job.Done(ErrCanceled)
	}
	for _, job := range running {
		job.cancel(ErrCanceled)
	}
	return len(dropped) + len(running)
}

// removeQueued removes q[i] of chat. Caller holds mu.
func (p *Pool) removeQueued(chat int64, i int) {
	q := slices.Delete(p.queues[chat], i, i+1)
	p.queued--
	p.release(chat)
	if len(q) > 0 {
		p.queues[chat] = q
		return
	}
	delete(p.queues, chat)
	p.normal = slices.DeleteFunc(p.normal, func(c int64) bool { return c == chat })
	p.prio = slices.DeleteFunc(p.prio, func(c int64) bool { return c == chat })
}

// ChatLoad is the number of jobs of one chat.
type ChatLoad struct {
	ChatID  int64
	Queued  int
	Running int
}

// Snapshot describes the pool state.
type Snapshot struct {
	Queued  int
	Running int
	Workers int
	AvgRun  time.Duration
	Chats   []ChatLoad // busiest first
}

// Snapshot returns the current state.
func (p *Pool) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	loads := map[int64]*ChatLoad{}
	get := func(c int64) *ChatLoad {
		if loads[c] == nil {
			loads[c] = &ChatLoad{ChatID: c}
		}
		return loads[c]
	}
	for c, q := range p.queues {
		get(c).Queued = len(q)
	}
	for _, j := range p.running {
		get(j.ChatID).Running++
	}
	s := Snapshot{Queued: p.queued, Running: len(p.running), Workers: p.workers, AvgRun: p.avg}
	for _, l := range loads {
		s.Chats = append(s.Chats, *l)
	}
	slices.SortFunc(s.Chats, func(a, b ChatLoad) int {
		if n := cmp.Compare(b.Queued+b.Running, a.Queued+a.Running); n != 0 {
			return n
		}
		return cmp.Compare(a.ChatID, b.ChatID)
	})
	return s
}

// Shutdown stops accepting jobs and drops pending ones with ErrShutdown.
// Running jobs get until ctx is done to finish; then they are canceled.
func (p *Pool) Shutdown(ctx context.Context) {
	p.mu.Lock()
	p.closed = true
	var dropped []*Job
	for _, q := range p.queues {
		dropped = append(dropped, q...)
	}
	for _, job := range dropped {
		p.release(job.ChatID)
	}
	p.queues, p.normal, p.prio, p.queued = map[int64][]*Job{}, nil, nil, 0
	p.cond.Broadcast()
	p.mu.Unlock()

	for _, job := range dropped {
		job.cancel(ErrShutdown)
		job.Done(ErrShutdown)
	}

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		p.stop(ErrShutdown)
		<-done
	}
}
