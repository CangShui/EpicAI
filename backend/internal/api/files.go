package api

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/epicai/epicai/backend/internal/adapters/openai"
	"github.com/epicai/epicai/backend/internal/config"
	"github.com/epicai/epicai/backend/internal/storage"
	"github.com/epicai/epicai/backend/internal/vlog"
)

// FileObject mirrors the OpenAI Files API object.
type FileObject struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Bytes     int64  `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
	MimeType  string `json:"mime_type,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Status    string `json:"status,omitempty"`
}

type FileList struct {
	Object  string       `json:"object"`
	Data    []FileObject `json:"data"`
	HasMore bool         `json:"has_more"`
}

type DeleteResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}

// HandleFiles serves /v1/files and /v1/files/{id}[/content].
func (s *Server) HandleFiles(w http.ResponseWriter, r *http.Request) {
	traceID := vlog.TraceID(r.Context())
	if openai.ApplyCORS(w, r) {
		vlog.MiddlewareCORS(traceID, r.Header.Get("Origin"), string(config.C().Runtime().CORSMode), true)
		return
	}
	fp, ok, _ := s.resolveKey(w, r)
	if !ok {
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/files"), "/")
	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			s.listFiles(w, r)
		case http.MethodPost:
			s.uploadFile(w, r, fp)
		default:
			vlog.RequestBlocked(traceID, "文件方法校验", "不支持的方法: "+r.Method, 405, "请求未进入业务逻辑", "支持GET与POST")
			jsonErr(w, 405, "method_not_allowed", "invalid_request_error", "Method not allowed")
		}
		return
	}

	parts := strings.Split(rest, "/")
	id := parts[0]
	isContent := len(parts) > 1 && parts[1] == "content"

	switch r.Method {
	case http.MethodGet:
		if isContent {
			s.downloadFile(w, r, id)
		} else {
			s.getFile(w, r, id)
		}
	case http.MethodDelete:
		s.deleteFile(w, r, id)
	default:
		vlog.RequestBlocked(traceID, "文件操作校验", "不支持的方法: "+r.Method, 405, "请求未进入业务逻辑", "支持GET与DELETE")
		jsonErr(w, 405, "method_not_allowed", "invalid_request_error", "Method not allowed")
	}
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	traceID := vlog.TraceID(r.Context())
	vlog.BizEntry(traceID, "list_files", "列出文件列表", "")
	files, err := s.deps.Store.ListFiles(r.Context(), 200, 0)
	if err != nil {
		vlog.ErrorOccurred(traceID, "查询文件列表", "db_error", err.Error(), 500, "无法获取文件列表", true)
		jsonErr(w, 500, "internal_error", "server_error", "Failed to list files")
		return
	}
	vlog.DBAudit(traceID, "查询文件列表", "limit=200 offset=0", fmt.Sprintf("查到%d条", len(files)), int64(len(files)))
	out := FileList{Object: "list", Data: []FileObject{}}
	for i := range files {
		out.Data = append(out.Data, toFileObject(&files[i]))
	}
	writeJSON(w, 200, out)
}

func (s *Server) getFile(w http.ResponseWriter, r *http.Request, id string) {
	traceID := vlog.TraceID(r.Context())
	vlog.BizEntry(traceID, "get_file", "获取文件元数据", "id="+id)
	f, err := s.deps.Store.GetFile(r.Context(), id)
	if err != nil || f == nil {
		vlog.RequestBlocked(traceID, "文件存在性校验", "文件不存在: "+id, 404, "无法返回文件元数据", "请检查file_id")
		jsonErr(w, 404, "file_not_found", "invalid_request_error", "File not found: "+id)
		return
	}
	vlog.DBAudit(traceID, "查询单文件元数据", "id="+id, "成功", 1)
	writeJSON(w, 200, toFileObject(f))
}

func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request, id string) {
	traceID := vlog.TraceID(r.Context())
	vlog.BizEntry(traceID, "download_file", "下载文件内容", "id="+id)
	f, err := s.deps.Store.GetFile(r.Context(), id)
	if err != nil || f == nil {
		vlog.RequestBlocked(traceID, "文件存在性校验", "文件不存在: "+id, 404, "无法下载文件内容", "请检查file_id")
		jsonErr(w, 404, "file_not_found", "invalid_request_error", "File not found: "+id)
		return
	}
	path := f.StoragePath
	if path == "" && s.deps.Assets != nil {
		path = s.deps.Assets.Path("file", f.ID)
	}
	fh, err := os.Open(path)
	if err != nil {
		vlog.ErrorOccurred(traceID, "打开存储文件", "file_content_missing", err.Error(), 404, "磁盘文件丢失", true)
		jsonErr(w, 404, "file_content_unavailable", "server_error", "Stored file content is missing")
		return
	}
	defer fh.Close()
	w.Header().Set("Content-Type", f.MimeType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+f.Filename+`"`)
	w.Header().Set("X-EpicAI-SHA256", f.SHA256)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, fh)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request, id string) {
	traceID := vlog.TraceID(r.Context())
	vlog.BizEntry(traceID, "delete_file", "删除文件", "id="+id)
	f, err := s.deps.Store.GetFile(r.Context(), id)
	if err != nil || f == nil {
		vlog.RequestBlocked(traceID, "文件存在性校验", "文件不存在: "+id, 404, "无法删除", "请检查file_id")
		jsonErr(w, 404, "file_not_found", "invalid_request_error", "File not found: "+id)
		return
	}
	if f.StoragePath != "" {
		_ = os.Remove(f.StoragePath)
	}
	_ = s.deps.Store.DeleteFile(r.Context(), id)
	vlog.DBAudit(traceID, "删除文件记录", "id="+id, "成功", 1)
	writeJSON(w, 200, DeleteResponse{ID: id, Object: "file", Deleted: true})
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request, fp string) {
	traceID := vlog.TraceID(r.Context())
	maxBytes := config.C().Runtime().MaxFileSize
	if maxBytes <= 0 {
		maxBytes = 20 << 20
	}

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		// Limit the multipart read
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1024*1024)
		if err := r.ParseMultipartForm(maxBytes); err != nil {
			vlog.RequestBlocked(traceID, "文件体积校验", fmt.Sprintf("上传超过限制(%d字节)", maxBytes), 413, "拒绝文件上传，未进入存储", "请上传更小的文件")
			jsonErr(w, 413, "payload_too_large", "invalid_request_error", "Upload exceeds the maximum allowed size")
			return
		}
		s.doUploads(w, r, fp, maxBytes)
		return
	}
	name := r.URL.Query().Get("filename")
	if name == "" {
		name = "upload.bin"
	}
	purpose := r.URL.Query().Get("purpose")
	if purpose == "" {
		purpose = "assistants"
	}
	obj, ok := s.storeFile(w, r, name, "", io.LimitReader(r.Body, maxBytes+1), purpose, "", fp, maxBytes)
	if !ok {
		return
	}
	writeJSON(w, 200, obj)
}

func (s *Server) doUploads(w http.ResponseWriter, r *http.Request, fp string, maxBytes int64) {
	traceID := vlog.TraceID(r.Context())
	form := r.MultipartForm
	if form == nil || len(form.File["file"]) == 0 {
		vlog.RequestBlocked(traceID, "参数校验", "未找到名为file的文件表单项", 400, "文件上传失败", "请在表单中包含file字段")
		jsonErr(w, 400, "missing_file", "invalid_request_error", "No file part named 'file' was provided")
		return
	}
	var results []FileObject
	for _, fh := range form.File["file"] {
		f, err := fh.Open()
		if err != nil {
			vlog.ErrorOccurred(traceID, "读取上传文件", "file_open_failed", err.Error(), 400, "无法读取上传内容", true)
			jsonErr(w, 400, "file_open_failed", "invalid_request_error", "Could not read the uploaded file")
			return
		}
		purpose := "assistants"
		if v := form.Value["purpose"]; len(v) > 0 && v[0] != "" {
			purpose = v[0]
		}
		obj, ok := s.storeFile(w, r, fh.Filename, "", f, purpose, "", fp, maxBytes)
		f.Close()
		if !ok {
			return
		}
		results = append(results, obj)
	}
	if len(results) == 1 {
		writeJSON(w, 200, results[0])
		return
	}
	writeJSON(w, 200, FileList{Object: "list", Data: results})
}

func (s *Server) storeFile(w http.ResponseWriter, r *http.Request, filename, mimeType string,
	src io.Reader, purpose, sessionID, fp string, maxBytes int64) (FileObject, bool) {
	traceID := vlog.TraceID(r.Context())

	if s.deps.Assets == nil {
		vlog.ErrorOccurred(traceID, "文件存储", "storage_unavailable", "AssetStore未配置", 500, "无法落盘文件", true)
		jsonErr(w, 500, "storage_unavailable", "server_error", "Asset storage is not configured")
		return FileObject{}, false
	}
	res, err := s.deps.Assets.Put("file", filename, mimeType, src, maxBytes, sessionID)
	if err != nil {
		if strings.Contains(err.Error(), "too large") {
			vlog.RequestBlocked(traceID, "文件大小限制", fmt.Sprintf("单文件超过限制(%d字节)", maxBytes), 413, "文件被拒绝，未落盘", "请减小文件大小")
			jsonErr(w, 413, "payload_too_large", "invalid_request_error", "Upload exceeds the maximum allowed size")
		} else {
			vlog.ErrorOccurred(traceID, "文件写入磁盘", "storage_error", err.Error(), 500, "文件写入失败", true)
			jsonErr(w, 500, "storage_error", "server_error", "Failed to persist the uploaded file")
		}
		return FileObject{}, false
	}
	rec := storage.FileRecord{
		ID: res.ID, Filename: filename, MimeType: res.MimeType, Bytes: res.Size,
		SHA256: res.SHA256, Purpose: purpose, UploadedAt: time.Now(),
		SessionID: sessionID, KeyFingerprint: fp, StoragePath: res.Path,
	}
	if err := s.deps.Store.CreateFile(r.Context(), &rec); err != nil {
		vlog.ErrorOccurred(traceID, "记录文件元数据", "db_error", err.Error(), 500, "元数据写入DB失败", true)
		jsonErr(w, 500, "storage_error", "server_error", "Failed to record file metadata")
		return FileObject{}, false
	}
	vlog.DBAudit(traceID, "创建文件元数据", "id="+rec.ID+" filename="+filename+fmt.Sprintf(" bytes=%d sha256=%s", rec.Bytes, rec.SHA256), "成功", 1)
	return toFileObject(&rec), true
}

func toFileObject(f *storage.FileRecord) FileObject {
	return FileObject{
		ID: f.ID, Object: "file", Bytes: f.Bytes, CreatedAt: f.UploadedAt.Unix(),
		Filename: f.Filename, Purpose: f.Purpose, MimeType: f.MimeType,
		SHA256: f.SHA256, SessionID: f.SessionID, Status: "processed",
	}
}

var _ = multipart.FileHeader{}
