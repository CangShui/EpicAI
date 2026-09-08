// Package config loads EpicAI configuration from environment variables / .env
// and exposes mutable runtime settings that can be changed from the admin UI
// without restarting the process.
package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CORS string

const (
	CORSDisabled  CORS = "disabled"
	CORSAllowAll  CORS = "allow_all"
	CORSAllowList CORS = "allow_list"
)

type KeyMode string

const (
	KeyModeAny     KeyMode = "any"
	KeyModeRequire KeyMode = "require"
	KeyModeEmpty   KeyMode = "allow_empty"
)

type OverloadBehavior string

const (
	OverloadReject      OverloadBehavior = "reject"
	OverloadQueue       OverloadBehavior = "queue"
	OverloadHang        OverloadBehavior = "hang"
	OverloadCustomError OverloadBehavior = "custom_error"
)

type ResourceAction string

const (
	ResourceDisconnect ResourceAction = "disconnect"
	ResourceError      ResourceAction = "error"
	ResourcePause      ResourceAction = "pause"
)

// Static holds process-lifetime configuration (from env / .env).
type Static struct {
	Host            string
	Port            int
	AdminPort       int
	DatabaseURL     string
	StoragePath     string
	AdminUser       string
	AdminPassword   string
	AllowAnyKey     bool
	DefaultModel    string
	LogLevel        string
	PublicBaseURL   string
	ReadTimeoutSec  int
	WriteTimeoutSec int
	// Streaming connections must not be bounded by write deadlines.
	IdleTimeoutSec int
}

// Runtime holds settings that may change at runtime via admin Settings.
type Runtime struct {
	CORSMode              CORS
	CORSAllowOrigins      []string
	KeyMode               KeyMode
	GlobalTokenRate       int64 // tokens/s, 0 = unlimited
	GlobalBurstSeconds    float64
	SessionTokenRate      int64 // default session rate, 0 = unlimited
	SessionBurstSeconds   float64
	RateMode              string // smooth | burst | unlimited
	ChunkSizeTokens       int    // 0 = auto
	MaxActiveSessions     int
	MaxSessionsPerIP      int
	MaxSessionsPerKey     int
	OverloadBehavior      OverloadBehavior
	OverloadCustomBody    string
	OverloadCustomCode    int
	MaxFileSize           int64
	MaxAssetsSize         int64
	MaxLogSize            int64
	MaxEventsPerSession   int
	MaxEchoRate           float64
	ResourceAction        ResourceAction
	RetentionDays         int
	HoldConnectionForever bool
	BypassManualRate      bool
	DefaultEchoIntervalMS int
	EchoContentMode       string
	ImageEchoMode         string // strict | relaxed
	RepeatFileReference   bool
	RepeatDownloadURL     bool
	RepeatMetadata        bool
	UnknownModelBehavior  string // error | fallback
	AdminMaxTokenRate     int64  // administrative maximum, 0 = unset
	ManualCharsPerSecond  int
}

type Config struct {
	mu      sync.RWMutex
	static  Static
	runtime Runtime
}

var instance *Config

func Default() *Config {
	return &Config{
		static: Static{
			Host:            "0.0.0.0",
			Port:            8000,
			AdminPort:       0,
			DatabaseURL:     "sqlite:data/epicai.db",
			StoragePath:     "data/storage",
			AdminUser:       "admin",
			AdminPassword:   "epicai",
			AllowAnyKey:     true,
			DefaultModel:    "epic-alpha",
			LogLevel:        "info",
			PublicBaseURL:   "",
			ReadTimeoutSec:  120,
			WriteTimeoutSec: 0,
			IdleTimeoutSec:  0,
		},
		runtime: Runtime{
			CORSMode:              CORSAllowAll,
			CORSAllowOrigins:      []string{},
			KeyMode:               KeyModeAny,
			GlobalTokenRate:       0,
			GlobalBurstSeconds:    1.0,
			SessionTokenRate:      0,
			SessionBurstSeconds:   1.0,
			RateMode:              "burst",
			ChunkSizeTokens:       0,
			MaxActiveSessions:     100,
			MaxSessionsPerIP:      0,
			MaxSessionsPerKey:     0,
			OverloadBehavior:      OverloadReject,
			OverloadCustomCode:    429,
			MaxFileSize:           20 << 20,
			MaxAssetsSize:         1 << 30,
			MaxLogSize:            1 << 30,
			MaxEventsPerSession:   100000,
			MaxEchoRate:           0,
			ResourceAction:        ResourcePause,
			RetentionDays:         7,
			HoldConnectionForever: false,
			BypassManualRate:      false,
			DefaultEchoIntervalMS: 500,
			EchoContentMode:       "message",
			ImageEchoMode:         "strict",
			RepeatFileReference:   true,
			RepeatDownloadURL:     true,
			RepeatMetadata:        true,
			UnknownModelBehavior:  "error",
			AdminMaxTokenRate:     0,
			ManualCharsPerSecond:  0,
		},
	}
}

func Init() *Config {
	instance = Default()
	instance.loadEnv()
	return instance
}

func C() *Config {
	if instance == nil {
		instance = Default()
	}
	return instance
}

func (c *Config) Static() Static {
	return c.static
}

func (c *Config) Runtime() Runtime {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rt := c.runtime
	rt.CORSAllowOrigins = append([]string{}, c.runtime.CORSAllowOrigins...)
	return rt
}

func (c *Config) Update(fn func(*Runtime)) Runtime {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(&c.runtime)
	rt := c.runtime
	rt.CORSAllowOrigins = append([]string{}, c.runtime.CORSAllowOrigins...)
	return rt
}

func (c *Config) loadEnv() {
	// .env is optional; environment variables take precedence.
	if data, err := os.ReadFile(".env"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			idx := strings.Index(line, "=")
			if idx <= 0 {
				continue
			}
			k := strings.TrimSpace(line[:idx])
			v := strings.Trim(strings.TrimSpace(line[idx+1:]), `"'`)
			if os.Getenv(k) == "" {
				_ = os.Setenv(k, v)
			}
		}
	}
	// Also check next to the executable and repo root for dev convenience.
	for _, p := range []string{"../.env", "../../.env"} {
		if data, err := os.ReadFile(p); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				idx := strings.Index(line, "=")
				if idx <= 0 {
					continue
				}
				k := strings.TrimSpace(line[:idx])
				v := strings.Trim(strings.TrimSpace(line[idx+1:]), `"'`)
				if os.Getenv(k) == "" {
					_ = os.Setenv(k, v)
				}
			}
			break
		}
	}

	s := &c.static
	s.Host = envStr("EPICAI_HOST", s.Host)
	s.Port = envInt("EPICAI_PORT", s.Port)
	s.AdminPort = envInt("EPICAI_ADMIN_PORT", s.AdminPort)
	s.DatabaseURL = envStr("EPICAI_DATABASE_URL", s.DatabaseURL)
	s.StoragePath = envStr("EPICAI_STORAGE_PATH", s.StoragePath)
	s.AdminUser = envStr("EPICAI_ADMIN_USER", s.AdminUser)
	s.AdminPassword = envStr("EPICAI_ADMIN_PASSWORD", s.AdminPassword)
	s.AllowAnyKey = envBool("EPICAI_ALLOW_ANY_KEY", s.AllowAnyKey)
	s.DefaultModel = envStr("EPICAI_DEFAULT_MODEL", s.DefaultModel)
	s.LogLevel = envStr("EPICAI_LOG_LEVEL", s.LogLevel)
	s.PublicBaseURL = envStr("EPICAI_PUBLIC_BASE_URL", s.PublicBaseURL)
	s.ReadTimeoutSec = envInt("EPICAI_READ_TIMEOUT_SEC", s.ReadTimeoutSec)
	s.WriteTimeoutSec = envInt("EPICAI_WRITE_TIMEOUT_SEC", s.WriteTimeoutSec)
	s.IdleTimeoutSec = envInt("EPICAI_IDLE_TIMEOUT_SEC", s.IdleTimeoutSec)

	if !filepath.IsAbs(s.StoragePath) && !strings.HasPrefix(s.DatabaseURL, "postgres") {
		// keep relative paths relative to cwd; documented in README
		_ = s
	}
}

func (c *Config) Level() slog.Level {
	switch strings.ToLower(c.static.LogLevel) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (c *Config) Addr() string {
	return c.static.Host + ":" + strconv.Itoa(c.static.Port)
}

func (c *Config) AdminAddr() string {
	if c.static.AdminPort > 0 {
		return c.static.Host + ":" + strconv.Itoa(c.static.AdminPort)
	}
	return c.Addr()
}

func (c *Config) UptimeStart() time.Time { return startTime }

var startTime = time.Now()

func envStr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}
