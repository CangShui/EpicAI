package main

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/epicai/epicai/backend/internal/api"
	adminapi "github.com/epicai/epicai/backend/internal/api/admin"
	"github.com/epicai/epicai/backend/internal/assets"
	"github.com/epicai/epicai/backend/internal/audit"
	"github.com/epicai/epicai/backend/internal/auth"
	"github.com/epicai/epicai/backend/internal/config"
	"github.com/epicai/epicai/backend/internal/events"
	"github.com/epicai/epicai/backend/internal/ratelimit"
	"github.com/epicai/epicai/backend/internal/sessions"
	"github.com/epicai/epicai/backend/internal/storage"
	"github.com/epicai/epicai/backend/internal/vlog"
	adminui "github.com/epicai/epicai/backend/internal/web"
)

type routerDeps struct {
	store     storage.Store
	manager   *sessions.Manager
	bus       *events.Bus
	logger    *slog.Logger
	keyMgr    *auth.Manager
	adminAuth *auth.AdminAuth
	auditLog  *audit.Logger
	limiter   *ratelimit.Limiter
	assets    *assets.Store
	cfg       *config.Config
}

// newRouter wires the OpenAI surface, the admin surface and the admin UI. The
// two API surfaces are logically isolated: /v1/* is client facing, /admin/*
// requires administrator authentication.
func newRouter(d routerDeps) http.Handler {
	mux := http.NewServeMux()

	openaiSrv := api.NewServer(api.Deps{
		Store: d.store, Manager: d.manager, Auth: d.keyMgr, Bus: d.bus, Log: d.logger,
		Assets: assetAdapter{s: d.assets},
	})

	adminSrv := adminapi.New(adminapi.Deps{
		Store: d.store, Manager: d.manager, Bus: d.bus, Admin: d.adminAuth,
		Limiter: d.limiter, Audit: d.auditLog, Log: d.logger,
		StreamOf: func(sessionID string) *adminapi.StreamView {
			sw := openaiSrv.StreamWriter(sessionID)
			if sw == nil {
				return nil
			}
			return &adminapi.StreamView{Frames: func() []adminapi.Frame {
				caps := sw.Captured()
				out := make([]adminapi.Frame, 0, len(caps))
				for _, c := range caps {
					out = append(out, adminapi.Frame{Seq: c.Seq, At: c.At, Data: c.Data})
				}
				return out
			}}
		},
	})

	// ---- health & readiness ----
	mux.HandleFunc("/health", openaiSrv.HandleHealth)
	mux.HandleFunc("/ready", openaiSrv.HandleReady)

	// ---- OpenAI compatible surface ----
	mux.HandleFunc("/v1/models", openaiSrv.HandleModels)
	mux.HandleFunc("/v1/models/", openaiSrv.HandleModels)
	mux.HandleFunc("/v1/chat/completions", openaiSrv.HandleChatCompletions)
	mux.HandleFunc("/v1/responses", openaiSrv.HandleResponses)
	mux.HandleFunc("/v1/files", openaiSrv.HandleFiles)
	mux.HandleFunc("/v1/files/", openaiSrv.HandleFiles)
	mux.HandleFunc("/epic-assets/", openaiSrv.HandleAssets)

	// ---- admin surface ----
	adminHandler := adminSrv.Routes()
	mux.Handle("/admin/api/", adminHandler)
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})
	mux.Handle("/admin/", adminui.Handler(d.logger))

	// ---- static / UI ----
	mux.Handle("/", adminui.Handler(d.logger))

	return vlog.HTTPMiddleware(mux)
}

// assetAdapter bridges the asset store to the API layer interface.
type assetAdapter struct{ s *assets.Store }

func (a assetAdapter) Put(kind, filename, mimeType string, r io.Reader, maxBytes int64, sessionID string) (*api.AssetPutResult, error) {
	res, err := a.s.Put(kind, filename, mimeType, r, maxBytes, sessionID)
	if err != nil {
		return nil, err
	}
	return &api.AssetPutResult{
		ID: res.ID, Path: res.Path, Size: res.Size, SHA256: res.SHA256, MimeType: res.MimeType,
	}, nil
}

func (a assetAdapter) Open(kind, id string) (io.ReadCloser, error) { return a.s.Open(kind, id) }
func (a assetAdapter) Path(kind, id string) string                 { return a.s.Path(kind, id) }
func (a assetAdapter) Root() string                                { return a.s.Root() }
func (a assetAdapter) TotalBytes() int64                           { return a.s.TotalBytes() }
