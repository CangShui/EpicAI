// Package ratelimit implements EpicAI's throughput simulation system:
// per-session token buckets, a global fair-share scheduler, smooth/burst modes
// and a truly uncapped mode that never sleeps.
package ratelimit

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/epicai/epicai/backend/internal/config"
)

// Limiter coordinates all token accounting for output tokens.
type Limiter struct {
	mu       sync.RWMutex
	sessions map[string]*SessionBucket
	global   *bucket
	// rolling throughput stats
	windowStart  time.Time
	windowTokens int64
	currentRate  float64
	avg1m        float64
	peak         float64
	samples      []sample
	totalTokens  int64
}

type sample struct {
	at     time.Time
	tokens int64
}

// SessionBucket is the per-session limiter.
type SessionBucket struct {
	id string
	b  *bucket
	mu sync.Mutex
	// configured rate in tokens/s; 0 = unlimited
	rate      int64
	burstSec  float64
	mode      string // smooth|burst|unlimited
	sent      int64
	cur       float64
	avg       float64
	peak      float64
	startedAt time.Time
	paused    bool
	recent    []tokSample
}

func New() *Limiter {
	l := &Limiter{
		sessions:    map[string]*SessionBucket{},
		global:      newBucket(0, 0),
		windowStart: time.Now(),
		samples:     make([]sample, 0, 4096),
	}
	go l.statsLoop()
	return l
}

func (l *Limiter) Attach(id string, rate int64, burstSec float64, mode string) *SessionBucket {
	l.mu.Lock()
	defer l.mu.Unlock()
	sb := &SessionBucket{
		id:        id,
		b:         newBucket(rate, burstSec),
		rate:      rate,
		burstSec:  burstSec,
		mode:      mode,
		startedAt: time.Now(),
	}
	l.sessions[id] = sb
	return sb
}

func (l *Limiter) Detach(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.sessions, id)
}

func (l *Limiter) Get(id string) *SessionBucket {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.sessions[id]
}

func (l *Limiter) SessionCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.sessions)
}

// SetGlobal configures the global token rate (0 = unlimited) and burst window.
func (l *Limiter) SetGlobal(rate int64, burstSec float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.global.configure(rate, burstSec)
}

// UpdateSession applies a new rate at runtime without dropping the connection.
func (sb *SessionBucket) Update(rate int64, burstSec float64, mode string) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.rate = rate
	sb.burstSec = burstSec
	if mode != "" {
		sb.mode = mode
	}
	sb.b.configure(rate, burstSec)
}

func (sb *SessionBucket) SetPaused(paused bool) {
	sb.mu.Lock()
	sb.paused = paused
	sb.mu.Unlock()
}

func (sb *SessionBucket) Stats() (sent int64, cur, avg, peak float64) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.sent, sb.cur, sb.avg, sb.peak
}

// Wait blocks until the given number of output tokens may be emitted, honoring
// the session limit, then the global allocation (fair share). A cancel/ctx
// abort returns immediately. Returns false only when ctx was cancelled.
func (sb *SessionBucket) Wait(ctx context.Context, tokens int) bool {
	if tokens <= 0 {
		return true
	}
	l := global()
	if !sb.waitSession(ctx, tokens) {
		return false
	}
	if l == nil {
		return true
	}
	return l.waitGlobal(ctx, sb, tokens)
}

func (sb *SessionBucket) waitSession(ctx context.Context, tokens int) bool {
	for {
		sb.mu.Lock()
		paused := sb.paused
		sb.mu.Unlock()
		if paused {
			// PAUSE has priority over all rate limits: effective rate is 0.
			select {
			case <-ctx.Done():
				return false
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		d := sb.b.take(tokens)
		if d <= 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
		}
	}
}

// waitGlobal enforces the global ceiling. Sessions compete fairly: the wait is
// spread over the number of active unlimited peers so no session starves.
func (l *Limiter) waitGlobal(ctx context.Context, sb *SessionBucket, tokens int) bool {
	for {
		d := l.global.take(tokens)
		if d <= 0 {
			return true
		}
		// Fair queue: when the bucket is empty, also yield proportionally to the
		// number of competing sessions so a first-mover cannot monopolize.
		peers := l.SessionCount()
		if peers > 1 {
			d = time.Duration(float64(d) / math.Min(float64(peers), 16))
			if d < 200*time.Microsecond {
				d = 200 * time.Microsecond
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
		}
	}
}

// Account records emitted tokens for statistics AFTER they are written.
func (sb *SessionBucket) Account(tokens int) {
	if tokens <= 0 {
		return
	}
	sb.mu.Lock()
	sb.sent += int64(tokens)
	sb.cur = float64(tokens)
	rate := sb.calcRateLocked()
	sb.cur = rate
	if rate > sb.peak {
		sb.peak = rate
	}
	sb.avg = float64(sb.sent) / time.Since(sb.startedAt).Seconds()
	sb.mu.Unlock()

	l := global()
	if l != nil {
		l.account(tokens)
	}
}

func (sb *SessionBucket) calcRateLocked() float64 {
	now := time.Now()
	sb.recent = append(sb.recent, tokSample{now, sb.cur})
	cut := now.Add(-2 * time.Second)
	i := 0
	for ; i < len(sb.recent); i++ {
		if sb.recent[i].at.After(cut) {
			break
		}
	}
	if i > 0 {
		sb.recent = sb.recent[i:]
	}
	if len(sb.recent) < 2 {
		return 0
	}
	var sum float64
	for _, s := range sb.recent {
		sum += s.tok
	}
	span := now.Sub(sb.recent[0].at).Seconds()
	if span <= 0 {
		return 0
	}
	return sum / span
}

type tokSample struct {
	at  time.Time
	tok float64
}

func (l *Limiter) account(tokens int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.totalTokens += int64(tokens)
	l.windowTokens += int64(tokens)
	l.samples = append(l.samples, sample{time.Now(), int64(tokens)})
}

func (l *Limiter) statsLoop() {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		l.recompute()
	}
}

func (l *Limiter) recompute() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cut := now.Add(-1 * time.Second)
	i := 0
	for ; i < len(l.samples); i++ {
		if l.samples[i].at.After(cut) {
			break
		}
	}
	if i > 0 {
		l.samples = l.samples[i:]
	}
	var cur int64
	cutCur := now.Add(-200 * time.Millisecond)
	for _, s := range l.samples {
		if s.at.After(cutCur) {
			cur += s.tokens
		}
	}
	l.currentRate = float64(cur) / 0.2
	if l.currentRate > l.peak {
		l.peak = l.currentRate
	}
	var sum int64
	for _, s := range l.samples {
		sum += s.tokens
	}
	span := 1.0
	if len(l.samples) > 0 {
		if d := now.Sub(l.samples[0].at).Seconds(); d > 0 {
			span = math.Min(d, 60)
		}
	}
	l.avg1m = float64(sum) / span
	if len(l.samples) > 4096 {
		l.samples = l.samples[len(l.samples)-4096:]
	}
}

// StatsSnapshot is the dashboard throughput panel payload.
type StatsSnapshot struct {
	Current     float64 `json:"current_token_rate"`
	Avg1m       float64 `json:"avg_1m_token_rate"`
	Peak        float64 `json:"peak_token_rate"`
	GlobalLimit int64   `json:"global_limit"`
	Utilization float64 `json:"utilization"`
	TotalTokens int64   `json:"total_tokens"`
}

func (l *Limiter) Stats() StatsSnapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var util float64
	if l.global.rate > 0 {
		util = l.currentRate / float64(l.global.rate) * 100
	}
	return StatsSnapshot{
		Current:     l.currentRate,
		Avg1m:       l.avg1m,
		Peak:        l.peak,
		GlobalLimit: l.global.rate,
		Utilization: util,
		TotalTokens: l.totalTokens,
	}
}

var (
	instance *Limiter
	once     sync.Once
)

func Init() *Limiter {
	once.Do(func() {
		instance = New()
	})
	return instance
}

func global() *Limiter { return instance }

// ApplyRuntimeConfig pushes the current admin settings into the limiter.
func ApplyRuntimeConfig() {
	l := instance
	if l == nil {
		return
	}
	rt := config.C().Runtime()
	l.SetGlobal(rt.GlobalTokenRate, rt.GlobalBurstSeconds)
}

// ---- token bucket ----

type bucket struct {
	mu       sync.Mutex
	rate     int64   // tokens per second, 0 = unlimited
	capacity float64 // burst capacity in tokens
	tokens   float64
	last     time.Time
	burstSec float64
}

func newBucket(rate int64, burstSec float64) *bucket {
	var cap float64
	if rate > 0 && burstSec > 0 {
		cap = float64(rate) * burstSec
	}
	return &bucket{rate: rate, burstSec: burstSec, capacity: cap, tokens: cap, last: time.Now()}
}

func (b *bucket) configure(rate int64, burstSec float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rate = rate
	b.burstSec = burstSec
	if rate > 0 && burstSec > 0 {
		b.capacity = float64(rate) * burstSec
	} else {
		b.capacity = 0
	}
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
}

// take attempts to consume n tokens. It returns the duration to wait before the
// tokens become available, or 0 if they are available right now.
func (b *bucket) take(n int) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rate <= 0 {
		return 0 // Unlimited: never sleep.
	}
	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens += elapsed * float64(b.rate)
	if b.capacity > 0 && b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	need := float64(n)
	if b.tokens >= need {
		b.tokens -= need
		return 0
	}
	deficit := need - b.tokens
	b.tokens = 0
	return time.Duration(deficit / float64(b.rate) * float64(time.Second))
}
