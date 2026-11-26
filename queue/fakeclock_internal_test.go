package queue

import (
	"sync"
	"time"
)

var clockStart = time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)

type fakeClock struct {
	mutex   sync.Mutex
	current time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{current: clockStart}
}

func (c *fakeClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.current
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.current = c.current.Add(d)
}
