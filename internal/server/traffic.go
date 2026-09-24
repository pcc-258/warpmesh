package server

import (
	"sync"
	"time"
)

// trafficRecorder batches byte counters so desktop frame forwarding does not issue
// a SQLite write for every WebSocket frame.
type trafficRecorder struct {
	mu         sync.Mutex
	registry   *Registry
	id         string
	toDevice   int64
	fromDevice int64
	lastFlush  time.Time
}

func newTrafficRecorder(registry *Registry, id string) *trafficRecorder {
	return &trafficRecorder{registry: registry, id: id, lastFlush: time.Now()}
}

func (t *trafficRecorder) Add(toDevice, fromDevice int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.toDevice += toDevice
	t.fromDevice += fromDevice
	if time.Since(t.lastFlush) >= time.Second || t.toDevice+t.fromDevice >= 1024*1024 {
		t.flushLocked()
	}
}

func (t *trafficRecorder) flushLocked() {
	if t.toDevice == 0 && t.fromDevice == 0 {
		return
	}
	_ = t.registry.AddConnectionTraffic(t.id, t.toDevice, t.fromDevice)
	t.toDevice = 0
	t.fromDevice = 0
	t.lastFlush = time.Now()
}

func (t *trafficRecorder) Close(state string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushLocked()
	_ = t.registry.EndConnection(t.id, state)
}
