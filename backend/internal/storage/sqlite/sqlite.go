// Package sqlite implements the storage.Store contract on top of SQLite using a
// pure-Go driver (no CGO), so the binary builds anywhere.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/epicai/epicai/backend/internal/storage"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(dsn string) (*Store, error) {
	path := strings.TrimPrefix(dsn, "sqlite:")
	if path == "" {
		path = "data/epicai.db"
	}
	if path != ":memory:" {
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create db dir: %w", err)
			}
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return s.db.PingContext(ctx)
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS models (
			model_id TEXT PRIMARY KEY,
			display_name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			behavior TEXT NOT NULL,
			echo_interval_ms INTEGER NOT NULL DEFAULT 500,
			protocol_mode TEXT NOT NULL DEFAULT 'openai',
			scenario_id TEXT,
			description TEXT,
			static_response TEXT,
			error_status INTEGER,
			error_code TEXT,
			error_type TEXT,
			error_message TEXT,
			token_rate INTEGER DEFAULT 0,
			echo_content_mode TEXT,
			max_echo_count INTEGER DEFAULT 0,
			metadata TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			session_id TEXT PRIMARY KEY,
			request_id TEXT,
			protocol TEXT,
			model TEXT,
			streaming INTEGER,
			client_ip TEXT,
			user_agent TEXT,
			key_fingerprint TEXT,
			state TEXT,
			mode TEXT,
			created_at DATETIME,
			updated_at DATETIME,
			ended_at DATETIME,
			echo_count INTEGER DEFAULT 0,
			bytes_in INTEGER DEFAULT 0,
			bytes_out INTEGER DEFAULT 0,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			chunk_count INTEGER DEFAULT 0,
			current_rate REAL DEFAULT 0,
			average_rate REAL DEFAULT 0,
			peak_rate REAL DEFAULT 0,
			echo_interval_ms INTEGER DEFAULT 500,
			echo_content_mode TEXT,
			rate_config TEXT,
			raw_request TEXT,
			scenario_id TEXT,
			finish_reason TEXT,
			end_reason TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_created ON sessions(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_state ON sessions(state)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_model ON sessions(model)`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			kind TEXT NOT NULL,
			role TEXT,
			content TEXT,
			bytes INTEGER DEFAULT 0,
			tokens INTEGER DEFAULT 0,
			created_at DATETIME
		)`,
		`CREATE INDEX IF NOT EXISTS idx_events_session ON events(session_id, id)`,
		`CREATE TABLE IF NOT EXISTS files (
			id TEXT PRIMARY KEY,
			filename TEXT,
			mime_type TEXT,
			bytes INTEGER,
			sha256 TEXT,
			purpose TEXT,
			uploaded_at DATETIME,
			session_id TEXT,
			key_fingerprint TEXT,
			storage_path TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS assets (
			id TEXT PRIMARY KEY,
			mime_type TEXT,
			path TEXT,
			size INTEGER,
			filename TEXT,
			session_id TEXT,
			source_kind TEXT,
			created_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id TEXT PRIMARY KEY,
			name TEXT,
			key_hash TEXT UNIQUE,
			fingerprint TEXT,
			prefix TEXT,
			suffix TEXT,
			enabled INTEGER DEFAULT 1,
			created_at DATETIME,
			last_used_at DATETIME,
			use_count INTEGER DEFAULT 0,
			models TEXT,
			max_sessions INTEGER DEFAULT 0,
			rate_limit INTEGER DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_keys_hash ON api_keys(key_hash)`,
		`CREATE TABLE IF NOT EXISTS scenarios (
			id TEXT PRIMARY KEY,
			name TEXT,
			description TEXT,
			steps TEXT,
			created_at DATETIME,
			updated_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp DATETIME,
			admin TEXT,
			session_id TEXT,
			action TEXT,
			params TEXT,
			ip TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit(timestamp DESC)`,
		`CREATE TABLE IF NOT EXISTS benchmarks (
			id TEXT PRIMARY KEY,
			timestamp DATETIME,
			protocol TEXT,
			chunk_size INTEGER,
			duration_ms INTEGER,
			tokens_sent INTEGER,
			average_token_rate REAL,
			peak_token_rate REAL,
			bytes_sent INTEGER,
			cpu_percent REAL,
			memory_mb REAL
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT
		)`,
	}
	for _, st := range stmts {
		if _, err := s.db.Exec(st); err != nil {
			return fmt.Errorf("migrate: %w\n%s", err, st)
		}
	}
	return nil
}

// ---------------- Models ----------------

func (s *Store) ListModels(ctx context.Context) ([]storage.Model, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT model_id,display_name,enabled,created_at,updated_at,behavior,
		echo_interval_ms,protocol_mode,COALESCE(scenario_id,''),COALESCE(description,''),COALESCE(static_response,''),
		COALESCE(error_status,0),COALESCE(error_code,''),COALESCE(error_type,''),COALESCE(error_message,''),
		COALESCE(token_rate,0),COALESCE(echo_content_mode,''),COALESCE(max_echo_count,0),COALESCE(metadata,'')
		FROM models ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanModels(rows)
}

func (s *Store) ListEnabledModels(ctx context.Context) ([]storage.Model, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT model_id,display_name,enabled,created_at,updated_at,behavior,
		echo_interval_ms,protocol_mode,COALESCE(scenario_id,''),COALESCE(description,''),COALESCE(static_response,''),
		COALESCE(error_status,0),COALESCE(error_code,''),COALESCE(error_type,''),COALESCE(error_message,''),
		COALESCE(token_rate,0),COALESCE(echo_content_mode,''),COALESCE(max_echo_count,0),COALESCE(metadata,'')
		FROM models WHERE enabled=1 ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanModels(rows)
}

func scanModels(rows *sql.Rows) ([]storage.Model, error) {
	var out []storage.Model
	for rows.Next() {
		var m storage.Model
		var meta string
		var enc int
		if err := rows.Scan(&m.ModelID, &m.DisplayName, &enc, &m.CreatedAt, &m.UpdatedAt, &m.Behavior,
			&m.EchoIntervalMS, &m.ProtocolMode, &m.ScenarioID, &m.Description, &m.StaticResponse,
			&m.ErrorStatus, &m.ErrorCode, &m.ErrorType, &m.ErrorMessage, &m.TokenRate,
			&m.EchoContentMode, &m.MaxEchoCount, &meta); err != nil {
			return nil, err
		}
		m.Enabled = enc != 0
		if meta != "" {
			_ = json.Unmarshal([]byte(meta), &m.Metadata)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetModel(ctx context.Context, id string) (*storage.Model, error) {
	row := s.db.QueryRowContext(ctx, `SELECT model_id,display_name,enabled,created_at,updated_at,behavior,
		echo_interval_ms,protocol_mode,COALESCE(scenario_id,''),COALESCE(description,''),COALESCE(static_response,''),
		COALESCE(error_status,0),COALESCE(error_code,''),COALESCE(error_type,''),COALESCE(error_message,''),
		COALESCE(token_rate,0),COALESCE(echo_content_mode,''),COALESCE(max_echo_count,0),COALESCE(metadata,'')
		FROM models WHERE model_id=?`, id)
	var m storage.Model
	var meta string
	var enc int
	err := row.Scan(&m.ModelID, &m.DisplayName, &enc, &m.CreatedAt, &m.UpdatedAt, &m.Behavior,
		&m.EchoIntervalMS, &m.ProtocolMode, &m.ScenarioID, &m.Description, &m.StaticResponse,
		&m.ErrorStatus, &m.ErrorCode, &m.ErrorType, &m.ErrorMessage, &m.TokenRate,
		&m.EchoContentMode, &m.MaxEchoCount, &meta)
	if err != nil {
		return nil, err
	}
	m.Enabled = enc != 0
	if meta != "" {
		_ = json.Unmarshal([]byte(meta), &m.Metadata)
	}
	return &m, nil
}

func (s *Store) CreateModel(ctx context.Context, m *storage.Model) error {
	meta, _ := json.Marshal(m.Metadata)
	now := time.Now()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO models (model_id,display_name,enabled,created_at,updated_at,behavior,
		echo_interval_ms,protocol_mode,scenario_id,description,static_response,error_status,error_code,error_type,
		error_message,token_rate,echo_content_mode,max_echo_count,metadata)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ModelID, m.DisplayName, boolInt(m.Enabled), m.CreatedAt, m.UpdatedAt, string(m.Behavior),
		m.EchoIntervalMS, m.ProtocolMode, nullStr(m.ScenarioID), nullStr(m.Description), nullStr(m.StaticResponse),
		m.ErrorStatus, nullStr(m.ErrorCode), nullStr(m.ErrorType), nullStr(m.ErrorMessage),
		m.TokenRate, nullStr(m.EchoContentMode), m.MaxEchoCount, string(meta))
	return err
}

func (s *Store) UpdateModel(ctx context.Context, m *storage.Model) error {
	meta, _ := json.Marshal(m.Metadata)
	m.UpdatedAt = time.Now()
	res, err := s.db.ExecContext(ctx, `UPDATE models SET display_name=?,enabled=?,updated_at=?,behavior=?,
		echo_interval_ms=?,protocol_mode=?,scenario_id=?,description=?,static_response=?,error_status=?,error_code=?,
		error_type=?,error_message=?,token_rate=?,echo_content_mode=?,max_echo_count=?,metadata=? WHERE model_id=?`,
		m.DisplayName, boolInt(m.Enabled), m.UpdatedAt, string(m.Behavior),
		m.EchoIntervalMS, m.ProtocolMode, nullStr(m.ScenarioID), nullStr(m.Description), nullStr(m.StaticResponse),
		m.ErrorStatus, nullStr(m.ErrorCode), nullStr(m.ErrorType), nullStr(m.ErrorMessage),
		m.TokenRate, nullStr(m.EchoContentMode), m.MaxEchoCount, string(meta), m.ModelID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteModel(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM models WHERE model_id=?`, id)
	return err
}

// ---------------- Sessions ----------------

func (s *Store) CreateSession(ctx context.Context, ses *storage.Session) error {
	rate, _ := json.Marshal(ses.Rate)
	req, _ := json.Marshal(ses.Request)
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (session_id,request_id,protocol,model,streaming,client_ip,
		user_agent,key_fingerprint,state,mode,created_at,updated_at,ended_at,echo_count,bytes_in,bytes_out,
		input_tokens,output_tokens,chunk_count,current_rate,average_rate,peak_rate,echo_interval_ms,echo_content_mode,
		rate_config,raw_request,scenario_id,finish_reason,end_reason)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ses.ID, ses.RequestID, ses.Protocol, ses.Model, boolInt(ses.Streaming), ses.ClientIP,
		ses.UserAgent, ses.KeyFingerprint, string(ses.State), string(ses.Mode), ses.CreatedAt, ses.UpdatedAt, ses.EndedAt,
		ses.EchoCount, ses.BytesIn, ses.BytesOut, ses.InputTokens, ses.OutputTokens, ses.ChunkCount,
		ses.CurrentRate, ses.AverageRate, ses.PeakRate, ses.EchoIntervalMS, ses.EchoContentMode,
		string(rate), string(req), nullStr(ses.ScenarioID), nullStr(ses.FinishReason), nullStr(ses.EndReason))
	return err
}

func (s *Store) UpdateSession(ctx context.Context, ses *storage.Session) error {
	rate, _ := json.Marshal(ses.Rate)
	ses.UpdatedAt = time.Now()
	var reqJSON any = nil
	if ses.Request != nil {
		b, _ := json.Marshal(ses.Request)
		reqJSON = string(b)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET request_id=?,protocol=?,model=?,streaming=?,client_ip=?,
		user_agent=?,key_fingerprint=?,state=?,mode=?,updated_at=?,ended_at=?,echo_count=?,bytes_in=?,bytes_out=?,
		input_tokens=?,output_tokens=?,chunk_count=?,current_rate=?,average_rate=?,peak_rate=?,echo_interval_ms=?,
		echo_content_mode=?,rate_config=?,raw_request=COALESCE(?,raw_request),scenario_id=?,finish_reason=?,end_reason=? WHERE session_id=?`,
		ses.RequestID, ses.Protocol, ses.Model, boolInt(ses.Streaming), ses.ClientIP,
		ses.UserAgent, ses.KeyFingerprint, string(ses.State), string(ses.Mode), ses.UpdatedAt, ses.EndedAt,
		ses.EchoCount, ses.BytesIn, ses.BytesOut, ses.InputTokens, ses.OutputTokens, ses.ChunkCount,
		ses.CurrentRate, ses.AverageRate, ses.PeakRate, ses.EchoIntervalMS, ses.EchoContentMode,
		string(rate), reqJSON, nullStr(ses.ScenarioID), nullStr(ses.FinishReason), nullStr(ses.EndReason), ses.ID)
	return err
}

func (s *Store) GetSession(ctx context.Context, id string) (*storage.Session, error) {
	row := s.db.QueryRowContext(ctx, `SELECT session_id,COALESCE(request_id,''),COALESCE(protocol,''),COALESCE(model,''),
		COALESCE(streaming,0),COALESCE(client_ip,''),COALESCE(user_agent,''),COALESCE(key_fingerprint,''),
		COALESCE(state,''),COALESCE(mode,''),created_at,COALESCE(updated_at,created_at),ended_at,
		COALESCE(echo_count,0),COALESCE(bytes_in,0),COALESCE(bytes_out,0),COALESCE(input_tokens,0),
		COALESCE(output_tokens,0),COALESCE(chunk_count,0),COALESCE(current_rate,0),COALESCE(average_rate,0),
		COALESCE(peak_rate,0),COALESCE(echo_interval_ms,500),COALESCE(echo_content_mode,''),
		COALESCE(rate_config,''),COALESCE(raw_request,''),COALESCE(scenario_id,''),COALESCE(finish_reason,''),COALESCE(end_reason,'')
		FROM sessions WHERE session_id=?`, id)
	return scanSession(row)
}

type scanner interface{ Scan(dest ...any) error }

func scanSession(row scanner) (*storage.Session, error) {
	var s storage.Session
	var str int
	var rawCreated, rawUpdated, rawEnded any
	var rate, req string
	err := row.Scan(&s.ID, &s.RequestID, &s.Protocol, &s.Model, &str, &s.ClientIP, &s.UserAgent,
		&s.KeyFingerprint, &s.State, &s.Mode, &rawCreated, &rawUpdated, &rawEnded,
		&s.EchoCount, &s.BytesIn, &s.BytesOut, &s.InputTokens, &s.OutputTokens, &s.ChunkCount,
		&s.CurrentRate, &s.AverageRate, &s.PeakRate, &s.EchoIntervalMS, &s.EchoContentMode,
		&rate, &req, &s.ScenarioID, &s.FinishReason, &s.EndReason)
	if err != nil {
		return nil, err
	}
	s.Streaming = str != 0
	s.CreatedAt = parseDBTime(rawCreated)
	s.UpdatedAt = parseDBTime(rawUpdated)
	if rawEnded != nil {
		if str, ok := rawEnded.(string); ok && str != "" {
			t := parseDBTime(str)
			s.EndedAt = &t
		} else if tm, ok := rawEnded.(time.Time); ok {
			s.EndedAt = &tm
		}
	}
	if rate != "" && rate != "null" {
		_ = json.Unmarshal([]byte(rate), &s.Rate)
	}
	if req != "" && req != "null" {
		var rr storage.RawRequest
		if err := json.Unmarshal([]byte(req), &rr); err == nil {
			s.Request = &rr
		}
	}
	return &s, nil
}

func (s *Store) ListSessions(ctx context.Context, f storage.SessionFilter) ([]storage.Session, error) {
	q, args := buildSessionQuery(f, false)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Session
	for rows.Next() {
		ses, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ses)
	}
	return out, rows.Err()
}

func (s *Store) CountSessions(ctx context.Context, f storage.SessionFilter) (int, error) {
	q, args := buildSessionQuery(f, true)
	var n int
	err := s.db.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

func buildSessionQuery(f storage.SessionFilter, count bool) (string, []any) {
	var cols string
	if count {
		cols = "SELECT COUNT(*)"
	} else {
		cols = `SELECT session_id,COALESCE(request_id,''),COALESCE(protocol,''),COALESCE(model,''),
		COALESCE(streaming,0),COALESCE(client_ip,''),COALESCE(user_agent,''),COALESCE(key_fingerprint,''),
		COALESCE(state,''),COALESCE(mode,''),created_at,COALESCE(updated_at,created_at),ended_at,
		COALESCE(echo_count,0),COALESCE(bytes_in,0),COALESCE(bytes_out,0),COALESCE(input_tokens,0),
		COALESCE(output_tokens,0),COALESCE(chunk_count,0),COALESCE(current_rate,0),COALESCE(average_rate,0),
		COALESCE(peak_rate,0),COALESCE(echo_interval_ms,500),COALESCE(echo_content_mode,''),
		COALESCE(rate_config,''),COALESCE(raw_request,''),COALESCE(scenario_id,''),COALESCE(finish_reason,''),COALESCE(end_reason,'')`
	}
	var sb strings.Builder
	var args []any
	sb.WriteString(cols + " FROM sessions WHERE 1=1")
	if f.Query != "" {
		sb.WriteString(" AND (session_id LIKE ? OR model LIKE ? OR client_ip LIKE ? OR COALESCE(request_id,'') LIKE ?)")
		like := "%" + f.Query + "%"
		args = append(args, like, like, like, like)
	}
	if f.Model != "" {
		sb.WriteString(" AND model = ?")
		args = append(args, f.Model)
	}
	if f.Protocol != "" {
		sb.WriteString(" AND protocol = ?")
		args = append(args, f.Protocol)
	}
	if f.State != "" {
		if f.State == "ACTIVE" {
			sb.WriteString(" AND state IN ('CONNECTED','ECHOING','PAUSED','MANUAL','ERROR_PENDING','WAITING_FOR_SLOT','ENDING')")
		} else {
			sb.WriteString(" AND state = ?")
			args = append(args, f.State)
		}
	}
	if f.ActiveOnly {
		sb.WriteString(" AND state IN ('CONNECTED','ECHOING','PAUSED','MANUAL','ERROR_PENDING','WAITING_FOR_SLOT','ENDING')")
	}
	if f.IP != "" {
		sb.WriteString(" AND client_ip = ?")
		args = append(args, f.IP)
	}
	if f.Key != "" {
		sb.WriteString(" AND key_fingerprint LIKE ?")
		args = append(args, "%"+f.Key+"%")
	}
	if f.Since != nil {
		sb.WriteString(" AND created_at >= ?")
		args = append(args, *f.Since)
	}
	if f.Until != nil {
		sb.WriteString(" AND created_at <= ?")
		args = append(args, *f.Until)
	}
	if !count {
		sb.WriteString(" ORDER BY created_at DESC")
		if f.Limit > 0 {
			sb.WriteString(fmt.Sprintf(" LIMIT %d", f.Limit))
			if f.Offset > 0 {
				sb.WriteString(fmt.Sprintf(" OFFSET %d", f.Offset))
			}
		} else {
			sb.WriteString(" LIMIT 200")
		}
	}
	return sb.String(), args
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE session_id=?`, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE session_id=?`, id)
	return err
}

func (s *Store) DeleteSessionsBefore(ctx context.Context, t time.Time) (int64, error) {
	ids, err := s.db.QueryContext(ctx, `SELECT session_id FROM sessions WHERE created_at < ?`, t)
	if err != nil {
		return 0, err
	}
	var list []string
	for ids.Next() {
		var id string
		_ = ids.Scan(&id)
		list = append(list, id)
	}
	ids.Close()
	for _, id := range list {
		_ = s.DeleteSession(ctx, id)
	}
	return int64(len(list)), nil
}

// ---------------- Events ----------------

func (s *Store) AppendEvents(ctx context.Context, evs []storage.Event) error {
	if len(evs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO events (session_id,seq,kind,role,content,bytes,tokens,created_at) VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	for _, e := range evs {
		if e.CreatedAt.IsZero() {
			e.CreatedAt = time.Now()
		}
		if _, err := stmt.ExecContext(ctx, e.SessionID, e.Seq, string(e.Kind), e.Role, e.Content, e.Bytes, e.Tokens, e.CreatedAt); err != nil {
			tx.Rollback()
			return err
		}
	}
	stmt.Close()
	return tx.Commit()
}

func (s *Store) ListEvents(ctx context.Context, f storage.EventFilter) ([]storage.Event, error) {
	q := `SELECT id,session_id,seq,kind,COALESCE(role,''),COALESCE(content,''),COALESCE(bytes,0),COALESCE(tokens,0),created_at FROM events WHERE session_id=?`
	args := []any{f.SessionID}
	if f.Kind != "" {
		q += " AND kind=?"
		args = append(args, f.Kind)
	}
	if f.AfterSeq > 0 {
		q += " AND seq > ?"
		args = append(args, f.AfterSeq)
	}
	q += " ORDER BY seq DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	} else {
		q += " LIMIT 500"
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Event
	for rows.Next() {
		var e storage.Event
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Seq, &e.Kind, &e.Role, &e.Content, &e.Bytes, &e.Tokens, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) CountEvents(ctx context.Context, sessionID string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE session_id=?`, sessionID).Scan(&n)
	return n, err
}

func (s *Store) DeleteEventsBefore(ctx context.Context, t time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE created_at < ?`, t)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---------------- Files ----------------

func (s *Store) CreateFile(ctx context.Context, f *storage.FileRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO files (id,filename,mime_type,bytes,sha256,purpose,uploaded_at,session_id,key_fingerprint,storage_path) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		f.ID, f.Filename, f.MimeType, f.Bytes, f.SHA256, f.Purpose, f.UploadedAt, nullStr(f.SessionID), nullStr(f.KeyFingerprint), nullStr(f.StoragePath))
	return err
}

func (s *Store) GetFile(ctx context.Context, id string) (*storage.FileRecord, error) {
	var f storage.FileRecord
	var sid, kf, sp sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,filename,mime_type,bytes,sha256,COALESCE(purpose,''),uploaded_at,session_id,key_fingerprint,storage_path FROM files WHERE id=?`, id).
		Scan(&f.ID, &f.Filename, &f.MimeType, &f.Bytes, &f.SHA256, &f.Purpose, &f.UploadedAt, &sid, &kf, &sp)
	if err != nil {
		return nil, err
	}
	f.SessionID = sid.String
	f.KeyFingerprint = kf.String
	f.StoragePath = sp.String
	return &f, nil
}

func (s *Store) ListFiles(ctx context.Context, limit, offset int) ([]storage.FileRecord, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,filename,mime_type,bytes,sha256,COALESCE(purpose,''),uploaded_at,COALESCE(session_id,''),COALESCE(key_fingerprint,''),COALESCE(storage_path,'') FROM files ORDER BY uploaded_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.FileRecord
	for rows.Next() {
		var f storage.FileRecord
		if err := rows.Scan(&f.ID, &f.Filename, &f.MimeType, &f.Bytes, &f.SHA256, &f.Purpose, &f.UploadedAt, &f.SessionID, &f.KeyFingerprint, &f.StoragePath); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) DeleteFile(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE id=?`, id)
	return err
}

// ---------------- Assets ----------------

func (s *Store) CreateAsset(ctx context.Context, a *storage.Asset) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO assets (id,mime_type,path,size,filename,session_id,source_kind,created_at) VALUES (?,?,?,?,?,?,?,?)`,
		a.ID, a.MimeType, a.Path, a.Size, nullStr(a.Filename), nullStr(a.SessionID), a.SourceKind, a.CreatedAt)
	return err
}

func (s *Store) GetAsset(ctx context.Context, id string) (*storage.Asset, error) {
	var a storage.Asset
	var fn, sid sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,mime_type,path,size,filename,session_id,source_kind,created_at FROM assets WHERE id=?`, id).
		Scan(&a.ID, &a.MimeType, &a.Path, &a.Size, &fn, &sid, &a.SourceKind, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	a.Filename = fn.String
	a.SessionID = sid.String
	return &a, nil
}

func (s *Store) ListAssets(ctx context.Context, limit, offset int) ([]storage.Asset, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,mime_type,path,size,COALESCE(filename,''),COALESCE(session_id,''),source_kind,created_at FROM assets ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Asset
	for rows.Next() {
		var a storage.Asset
		if err := rows.Scan(&a.ID, &a.MimeType, &a.Path, &a.Size, &a.Filename, &a.SessionID, &a.SourceKind, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) TotalAssetBytes(ctx context.Context) (int64, error) {
	var n sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT SUM(size) FROM assets`).Scan(&n)
	return n.Int64, err
}

// ---------------- API Keys ----------------

func (s *Store) CreateKey(ctx context.Context, k *storage.APIKey) error {
	models, _ := json.Marshal(k.Models)
	_, err := s.db.ExecContext(ctx, `INSERT INTO api_keys (id,name,key_hash,fingerprint,prefix,suffix,enabled,created_at,last_used_at,use_count,models,max_sessions,rate_limit) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		k.ID, k.Name, k.KeyHash, k.Fingerprint, k.Prefix, k.Suffix, boolInt(k.Enabled), k.CreatedAt, k.LastUsedAt, k.UseCount, string(models), k.MaxSessions, k.RateLimit)
	return err
}

func (s *Store) GetKeyByHash(ctx context.Context, hash string) (*storage.APIKey, error) {
	var k storage.APIKey
	var models string
	var last sql.NullTime
	var enc int
	err := s.db.QueryRowContext(ctx, `SELECT id,name,key_hash,fingerprint,COALESCE(prefix,''),COALESCE(suffix,''),enabled,created_at,last_used_at,use_count,COALESCE(models,''),COALESCE(max_sessions,0),COALESCE(rate_limit,0) FROM api_keys WHERE key_hash=?`, hash).
		Scan(&k.ID, &k.Name, &k.KeyHash, &k.Fingerprint, &k.Prefix, &k.Suffix, &enc, &k.CreatedAt, &last, &k.UseCount, &models, &k.MaxSessions, &k.RateLimit)
	if err != nil {
		return nil, err
	}
	k.Enabled = enc != 0
	if last.Valid {
		k.LastUsedAt = &last.Time
	}
	if models != "" {
		_ = json.Unmarshal([]byte(models), &k.Models)
	}
	return &k, nil
}

func (s *Store) ListKeys(ctx context.Context) ([]storage.APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,key_hash,fingerprint,COALESCE(prefix,''),COALESCE(suffix,''),enabled,created_at,last_used_at,use_count,COALESCE(models,''),COALESCE(max_sessions,0),COALESCE(rate_limit,0) FROM api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.APIKey
	for rows.Next() {
		var k storage.APIKey
		var models string
		var last sql.NullTime
		var enc int
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyHash, &k.Fingerprint, &k.Prefix, &k.Suffix, &enc, &k.CreatedAt, &last, &k.UseCount, &models, &k.MaxSessions, &k.RateLimit); err != nil {
			return nil, err
		}
		k.Enabled = enc != 0
		if last.Valid {
			k.LastUsedAt = &last.Time
		}
		if models != "" {
			_ = json.Unmarshal([]byte(models), &k.Models)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) UpdateKey(ctx context.Context, k *storage.APIKey) error {
	models, _ := json.Marshal(k.Models)
	_, err := s.db.ExecContext(ctx, `UPDATE api_keys SET name=?,enabled=?,models=?,max_sessions=?,rate_limit=?,last_used_at=?,use_count=? WHERE id=?`,
		k.Name, boolInt(k.Enabled), string(models), k.MaxSessions, k.RateLimit, k.LastUsedAt, k.UseCount, k.ID)
	return err
}

func (s *Store) DeleteKey(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id=?`, id)
	return err
}

func (s *Store) TouchKey(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE api_keys SET use_count=use_count+1,last_used_at=? WHERE id=?`, time.Now(), id)
	return err
}

// ---------------- Scenarios ----------------

func (s *Store) CreateScenario(ctx context.Context, sc *storage.Scenario) error {
	steps, _ := json.Marshal(sc.Steps)
	if sc.CreatedAt.IsZero() {
		sc.CreatedAt = time.Now()
	}
	sc.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO scenarios (id,name,description,steps,created_at,updated_at) VALUES (?,?,?,?,?,?)`,
		sc.ID, sc.Name, nullStr(sc.Description), string(steps), sc.CreatedAt, sc.UpdatedAt)
	return err
}

func (s *Store) GetScenario(ctx context.Context, id string) (*storage.Scenario, error) {
	var sc storage.Scenario
	var steps string
	var desc sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,name,description,steps,created_at,updated_at FROM scenarios WHERE id=?`, id).
		Scan(&sc.ID, &sc.Name, &desc, &steps, &sc.CreatedAt, &sc.UpdatedAt)
	if err != nil {
		return nil, err
	}
	sc.Description = desc.String
	if steps != "" {
		_ = json.Unmarshal([]byte(steps), &sc.Steps)
	}
	return &sc, nil
}

func (s *Store) ListScenarios(ctx context.Context) ([]storage.Scenario, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,steps,created_at,updated_at FROM scenarios ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Scenario
	for rows.Next() {
		var sc storage.Scenario
		var steps string
		var desc sql.NullString
		if err := rows.Scan(&sc.ID, &sc.Name, &desc, &steps, &sc.CreatedAt, &sc.UpdatedAt); err != nil {
			return nil, err
		}
		sc.Description = desc.String
		if steps != "" {
			_ = json.Unmarshal([]byte(steps), &sc.Steps)
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *Store) UpdateScenario(ctx context.Context, sc *storage.Scenario) error {
	steps, _ := json.Marshal(sc.Steps)
	sc.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx, `UPDATE scenarios SET name=?,description=?,steps=?,updated_at=? WHERE id=?`,
		sc.Name, nullStr(sc.Description), string(steps), sc.UpdatedAt, sc.ID)
	return err
}

func (s *Store) DeleteScenario(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scenarios WHERE id=?`, id)
	return err
}

// ---------------- Audit ----------------

func (s *Store) AppendAudit(ctx context.Context, a *storage.AuditEntry) error {
	if a.Timestamp.IsZero() {
		a.Timestamp = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit (timestamp,admin,session_id,action,params,ip) VALUES (?,?,?,?,?,?)`,
		a.Timestamp, a.Admin, nullStr(a.SessionID), a.Action, nullStr(a.Params), nullStr(a.IP))
	return err
}

func (s *Store) ListAudit(ctx context.Context, limit, offset int) ([]storage.AuditEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,timestamp,admin,COALESCE(session_id,''),action,COALESCE(params,''),COALESCE(ip,'') FROM audit ORDER BY timestamp DESC, id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.AuditEntry
	for rows.Next() {
		var a storage.AuditEntry
		if err := rows.Scan(&a.ID, &a.Timestamp, &a.Admin, &a.SessionID, &a.Action, &a.Params, &a.IP); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------------- Benchmarks ----------------

func (s *Store) SaveBenchmark(ctx context.Context, b *storage.Benchmark) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO benchmarks (id,timestamp,protocol,chunk_size,duration_ms,tokens_sent,average_token_rate,peak_token_rate,bytes_sent,cpu_percent,memory_mb) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		b.ID, b.Timestamp, b.Protocol, b.ChunkSize, b.DurationMS, b.TokensSent, b.AverageRate, b.PeakRate, b.BytesSent, b.CPUPercent, b.MemoryMB)
	return err
}

func (s *Store) ListBenchmarks(ctx context.Context, limit int) ([]storage.Benchmark, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,timestamp,protocol,chunk_size,duration_ms,tokens_sent,average_token_rate,peak_token_rate,bytes_sent,cpu_percent,memory_mb FROM benchmarks ORDER BY timestamp DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storage.Benchmark
	for rows.Next() {
		var b storage.Benchmark
		if err := rows.Scan(&b.ID, &b.Timestamp, &b.Protocol, &b.ChunkSize, &b.DurationMS, &b.TokensSent, &b.AverageRate, &b.PeakRate, &b.BytesSent, &b.CPUPercent, &b.MemoryMB); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) LatestBenchmark(ctx context.Context, protocol string) (*storage.Benchmark, error) {
	var b storage.Benchmark
	err := s.db.QueryRowContext(ctx, `SELECT id,timestamp,protocol,chunk_size,duration_ms,tokens_sent,average_token_rate,peak_token_rate,bytes_sent,cpu_percent,memory_mb FROM benchmarks WHERE protocol=? ORDER BY timestamp DESC LIMIT 1`, protocol).
		Scan(&b.ID, &b.Timestamp, &b.Protocol, &b.ChunkSize, &b.DurationMS, &b.TokensSent, &b.AverageRate, &b.PeakRate, &b.BytesSent, &b.CPUPercent, &b.MemoryMB)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ---------------- Settings ----------------

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err != nil {
		return "", err
	}
	return v, nil
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) AllSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key,value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func mustJSON(v any) string {
	if v == nil {
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func parseDBTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		for _, layout := range []string{
			time.RFC3339Nano, time.RFC3339,
			"2006-01-02 15:04:05.999999999-07:00",
			"2006-01-02 15:04:05.999999999",
			"2006-01-02 15:04:05-07:00",
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05",
		} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return parsed
			}
		}
	}
	return time.Now()
}
