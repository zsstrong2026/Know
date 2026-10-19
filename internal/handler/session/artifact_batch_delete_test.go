package session

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// errArtifactGone stands in for any per-item failure (missing artifact,
// someone else's session — the handler treats them all as "skip").
var errArtifactGone = stderrors.New("artifact gone")

// batchDeleteStub dispatches per message so one request can mix deletable and
// inaccessible items, mirroring the real per-item behaviour of the service.
type batchDeleteStub struct {
	interfaces.MessageService
	results map[string]*types.ArtifactDeleteResult
	errs    map[string]error
	got     []*types.ArtifactDeleteRequest
}

func (s *batchDeleteStub) DeleteSessionArtifact(
	_ context.Context, req *types.ArtifactDeleteRequest,
) (*types.ArtifactDeleteResult, error) {
	s.got = append(s.got, req)
	if err, ok := s.errs[req.MessageID]; ok {
		return nil, err
	}
	if res, ok := s.results[req.MessageID]; ok {
		return res, nil
	}
	return nil, nil
}

func newBatchDeleteRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(42)))
		c.Next()
	})
	r.POST("/artifacts/batch-delete", h.BatchDeleteArtifacts)
	return r
}

func postBatchDelete(t *testing.T, h *Handler, items []batchArtifactDeleteItem) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(batchArtifactDeleteRequest{Items: items})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	w := httptest.NewRecorder()
	newBatchDeleteRouter(h).ServeHTTP(w,
		httptest.NewRequest(http.MethodPost, "/artifacts/batch-delete", bytes.NewReader(body)))
	return w
}

func batchDeleteResult(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, body)
	}
	return out.Data
}

func TestBatchDeleteArtifactsDeletesEveryItem(t *testing.T) {
	svc := &batchDeleteStub{results: map[string]*types.ArtifactDeleteResult{
		"msg-1": {FileName: "a.pdf", Deleted: 1, Reclaim: []types.ArtifactBlobRef{
			{URL: "resource://a", MessageIDs: []string{"msg-1"}},
		}},
		"msg-2": {FileName: "b.docx", Deleted: 2},
	}}
	files := &deletingFileService{}
	h := &Handler{messageService: svc, resourceCatalog: &deleteCatalogStub{resource: &types.StoredResource{
		TenantID: 42, PhysicalPath: "local://42/a.pdf",
	}}, fileService: files}

	w := postBatchDelete(t, h, []batchArtifactDeleteItem{
		{SessionID: "sess-1", MessageID: "msg-1", Index: 0},
		{SessionID: "sess-1", MessageID: "msg-2", Index: 3},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	data := batchDeleteResult(t, w.Body.Bytes())
	if data["requested"] != float64(2) {
		t.Fatalf("requested = %v, want 2", data["requested"])
	}
	deleted, _ := data["deleted"].([]any)
	if len(deleted) != 2 {
		t.Fatalf("deleted = %v, want 2 entries", data["deleted"])
	}
	for _, req := range svc.got {
		if !req.AllVersions {
			t.Fatalf("item %+v: a library row is the file, so all_versions must be set", req)
		}
	}
	// Reclaim runs after the loop: the blob returned by msg-1 must be deleted.
	if len(files.deleted) != 1 || files.deleted[0] != "resource://a" {
		t.Fatalf("deleted blobs = %v, want [resource://a]", files.deleted)
	}
}

func TestBatchDeleteArtifactsSkipsFailedItems(t *testing.T) {
	svc := &batchDeleteStub{
		results: map[string]*types.ArtifactDeleteResult{"msg-1": {FileName: "a.pdf", Deleted: 1}},
		errs:    map[string]error{"msg-gone": errArtifactGone, "msg-foreign": errArtifactGone},
	}
	h := &Handler{messageService: svc, fileService: &deletingFileService{}}

	w := postBatchDelete(t, h, []batchArtifactDeleteItem{
		{SessionID: "sess-1", MessageID: "msg-gone", Index: 0},
		{SessionID: "sess-1", MessageID: "msg-1", Index: 1},
		{SessionID: "other", MessageID: "msg-foreign", Index: 0},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	data := batchDeleteResult(t, w.Body.Bytes())
	deleted, _ := data["deleted"].([]any)
	if len(deleted) != 1 {
		t.Fatalf("deleted = %v, want only msg-1", data["deleted"])
	}
}

func TestBatchDeleteArtifactsNothingDeletable(t *testing.T) {
	svc := &batchDeleteStub{errs: map[string]error{"msg-1": errArtifactGone}}
	h := &Handler{messageService: svc, fileService: &deletingFileService{}}

	w := postBatchDelete(t, h, []batchArtifactDeleteItem{
		{SessionID: "sess-1", MessageID: "msg-1", Index: 0},
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
	}
}

func TestBatchDeleteArtifactsRequestValidation(t *testing.T) {
	h := &Handler{messageService: &batchDeleteStub{}, fileService: &deletingFileService{}}
	r := newBatchDeleteRouter(h)

	// 空 items
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/artifacts/batch-delete",
		strings.NewReader(`{"items":[]}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty items: status = %d, want 400", w.Code)
	}

	// 非法 JSON
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/artifacts/batch-delete",
		strings.NewReader(`{`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad json: status = %d, want 400", w.Code)
	}

	// 超过上限（与批量下载共用 maxBatchArtifactDownloads）
	items := make([]batchArtifactDeleteItem, maxBatchArtifactDownloads+1)
	for i := range items {
		items[i] = batchArtifactDeleteItem{SessionID: "s", MessageID: "m", Index: 0}
	}
	body, _ := json.Marshal(batchArtifactDeleteRequest{Items: items})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/artifacts/batch-delete", bytes.NewReader(body)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("too many items: status = %d, want 400", w.Code)
	}
}
