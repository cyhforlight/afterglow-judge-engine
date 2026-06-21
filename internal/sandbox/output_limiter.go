package sandbox

import (
	"bytes"
	"sync"
)

// outputLimiter holds a shared byte budget for all associated limitedWriters.
type outputLimiter struct {
	ch    chan struct{}
	once  sync.Once
	mu    sync.Mutex
	limit int64
	used  int64
}

func newOutputLimiter(maxBytes int64) *outputLimiter {
	return &outputLimiter{ch: make(chan struct{}), limit: maxBytes}
}

func (l *outputLimiter) signal() {
	l.once.Do(func() { close(l.ch) })
}

func (l *outputLimiter) reserve(requested int64) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	remaining := l.limit - l.used
	if remaining <= 0 {
		return 0
	}
	granted := min(requested, remaining)
	l.used += granted
	return granted
}

// limitedWriter buffers output while drawing bytes from the shared outputLimiter pool.
type limitedWriter struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	overflowed bool
	limiter    *outputLimiter
}

func newLimitedWriter(limiter *outputLimiter) *limitedWriter {
	return &limitedWriter{limiter: limiter}
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n := len(p)
	if w.overflowed {
		return n, nil
	}

	allowed := w.limiter.reserve(int64(n))
	if allowed > 0 {
		_, _ = w.buf.Write(p[:allowed])
	}
	if allowed < int64(n) {
		w.overflowed = true
		w.limiter.signal()
	}
	return n, nil
}

func (w *limitedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *limitedWriter) isOverflowed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflowed
}
