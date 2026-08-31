package ratelimit

import (
	"context"
	"sync"
	"time"
)

type memoryBucket struct {
	window int64
	count  int64
}

type MemoryLimiter struct {
	mu      sync.Mutex
	buckets map[string]*memoryBucket
}

func NewMemoryLimiter() *MemoryLimiter {
	return &MemoryLimiter{buckets: make(map[string]*memoryBucket)}
}

func (m *MemoryLimiter) Allow(_ context.Context, key string, limit int, window time.Duration) (Result, error) {
	if window <= 0 {
		window = time.Minute
	}
	if limit <= 0 {
		return Result{Allowed: false, Remaining: 0}, nil
	}

	idx := time.Now().UnixNano() / int64(window)
	bucketKey := key + ":" + window.String() + ":" + itoa(idx)

	m.mu.Lock()
	defer m.mu.Unlock()

	b := m.buckets[bucketKey]
	if b == nil || b.window != idx {
		b = &memoryBucket{window: idx}
		m.buckets[bucketKey] = b
	}
	b.count++

	remaining := int64(limit) - b.count
	if remaining < 0 {
		remaining = 0
	}
	allowed := b.count <= int64(limit)

	res := Result{
		Allowed:   allowed,
		Remaining: int(remaining),
	}
	if !allowed {
		res.RetryAfter = window - time.Duration(time.Now().UnixNano()%int64(window))
	}
	return res, nil
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
