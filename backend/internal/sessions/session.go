// Package sessions owns live streaming sessions: lifecycle, control commands
// (pause/resume/takeover/finish/drop), statistics and resource protection.
package sessions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/epicai/epicai/backend/internal/canonical"
	"github.com/epicai/epicai/backend/internal/config"
	"github.com/epicai/epicai/backend/internal/events"
	"github.com/epicai/epicai/backend/internal/ratelimit"
	"github.com/epicai/epicai/backend/internal/storage"
	"github.com/epicai/epicai/backend/internal/tokenizer"
	"github.com/google/uuid"
)

var (
	ErrLimitReached   = errors.New("concurrent session limit reached")
	ErrSessionClosed  = errors.New("session closed")
	ErrDropConnection = errors.New("connection dropped by administrator")
)

// Command is an admin control action delivered to a running session.
type Command struct {
	Type       string // pause|resume|takeover|return|send|finish|drop|inject|rate|interval|mode
	Text       string
	Rate       int64
	Mode       string
	IntervalMS int
	ChunkSize  int
	Fault      *FaultSpec
	// delivery mode for manual messages
	ManualMode  string // full|stream_char|stream_block|typing
	CharsPerSec int
}

// FaultSpec describes an injected error.
type FaultSpec struct {
	HTTPStatus  int    `json:"http_status"`
	Code        string `json:"code"`
	Type        string `json:"type"`
	Message     string `json:"message"`
	Param       string `json:"param"`
	RawBody     string `json:"raw_body,omitempty"`
	RawMode     bool   `json:"raw_mode,omitempty"`
	DelayMS     int    `json:"delay_ms,omitempty"`
	AfterChunks int    `json:"after_chunks,omitempty"`
	// Mode A: close | B: sse_error | C: http_error | D: malformed
	Mode string `json:"mode"`
}

// Session is a live streaming session.
type Session struct {
	ID        string
	RequestID string
	Protocol  string
	Model     string
	ModelCfg  *storage.Model
	Streaming bool
	ClientIP  string
	UserAgent string
	KeyFP     string
	CreatedAt time.Time
	Conv      *canonical.Conversation
	rawRequest *storage.RawRequest

	store storage.Store
	bus   *events.Bus
	log   *slog.Logger

	mu        sync.RWMutex
	state     storage.SessionState
	mode      storage.SessionMode
	endedAt   *time.Time
	endReason string

	// control
	cmdCh     chan Command
	done      chan struct{}
	closeOnce sync.Once

	// counters
	echoCount    int64
	chunkCount   int64
	bytesIn      int64
	bytesOut     int64
	inputTokens  int64
	outputTokens int64

	// echo config
	intervalMS int
	echoMode   string
	maxEcho    int64

	// rate
	bucket        *ratelimit.SessionBucket
	bucketRateVal int64
	bucketModeVal string

	// fault pending
	pendingFault    *FaultSpec
	afterChunksLeft int64

	// manual
	manualCh chan string

	// writer hook used by drop connection
	killConn func()

	// last activity for idle detection
	lastActivity atomic.Int64

	// event sequence for the DB log
	seq atomic.Int64
}

// Manager tracks live sessions and enforces limits.
type Manager struct {
	mu             sync.RWMutex
	live           map[string]*Session
	store          storage.Store
	bus            *events.Bus
	limiter        *ratelimit.Limiter
	log            *slog.Logger
	perIP          map[string]int
	perKey         map[string]int
	totalReq       atomic.Int64
	errorsInjected atomic.Int64
}

func NewManager(store storage.Store, bus *events.Bus, lim *ratelimit.Limiter, log *slog.Logger) *Manager {
	return &Manager{
		live:    map[string]*Session{},
		store:   store,
		bus:     bus,
		limiter: lim,
		log:     log,
		perIP:   map[string]int{},
		perKey:  map[string]int{},
	}
}

func (m *Manager) NewID() string {
	return "sess_epic_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
}

func (m *Manager) RequestID() string {
	return "req_epic_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
}

type AdmitResult struct {
	Allowed  bool
	Reason   string
	Status   int
	Code     string
	Message  string
	Behavior config.OverloadBehavior
}

// Admit enforces global / per-IP / per-key concurrency limits.
func (m *Manager) Admit(ip, keyFP string, keyMax int) AdmitResult {
	rt := config.C().Runtime()
	m.mu.Lock()
	defer m.mu.Unlock()
	active := len(m.live)

	limit := rt.MaxActiveSessions
	if limit > 0 && active >= limit {
		return AdmitResult{Allowed: false, Reason: "global", Status: 429,
			Code: "concurrency_limit_exceeded", Message: "EpicAI concurrent session limit exceeded",
			Behavior: rt.OverloadBehavior}
	}
	if rt.MaxSessionsPerIP > 0 && ip != "" {
		if m.perIP[ip] >= rt.MaxSessionsPerIP {
			return AdmitResult{Allowed: false, Reason: "ip", Status: 429,
				Code: "ip_concurrency_limit_exceeded", Message: "Too many concurrent sessions from this IP",
				Behavior: rt.OverloadBehavior}
		}
	}
	perKeyLimit := rt.MaxSessionsPerKey
	if keyMax > 0 {
		perKeyLimit = keyMax
	}
	if perKeyLimit > 0 && keyFP != "" {
		if m.perKey[keyFP] >= perKeyLimit {
			return AdmitResult{Allowed: false, Reason: "key", Status: 429,
				Code: "key_concurrency_limit_exceeded", Message: "Too many concurrent sessions for this API key",
				Behavior: rt.OverloadBehavior}
		}
	}
	m.perIP[ip]++
	m.perKey[keyFP]++
	return AdmitResult{Allowed: true}
}

func (m *Manager) Register(s *Session) {
	m.mu.Lock()
	m.live[s.ID] = s
	m.mu.Unlock()
	m.totalReq.Add(1)
	m.bus.Publish(events.Message{
		Type:    events.SessionCreated,
		Session: s.ID,
		Data: map[string]any{
			"session_id": s.ID, "model": s.Model, "protocol": s.Protocol,
			"client_ip": s.ClientIP, "streaming": s.Streaming, "state": string(s.State()),
		},
	})
}

func (m *Manager) Unregister(id string) {
	m.mu.Lock()
	s, ok := m.live[id]
	if ok {
		delete(m.live, id)
		if s.ClientIP != "" && m.perIP[s.ClientIP] > 0 {
			m.perIP[s.ClientIP]--
		}
		if s.KeyFP != "" && m.perKey[s.KeyFP] > 0 {
			m.perKey[s.KeyFP]--
		}
	}
	m.mu.Unlock()
}

func (m *Manager) Get(id string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.live[id]
}

func (m *Manager) Live() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, 0, len(m.live))
	for _, s := range m.live {
		out = append(out, s)
	}
	return out
}

func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, s := range m.live {
		switch s.State() {
		case storage.StateConnected, storage.StateEchoing, storage.StatePaused,
			storage.StateManual, storage.StateErrorPending, storage.StateWaitingForSlot:
			n++
		}
	}
	return n
}

func (m *Manager) TotalRequests() int64  { return m.totalReq.Load() }
func (m *Manager) ErrorsInjected() int64 { return m.errorsInjected.Load() }
func (m *Manager) IncErrorsInjected()    { m.errorsInjected.Add(1) }

// ---------------- Session lifecycle ----------------

type Options struct {
	ID              string
	RequestID       string
	Protocol        string
	Model           *storage.Model
	Streaming       bool
	ClientIP        string
	UserAgent       string
	KeyFP           string
	Conv            *canonical.Conversation
	RateLimit       int64
	EchoContentMode string
	ScenarioID      string
	KillConn        func()
}

func (m *Manager) Create(opts Options) *Session {
	rt := config.C().Runtime()
	now := time.Now()
	interval := opts.Model.EchoIntervalMS
	if interval < 0 {
		interval = 0
	}
	echoMode := opts.EchoContentMode
	if echoMode == "" {
		echoMode = rt.EchoContentMode
	}
	if opts.Model.EchoContentMode != "" {
		echoMode = opts.Model.EchoContentMode
	}
	rate := opts.RateLimit
	if rate == 0 && opts.Model.TokenRate > 0 {
		rate = opts.Model.TokenRate
	}
	s := &Session{
		ID:         opts.ID,
		RequestID:  opts.RequestID,
		Protocol:   opts.Protocol,
		Model:      opts.Model.ModelID,
		ModelCfg:   opts.Model,
		Streaming:  opts.Streaming,
		ClientIP:   opts.ClientIP,
		UserAgent:  opts.UserAgent,
		KeyFP:      opts.KeyFP,
		CreatedAt:  now,
		Conv:       opts.Conv,
		store:      m.store,
		bus:        m.bus,
		log:        m.log,
		state:      storage.StateConnected,
		mode:       storage.ModeEcho,
		cmdCh:      make(chan Command, 64),
		done:       make(chan struct{}),
		manualCh:   make(chan string, 64),
		intervalMS: interval,
		echoMode:   echoMode,
		maxEcho:    int64(opts.Model.MaxEchoCount),
		killConn:   opts.KillConn,
	}
	s.lastActivity.Store(now.UnixNano())

	inTok := int64(tokenizer.Count(s.Conv.Text()))
	s.inputTokens = inTok
	s.bucket = m.limiter.Attach(s.ID, rate, rt.SessionBurstSeconds, rt.RateMode)

	_ = m.store.CreateSession(context.Background(), s.snapshot())
	m.Register(s)
	return s
}

func (s *Session) snapshot() *storage.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cur, avg, peak := 0.0, 0.0, 0.0
	if s.bucket != nil {
		_, cur, avg, peak = s.bucket.Stats()
	}
	rate := storage.RateConfig{}
	if s.bucket != nil {
		rate.TokenRate = s.bucketRate()
		rate.Mode = s.bucketMode()
	}
	return &storage.Session{
		ID: s.ID, RequestID: s.RequestID, Protocol: s.Protocol, Model: s.Model,
		Streaming: s.Streaming, ClientIP: s.ClientIP, UserAgent: s.UserAgent,
		KeyFingerprint: s.KeyFP, State: s.state, Mode: s.mode,
		CreatedAt: s.CreatedAt, UpdatedAt: time.Now(), EndedAt: s.endedAt,
		EchoCount: s.echoCount, BytesIn: s.bytesIn, BytesOut: s.bytesOut,
		InputTokens: s.inputTokens, OutputTokens: s.outputTokens, ChunkCount: s.chunkCount,
		CurrentRate: cur, AverageRate: avg, PeakRate: peak,
		EchoIntervalMS: s.intervalMS, EchoContentMode: s.echoMode, Rate: rate,
		ScenarioID: "", EndReason: s.endReason,
		Request: s.rawRequest,
	}
}

func (s *Session) bucketRate() int64 {
	if s.bucket == nil {
		return 0
	}
	return s.bucketRateVal
}

func (s *Session) bucketMode() string {
	if s.bucket == nil {
		return "unlimited"
	}
	return s.bucketModeVal
}

// Persist writes the current state to storage (throttled by callers).
func (s *Session) Persist() {
	snap := s.snapshot()
	if err := s.store.UpdateSession(context.Background(), snap); err != nil {
		s.log.Debug("persist session", "id", s.ID, "err", err)
	}
}

// SetRawRequest attaches the original HTTP request metadata.
func (s *Session) SetRawRequest(req *storage.RawRequest, bytesIn int64) {
	s.mu.Lock()
	s.bytesIn = bytesIn
	s.rawRequest = req
	s.mu.Unlock()
	snap := s.snapshot()
	_ = s.store.UpdateSession(context.Background(), snap)
}

// PublishState broadcasts a state change to admin clients.
func (s *Session) PublishState() {
	snap := s.snapshot()
	s.bus.Publish(events.Message{
		Type:    events.SessionUpdated,
		Session: s.ID,
		Data: map[string]any{
			"state": string(snap.State), "mode": string(snap.Mode),
			"echo_count": snap.EchoCount, "bytes_out": snap.BytesOut,
			"bytes_in": snap.BytesIn, "output_tokens": snap.OutputTokens,
			"input_tokens": snap.InputTokens, "chunk_count": snap.ChunkCount,
			"current_rate": snap.CurrentRate, "average_rate": snap.AverageRate,
			"peak_rate": snap.PeakRate, "duration_ms": time.Since(s.CreatedAt).Milliseconds(),
			"model": snap.Model, "protocol": snap.Protocol,
		},
	})
}

func (s *Session) State() storage.SessionState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Session) Mode() storage.SessionMode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mode
}

func (s *Session) SetState(st storage.SessionState) {
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()
}

// SnapshotSession returns a storage copy of the live session state.
func (s *Session) SnapshotSession() *storage.Session {
	snap := s.snapshot()
	if snap.Request == nil {
		if rec, err := s.store.GetSession(context.Background(), s.ID); err == nil && rec != nil {
			snap.Request = rec.Request
		}
	}
	return snap
}

// SnapshotTokens returns the current token counters.
func (s *Session) SnapshotTokens() TokenCounters {
	return TokenCounters{
		Input:  atomic.LoadInt64(&s.inputTokens),
		Output: atomic.LoadInt64(&s.outputTokens),
		Echoes: atomic.LoadInt64(&s.echoCount),
		Chunks: atomic.LoadInt64(&s.chunkCount),
		Bytes:  atomic.LoadInt64(&s.bytesOut),
	}
}

// TokenCounters is a point-in-time statistics snapshot.
type TokenCounters struct {
	Input  int64
	Output int64
	Echoes int64
	Chunks int64
	Bytes  int64
}

func (s *Session) SetMode(m storage.SessionMode) {
	s.mu.Lock()
	s.mode = m
	s.mu.Unlock()
}

func (s *Session) Commands() <-chan Command   { return s.cmdCh }
func (s *Session) ManualInput() <-chan string { return s.manualCh }
func (s *Session) Done() <-chan struct{}      { return s.done }

// Send delivers an admin control command to the running session.
func (s *Session) Send(cmd Command) error {
	select {
	case s.cmdCh <- cmd:
		return nil
	case <-s.done:
		return ErrSessionClosed
	default:
		// command channel full: drop oldest to keep admin responsive
		select {
		case <-s.cmdCh:
		default:
		}
		select {
		case s.cmdCh <- cmd:
			return nil
		default:
			return ErrSessionClosed
		}
	}
}

// SendManual queues a manual assistant message for delivery to the client.
func (s *Session) SendManual(text string) error {
	select {
	case s.manualCh <- text:
		return nil
	case <-s.done:
		return ErrSessionClosed
	}
}

// TakeOver switches ECHOING -> MANUAL, pausing the Echo Engine immediately.
func (s *Session) TakeOver() {
	s.SetMode(storage.ModeManual)
	s.SetState(storage.StateManual)
	s.PublishState()
}

// ReturnToEcho resumes automatic echoing.
func (s *Session) ReturnToEcho() {
	s.SetMode(storage.ModeEcho)
	s.SetState(storage.StateEchoing)
	s.PublishState()
}

// Pause stops output while keeping the connection open (effective rate = 0).
func (s *Session) Pause() {
	if s.bucket != nil {
		s.bucket.SetPaused(true)
	}
	s.SetState(storage.StatePaused)
	s.PublishState()
}

// Resume restores the configured rate.
func (s *Session) Resume() {
	if s.bucket != nil {
		s.bucket.SetPaused(false)
	}
	s.SetState(storage.StateEchoing)
	s.PublishState()
}

func (s *Session) SetRate(rate int64, mode string, burst float64, chunkSize int) {
	if s.bucket != nil {
		s.bucket.Update(rate, burst, mode)
	}
	s.mu.Lock()
	s.bucketRateVal = rate
	if mode != "" {
		s.bucketModeVal = mode
	}
	s.mu.Unlock()
	s.PublishState()
}

func (s *Session) SetInterval(ms int) {
	if ms < 0 {
		ms = 0
	}
	s.mu.Lock()
	s.intervalMS = ms
	s.mu.Unlock()
	s.PublishState()
}

func (s *Session) IntervalMS() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.intervalMS
}

func (s *Session) EchoMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.echoMode
}

func (s *Session) SetEchoMode(mode string) {
	s.mu.Lock()
	s.echoMode = mode
	s.mu.Unlock()
}

// InjectFault schedules an error injection. It bypasses rate limiting entirely.
func (s *Session) InjectFault(f *FaultSpec) {
	m := s.manager()
	if m != nil {
		m.IncErrorsInjected()
	}
	s.mu.Lock()
	s.pendingFault = f
	if f.AfterChunks > 0 {
		s.afterChunksLeft = int64(f.AfterChunks)
	}
	s.state = storage.StateErrorPending
	s.mu.Unlock()
	s.bus.Publish(events.Message{
		Type: events.FaultInjected, Session: s.ID,
		Data: map[string]any{
			"http_status": f.HTTPStatus, "code": f.Code, "message": f.Message,
			"mode": f.Mode, "delay_ms": f.DelayMS, "after_chunks": f.AfterChunks,
		},
	})
	// Error injection must not wait on the token bucket.
	go func() {
		if f.DelayMS > 0 {
			select {
			case <-time.After(time.Duration(f.DelayMS) * time.Millisecond):
			case <-s.done:
				return
			}
		}
		_ = s.Send(Command{Type: "inject", Fault: f})
	}()
}

func (s *Session) PendingFault() *FaultSpec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pendingFault
}

// SetPendingFault arms a deferred fault that the echo loop triggers once the
// configured output chunk count has been reached.
func (s *Session) SetPendingFault(f *FaultSpec) {
	s.mu.Lock()
	s.pendingFault = f
	if f.AfterChunks > 0 {
		s.afterChunksLeft = int64(f.AfterChunks)
	}
	s.mu.Unlock()
	m := s.manager()
	if m != nil {
		m.IncErrorsInjected()
	}
	s.bus.Publish(events.Message{
		Type: events.FaultInjected, Session: s.ID,
		Data: map[string]any{
			"http_status": f.HTTPStatus, "code": f.Code, "message": f.Message,
			"mode": f.Mode, "after_chunks": f.AfterChunks, "scheduled": true,
		},
	})
}

// Drop kills the underlying socket without a clean protocol ending.
func (s *Session) Drop() {
	if s.killConn != nil {
		s.killConn()
	}
	s.mu.Lock()
	s.endReason = "drop_connection"
	s.mu.Unlock()
}

// Finish marks a normal ending.
func (s *Session) Finish(reason string) {
	s.mu.Lock()
	s.state = storage.StateEnding
	s.endReason = reason
	s.mu.Unlock()
	_ = s.Send(Command{Type: "finish"})
}

// Close finalizes the session, releasing resources.
func (s *Session) Close(state storage.SessionState, reason string) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.state = state
		s.endReason = reason
		now := time.Now()
		s.endedAt = &now
		s.mu.Unlock()
		close(s.done)
		snap := s.snapshot()
		_ = s.store.UpdateSession(context.Background(), snap)
		if s.bucket != nil {
			s.bucket.SetPaused(false)
		}
		s.bus.Publish(events.Message{
			Type: events.SessionClosed, Session: s.ID,
			Data: map[string]any{"state": string(state), "reason": reason,
				"echo_count": snap.EchoCount, "output_tokens": snap.OutputTokens,
				"bytes_out": snap.BytesOut, "duration_ms": time.Since(s.CreatedAt).Milliseconds()},
		})
	})
}

func (s *Session) manager() *Manager { return managerRef }

var managerRef *Manager

func SetManagerRef(m *Manager) { managerRef = m }

// accounting helpers used by engines

func (s *Session) AddEcho() {
	s.echoCount++
	s.lastActivity.Store(time.Now().UnixNano())
}

func (s *Session) EchoCount() int64 { return atomic.LoadInt64(&s.echoCount) }

func (s *Session) AddChunk(n int)    { atomic.AddInt64(&s.chunkCount, int64(n)) }
func (s *Session) ChunkCount() int64 { return atomic.LoadInt64(&s.chunkCount) }

func (s *Session) AddBytesOut(n int) {
	atomic.AddInt64(&s.bytesOut, int64(n))
	s.lastActivity.Store(time.Now().UnixNano())
}

func (s *Session) AddOutputTokens(n int) { atomic.AddInt64(&s.outputTokens, int64(n)) }

func (s *Session) BytesOut() int64 { return atomic.LoadInt64(&s.bytesOut) }

func (s *Session) NextSeq() int64 { return s.seq.Add(1) }

func (s *Session) Bucket() *ratelimit.SessionBucket { return s.bucket }

func (s *Session) EndReason() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.endReason
}

func (s *Session) StartedAt() time.Time { return s.CreatedAt }

func (s *Session) Duration() time.Duration { return time.Since(s.CreatedAt) }

func clientIPFrom(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

var _ = fmt.Sprintf
var _ = clientIPFrom
