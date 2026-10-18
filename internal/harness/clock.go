package harness

import (
	"sync"
	"time"

	"github.com/jaishankarh/system-telemtery-oracle/internal/machine"
)

// FakeClock is a controllable machine.Clock. Time moves only through Advance
// or Sleep.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*fakeTicker
	// OnSleep, if set, runs each time the program sleeps (after time advances);
	// use it to mutate the fake machine between two samples of `report`.
	OnSleep func()
}

// NewFakeClock starts a clock at t.
func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{now: t} }

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves time forward, firing every ticker whose period elapsed. It
// blocks until the program has received each tick.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	ts := append([]*fakeTicker(nil), c.tickers...)
	c.mu.Unlock()
	for _, t := range ts {
		t.fire(now)
	}
}

func (c *FakeClock) Sleep(d time.Duration) {
	c.Advance(d)
	if c.OnSleep != nil {
		c.OnSleep()
	}
}

func (c *FakeClock) NewTicker(d time.Duration) machine.Ticker {
	t := &fakeTicker{ch: make(chan time.Time), stop: make(chan struct{})}
	c.mu.Lock()
	c.tickers = append(c.tickers, t)
	c.mu.Unlock()
	return t
}

type fakeTicker struct {
	ch   chan time.Time
	stop chan struct{}
	once sync.Once
}

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop()               { t.once.Do(func() { close(t.stop) }) }
func (t *fakeTicker) fire(now time.Time) {
	select {
	case t.ch <- now:
	case <-t.stop:
	case <-time.After(5 * time.Second):
	}
}
