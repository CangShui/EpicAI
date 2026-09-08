// Package assets implements the EpicAI Asset Store: content-addressed binary
// storage used for uploaded files and for images extracted from requests.
package assets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/epicai/epicai/backend/internal/config"
	"github.com/epicai/epicai/backend/internal/storage"
	"github.com/google/uuid"
)

type Store struct {
	root  string
	db    storage.Store
	mu    sync.Mutex
	total int64
}

func New(db storage.Store) (*Store, error) {
	root := config.C().Static().StoragePath
	if root == "" {
		root = "data/storage"
	}
	for _, sub := range []string{"files", "assets"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return nil, fmt.Errorf("create storage dir: %w", err)
		}
	}
	s := &Store{root: root, db: db}
	if n, err := db.TotalAssetBytes(context.Background()); err == nil {
		s.total = n
	}
	return s, nil
}

func (s *Store) Root() string { return s.root }

func (s *Store) TotalBytes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

type WriteResult struct {
	ID       string
	Path     string
	Size     int64
	SHA256   string
	MimeType string
}

func (s *Store) Put(kind, filename, mimeType string, r io.Reader, maxBytes int64, sessionID string) (*WriteResult, error) {
	id := newID(kind)
	dir := filepath.Join(s.root, kind+"s")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, id)
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	h := sha256.New()
	var written int64
	buf := make([]byte, 64*1024)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			written += int64(n)
			if maxBytes > 0 && written > maxBytes {
				f.Close()
				os.Remove(path)
				return nil, fmt.Errorf("%w: limit %d bytes", ErrTooLarge, maxBytes)
			}
			h.Write(buf[:n])
			if _, werr := f.Write(buf[:n]); werr != nil {
				return nil, werr
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, rerr
		}
	}
	if mimeType == "" {
		mimeType = detectMime(filename, path)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	s.mu.Lock()
	s.total += written
	s.mu.Unlock()

	if kind == "asset" {
		_ = s.db.CreateAsset(context.Background(), &storage.Asset{
			ID: id, MimeType: mimeType, Path: path, Size: written,
			Filename: filename, SessionID: sessionID, SourceKind: kind, CreatedAt: time.Now(),
		})
	}
	return &WriteResult{ID: id, Path: path, Size: written, SHA256: sum, MimeType: mimeType}, nil
}

var ErrTooLarge = fmt.Errorf("payload too large")

func (s *Store) Open(kind, id string) (*os.File, error) {
	path := filepath.Join(s.root, kind+"s", filepath.Base(id))
	return os.Open(path)
}

func (s *Store) Path(kind, id string) string {
	return filepath.Join(s.root, kind+"s", filepath.Base(id))
}

func (s *Store) Delete(kind, id string) error {
	path := s.Path(kind, id)
	if info, err := os.Stat(path); err == nil {
		s.mu.Lock()
		s.total -= info.Size()
		s.mu.Unlock()
	}
	return os.Remove(path)
}

func newID(kind string) string {
	prefix := "file_epic_"
	if kind == "asset" {
		prefix = "asset_epic_"
	}
	return prefix + strings.ReplaceAll(uuid.NewString(), "-", "")[:24]
}

func detectMime(filename, path string) string {
	if filename != "" {
		if m := mime.TypeByExtension(filepath.Ext(filename)); m != "" {
			return m
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return httpDetect(buf[:n])
}
