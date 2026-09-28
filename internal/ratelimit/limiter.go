// Package ratelimit implements a tiny in-memory fixed-window limiter,
// enough to slow down brute-force attempts against a single-tenant,
// self-hosted server without pulling in an external dependency.
package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	count     int
	windowEnd time.Time
}

type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	limit   int
	window  time.Duration
}

func New(limit int, window time.Duration) *Limiter {
	l := &Limiter{
		buckets: make(map[string]*bucket),
		limit:   limit,
		window:  window,
	}
	go l.gc()
	return l
}

// Allow reports whether the given key (typically client IP, optionally
// combined with a username) may proceed.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[key]
	if !ok || now.After(b.windowEnd) {
		l.buckets[key] = &bucket{count: 1, windowEnd: now.Add(l.window)}
		return true
	}
	if b.count >= l.limit {
		return false
	}
	b.count++
	return true
}

func (l *Limiter) gc() {
	for range time.Tick(time.Minute) {
		l.mu.Lock()
		now := time.Now()
		for k, b := range l.buckets {
			if now.After(b.windowEnd) {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}
