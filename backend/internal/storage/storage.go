package storage

import (
	"context"
	"time"
)

// ---- Models ----

type ModelBehavior string

const (
	BehaviorInfiniteEcho    ModelBehavior = "infinite_echo"
	BehaviorInfiniteEchoMax ModelBehavior = "infinite_echo_max"
	BehaviorManualOnly      ModelBehavior = "manual_only"
	BehaviorImmediateError  ModelBehavior = "immediate_error"
	BehaviorHangForever     ModelBehavior = "hang_forever"
	BehaviorStaticResponse  ModelBehavior = "static_response"
	BehaviorConnectionDrop  ModelBehavior = "connection_drop"
)

type Model struct {
	ModelID         string         `json:"model_id"`
	DisplayName     string         `json:"display_name"`
	Enabled         bool           `json:"enabled"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	Behavior        ModelBehavior  `json:"behavior"`
	EchoIntervalMS  int            `json:"default_echo_interval_ms"`
	ProtocolMode    string         `json:"protocol_mode"`
	Description     string         `json:"description,omitempty"`
	StaticResponse  string         `json:"static_response,omitempty"`
	ErrorStatus     int            `json:"error_status,omitempty"`
	ErrorCode       string         `json:"error_code,omitempty"`
	ErrorType       string         `json:"error_type,omitempty"`
	ErrorMessage    string         `json:"error_message,omitempty"`
	TokenRate       int64          `json:"token_rate,omitempty"`
	EchoContentMode string         `json:"echo_content_mode,omitempty"`
	MaxEchoCount    int            `json:"max_echo_count,omitempty"`
	// Agent & Subagent capabilities
	EnableAgent    bool `json:"enable_agent"`
	SubagentCount  int  `json:"subagent_count"`
	// Infinite Echo MAX stress test
	MaxTokenChunk int `json:"max_token_chunk"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// ---- Sessions ----

type SessionState string

const (
	StateConnected          SessionState = "CONNECTED"
	StateWaitingForSlot     SessionState = "WAITING_FOR_SLOT"
	StateEchoing            SessionState = "ECHOING"
	StatePaused             SessionState = "PAUSED"
	StateManual             SessionState = "MANUAL"
	StateErrorPending       SessionState = "ERROR_PENDING"
	StateEnding             SessionState = "ENDING"
	StateEnded              SessionState = "ENDED"
	StateClientDisconnected SessionState = "CLIENT_DISCONNECTED"
	StateResourceLimit      SessionState = "RESOURCE_LIMIT"
)

type SessionMode string

const (
	ModeEcho   SessionMode = "ECHO"
	ModeManual SessionMode = "MANUAL"
	ModeFault  SessionMode = "FAULT"
)

type RawRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Query   string            `json:"query,omitempty"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type RateConfig struct {
	TokenRate       int64   `json:"token_rate"` // 0 = unlimited
	BurstSeconds    float64 `json:"burst_seconds"`
	Mode            string  `json:"mode"`              // smooth|burst|unlimited
	ChunkSizeTokens int     `json:"chunk_size_tokens"` // 0 = auto
}

type Session struct {
	ID             string       `json:"session_id"`
	RequestID      string       `json:"request_id"`
	Protocol       string       `json:"protocol"`
	Model          string       `json:"model"`
	Streaming      bool         `json:"streaming"`
	ClientIP       string       `json:"client_ip"`
	UserAgent      string       `json:"user_agent"`
	KeyFingerprint string       `json:"key_fingerprint"`
	State          SessionState `json:"state"`
	Mode           SessionMode  `json:"mode"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
	EndedAt        *time.Time   `json:"ended_at,omitempty"`

	EchoCount    int64 `json:"echo_count"`
	BytesIn      int64 `json:"bytes_in"`
	BytesOut     int64 `json:"bytes_out"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	ChunkCount   int64 `json:"chunk_count"`

	CurrentRate float64 `json:"current_rate"`
	AverageRate float64 `json:"average_rate"`
	PeakRate    float64 `json:"peak_rate"`

	EchoIntervalMS  int        `json:"echo_interval_ms"`
	EchoContentMode string     `json:"echo_content_mode"`
	Rate            RateConfig `json:"rate"`

	Request      *RawRequest `json:"request,omitempty"`
	FinishReason string      `json:"finish_reason,omitempty"`
	EndReason    string      `json:"end_reason,omitempty"`
}

// ---- Events ----

type EventKind string

const (
	EventInput  EventKind = "input"
	EventOutput EventKind = "output"
	EventManual EventKind = "manual"
	EventSystem EventKind = "system"
	EventError  EventKind = "error"
	EventRawSSE EventKind = "raw_sse"
	EventAdmin  EventKind = "admin"
)

type Event struct {
	ID        int64     `json:"id"`
	SessionID string    `json:"session_id"`
	Seq       int64     `json:"seq"`
	Kind      EventKind `json:"kind"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Bytes     int       `json:"bytes"`
	Tokens    int       `json:"tokens"`
	CreatedAt time.Time `json:"created_at"`
}

// ---- Files / Assets ----

type FileRecord struct {
	ID             string    `json:"id"`
	Filename       string    `json:"filename"`
	MimeType       string    `json:"mime_type"`
	Bytes          int64     `json:"bytes"`
	SHA256         string    `json:"sha256"`
	Purpose        string    `json:"purpose"`
	UploadedAt     time.Time `json:"uploaded_at"`
	SessionID      string    `json:"session_id,omitempty"`
	KeyFingerprint string    `json:"key_fingerprint,omitempty"`
	StoragePath    string    `json:"storage_path,omitempty"`
}

type Asset struct {
	ID         string    `json:"id"`
	MimeType   string    `json:"mime_type"`
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	Filename   string    `json:"filename,omitempty"`
	SessionID  string    `json:"session_id,omitempty"`
	SourceKind string    `json:"source_kind"`
	CreatedAt  time.Time `json:"created_at"`
}

// ---- API Keys ----

type APIKey struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	KeyHash     string     `json:"key_hash"`
	Fingerprint string     `json:"fingerprint"`
	Prefix      string     `json:"prefix"`
	Suffix      string     `json:"suffix"`
	Enabled     bool       `json:"enabled"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	UseCount    int64      `json:"use_count"`
	Models      []string   `json:"models,omitempty"`
	MaxSessions int        `json:"max_sessions"`
	RateLimit   int64      `json:"rate_limit"`
}

// ---- Audit ----

type AuditEntry struct {
	ID        int64     `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Admin     string    `json:"admin"`
	SessionID string    `json:"session_id,omitempty"`
	Action    string    `json:"action"`
	Params    string    `json:"params,omitempty"`
	IP        string    `json:"ip,omitempty"`
}

// ---- Benchmarks ----

type Benchmark struct {
	ID          string    `json:"id"`
	Timestamp   time.Time `json:"timestamp"`
	Protocol    string    `json:"protocol"`
	ChunkSize   int       `json:"chunk_size"`
	DurationMS  int64     `json:"duration_ms"`
	TokensSent  int64     `json:"tokens_sent"`
	AverageRate float64   `json:"average_token_rate"`
	PeakRate    float64   `json:"peak_token_rate"`
	BytesSent   int64     `json:"bytes_sent"`
	CPUPercent  float64   `json:"cpu_percent"`
	MemoryMB    float64   `json:"memory_mb"`
}

// ---- Filters ----

type SessionFilter struct {
	Query      string
	Model      string
	Protocol   string
	State      string
	IP         string
	Key        string
	Since      *time.Time
	Until      *time.Time
	Limit      int
	Offset     int
	ActiveOnly bool
}

type EventFilter struct {
	SessionID string
	Kind      string
	AfterSeq  int64
	Limit     int
}

// Store is the persistence contract. SQLite implement it today; PostgreSQL can
// be added without touching business logic.
type Store interface {
	Close() error
	Ping(ctx context.Context) error
	// Models
	ListModels(ctx context.Context) ([]Model, error)
	ListEnabledModels(ctx context.Context) ([]Model, error)
	GetModel(ctx context.Context, id string) (*Model, error)
	CreateModel(ctx context.Context, m *Model) error
	UpdateModel(ctx context.Context, m *Model) error
	DeleteModel(ctx context.Context, id string) error

	// Sessions
	CreateSession(ctx context.Context, s *Session) error
	UpdateSession(ctx context.Context, s *Session) error
	GetSession(ctx context.Context, id string) (*Session, error)
	ListSessions(ctx context.Context, f SessionFilter) ([]Session, error)
	CountSessions(ctx context.Context, f SessionFilter) (int, error)
	DeleteSession(ctx context.Context, id string) error
	DeleteSessionsBefore(ctx context.Context, t time.Time) (int64, error)

	// Events
	AppendEvents(ctx context.Context, evs []Event) error
	ListEvents(ctx context.Context, f EventFilter) ([]Event, error)
	CountEvents(ctx context.Context, sessionID string) (int64, error)
	DeleteEventsBefore(ctx context.Context, t time.Time) (int64, error)

	// Files
	CreateFile(ctx context.Context, f *FileRecord) error
	GetFile(ctx context.Context, id string) (*FileRecord, error)
	ListFiles(ctx context.Context, limit, offset int) ([]FileRecord, error)
	DeleteFile(ctx context.Context, id string) error

	// Assets
	CreateAsset(ctx context.Context, a *Asset) error
	GetAsset(ctx context.Context, id string) (*Asset, error)
	ListAssets(ctx context.Context, limit, offset int) ([]Asset, error)
	TotalAssetBytes(ctx context.Context) (int64, error)

	// API keys
	CreateKey(ctx context.Context, k *APIKey) error
	GetKeyByHash(ctx context.Context, hash string) (*APIKey, error)
	ListKeys(ctx context.Context) ([]APIKey, error)
	UpdateKey(ctx context.Context, k *APIKey) error
	DeleteKey(ctx context.Context, id string) error
	TouchKey(ctx context.Context, id string) error

	// Audit
	AppendAudit(ctx context.Context, a *AuditEntry) error
	ListAudit(ctx context.Context, limit, offset int) ([]AuditEntry, error)

	// Benchmarks
	SaveBenchmark(ctx context.Context, b *Benchmark) error
	ListBenchmarks(ctx context.Context, limit int) ([]Benchmark, error)
	LatestBenchmark(ctx context.Context, protocol string) (*Benchmark, error)

	// Settings (runtime overrides persisted)
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
	AllSettings(ctx context.Context) (map[string]string, error)
}
