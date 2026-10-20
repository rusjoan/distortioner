package bot

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Telegram delivers an album as separate messages sharing a media group id.
const albumWait = 1500 * time.Millisecond

// albumCollector groups the messages of an album and hands them over once
// no new part arrived for a short while.
type albumCollector struct {
	wait  time.Duration
	flush func(ctx context.Context, parts []request)

	mu     sync.Mutex
	groups map[string]*albumGroup
}

type albumGroup struct {
	parts []request
	timer *time.Timer
}

func newAlbumCollector(wait time.Duration, flush func(context.Context, []request)) *albumCollector {
	return &albumCollector{wait: wait, flush: flush, groups: map[string]*albumGroup{}}
}

func (c *albumCollector) add(groupID string, r request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g, ok := c.groups[groupID]
	if !ok {
		g = &albumGroup{}
		c.groups[groupID] = g
		g.timer = time.AfterFunc(c.wait, func() { c.fire(groupID) })
	} else {
		g.timer.Reset(c.wait)
	}
	g.parts = append(g.parts, r)
}

func (c *albumCollector) fire(groupID string) {
	c.mu.Lock()
	g := c.groups[groupID]
	delete(c.groups, groupID)
	c.mu.Unlock()
	if g == nil {
		return
	}
	sort.Slice(g.parts, func(i, j int) bool { return g.parts[i].source.ID < g.parts[j].source.ID })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c.flush(ctx, g.parts)
}

// processAlbum distorts the photos and videos of an album as one job and
// replies with an album. Anything else in it is processed on its own.
func (b *Bot) processAlbum(ctx context.Context, parts []request) {
	var items []item
	var first *request
	for i := range parts {
		r := parts[i]
		it, ok, _ := classify(r.source)
		if ok && (it.kind == KindPhoto || it.kind == KindVideo) {
			items = append(items, it)
			if first == nil {
				first = &parts[i]
			}
			continue
		}
		b.process(ctx, r)
	}
	switch {
	case len(items) == 0:
		return
	case len(items) == 1:
		b.submit(ctx, *first, items[0].kind, items)
	default:
		b.submit(ctx, *first, KindAlbum, items)
	}
}
