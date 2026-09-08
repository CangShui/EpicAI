// Package auth handles API key verification and admin authentication.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/epicai/epicai/backend/internal/config"
	"github.com/epicai/epicai/backend/internal/storage"
	"github.com/google/uuid"
)

type Manager struct {
	store storage.Store
	mu    sync.RWMutex
	cache map[string]*storage.APIKey
}

func New(store storage.Store) *Manager {
	return &Manager{store: store, cache: map[string]*storage.APIKey{}}
}

// Fingerprint produces a non-reversible short fingerprint of a key.
func Fingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:16]
}

func Hash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Mask renders sk-****F8A2 style masked keys for the admin UI.
func Mask(key string) string {
	if key == "" {
		return ""
	}
	prefix := ""
	if len(key) > 7 && strings.EqualFold(key[:7], "bearer ") {
		prefix = key[:7]
		key = key[7:]
	}
	if len(key) <= 8 {
		return prefix + strings.Repeat("*", len(key))
	}
	if strings.HasPrefix(key, "sk-") {
		return prefix + "sk-****" + key[len(key)-4:]
	}
	return prefix + key[:3] + "****" + key[len(key)-4:]
}

func MaskFingerprint(fp string) string {
	if fp == "" {
		return ""
	}
	if len(fp) <= 4 {
		return "****"
	}
	return "sk-****" + strings.ToUpper(fp[len(fp)-4:])
}

type KeyResult struct {
	Allowed bool
	Key     *storage.APIKey
	Reason  string
	Status  int
}

// Check validates a bearer token against the configured key mode.
func (m *Manager) Check(ctx context.Context, raw string) KeyResult {
	rt := config.C().Runtime()
	raw = strings.TrimSpace(raw)
	if len(raw) > 7 && strings.EqualFold(raw[:7], "bearer ") {
		raw = strings.TrimSpace(raw[7:])
	}
	switch rt.KeyMode {
	case config.KeyModeEmpty:
		return KeyResult{Allowed: true}
	case config.KeyModeAny:
		if raw == "" {
			return KeyResult{Allowed: false, Reason: "missing_api_key", Status: 401}
		}
		return KeyResult{Allowed: true}
	case config.KeyModeRequire:
		if raw == "" {
			return KeyResult{Allowed: false, Reason: "missing_api_key", Status: 401}
		}
		h := Hash(raw)
		m.mu.RLock()
		k, ok := m.cache[h]
		m.mu.RUnlock()
		if !ok {
			found, err := m.store.GetKeyByHash(ctx, h)
			if err == nil {
				k = found
			}
			m.mu.Lock()
			if k != nil {
				m.cache[h] = k
			}
			m.mu.Unlock()
		}
		if k == nil {
			return KeyResult{Allowed: false, Reason: "invalid_api_key", Status: 401}
		}
		if !k.Enabled {
			return KeyResult{Allowed: false, Reason: "key_disabled", Status: 403}
		}
		return KeyResult{Allowed: true, Key: k}
	}
	return KeyResult{Allowed: true}
}

func (m *Manager) Invalidate() {
	m.mu.Lock()
	m.cache = map[string]*storage.APIKey{}
	m.mu.Unlock()
}

func GenerateKey() (plain, hash, fingerprint, prefix, suffix string) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	plain = "sk-epic-" + hex.EncodeToString(b)
	hash = Hash(plain)
	fingerprint = Fingerprint(plain)
	prefix = plain[:8]
	suffix = plain[len(plain)-4:]
	return
}

func NewKeyID() string { return "key_" + uuid.NewString() }

// AdminAuth verifies admin credentials using constant-time comparison.
type AdminAuth struct {
	mu       sync.RWMutex
	sessions map[string]time.Time
}

func NewAdmin() *AdminAuth {
	return &AdminAuth{sessions: map[string]time.Time{}}
}

func (a *AdminAuth) Login(user, pass string) (string, bool) {
	cfg := config.C().Static()
	okUser := subtle.ConstantTimeCompare([]byte(user), []byte(cfg.AdminUser)) == 1
	okPass := subtle.ConstantTimeCompare([]byte(pass), []byte(cfg.AdminPassword)) == 1
	if !okUser || !okPass {
		return "", false
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	a.mu.Lock()
	a.sessions[token] = time.Now().Add(12 * time.Hour)
	a.mu.Unlock()
	return token, true
}

func (a *AdminAuth) Valid(token string) bool {
	if token == "" {
		return false
	}
	a.mu.RLock()
	exp, ok := a.sessions[token]
	a.mu.RUnlock()
	if !ok {
		return false
	}
	return time.Now().Before(exp)
}

func (a *AdminAuth) Logout(token string) {
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
}

func (a *AdminAuth) User(token string) string {
	if !a.Valid(token) {
		return ""
	}
	return config.C().Static().AdminUser
}
