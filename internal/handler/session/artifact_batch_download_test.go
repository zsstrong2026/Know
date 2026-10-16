package session

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// multiArtifactFileService serves several URLs at once, so one request can
// bundle artifacts from different messages/sessions.
type multiArtifactFileService struct {
	interfaces.FileService
	files map[string][]byte
	calls int
}

func (f *multiArtifactFileService) GetFile(_ context.Context, url string) (io.ReadCloser, error) {
	f.calls++
	data, ok := f.files[url]
	if !ok {
		return nil, stderrors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func newBatchArtifactTestRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(42)))
		c.Next()
	})
	r.POST("/artifacts/download", h.BatchDownloadArtifacts)
	return r
}

func postBatchDownload(router *gin.Engine, items []batchArtifactDownloadItem) *httptest.ResponseRecorder {
	body, err := json.Marshal(batchArtifactDownloadRequest{Items: items})
	if err != nil {
		panic(err)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/artifacts/download", bytes.NewReader(body)))
	return w
}

func zipEntryNames(t *testing.T, body []byte) []string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("response is not a readable zip: %v", err)
	}
	names := make([]string, 0, len(reader.File))
	for _, f := range reader.File {
		names = append(names, f.Name)
	}
	return names
}

func zipEntryContent(t *testing.T, zr *zip.Reader, name string) string {
	t.Helper()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", name, err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read zip entry %s: %v", name, err)
		}
		return string(data)
	}
	t.Fatalf("zip entry %s not found", name)
	return ""
}

func batchDownloadHandler(files map[string][]byte, messageIDs ...string) *Handler {
	return &Handler{
		sessionService: &stubSessionServiceForArtifacts{
			getSession: func(_ context.Context, id string) (*types.Session, error) {
				if id != "sess-1" {
					return nil, apperrors.ErrSessionNotFound
				}
				return &types.Session{ID: id, TenantID: 42}, nil
			},
		},
		messageService: &stubMessageServiceForArtifacts{
			getMessage: func(_ context.Context, sid, mid string) (*types.Message, error) {
				if sid != "sess-1" {
					return nil, nil
				}
				for _, known := range messageIDs {
					if mid != known {
						continue
					}
					switch mid {
					case "msg-1":
						return &types.Message{
							ID: mid, SessionID: sid,
							Artifacts: types.MessageArtifacts{{
								URL: "local://42/exports/a.pdf", FileName: "report.pdf", FileType: ".pdf",
							}},
						}, nil
					case "msg-2":
						return &types.Message{
							ID: mid, SessionID: sid,
							Artifacts: types.MessageArtifacts{
								{
									URL: "local://42/exports/b.pdf", FileName: "report.pdf", FileType: ".pdf",
								},
								{
									URL: "local://42/exports/evil.txt", FileName: "../../../evil.txt", FileType: ".txt",
								},
							},
						}, nil
					}
				}
				return nil, nil
			},
		},
		fileService: &multiArtifactFileService{files: files},
	}
}

func TestBatchDownloadArtifacts_HappyPathDedupesNames(t *testing.T) {
	h := batchDownloadHandler(map[string][]byte{
		"local://42/exports/a.pdf": []byte("A-BYTES"),
		"local://42/exports/b.pdf": []byte("B-BYTES"),
	}, "msg-1", "msg-2")
	router := newBatchArtifactTestRouter(h)
	w := postBatchDownload(router, []batchArtifactDownloadItem{
		{SessionID: "sess-1", MessageID: "msg-1", Index: 0},
		{SessionID: "sess-1", MessageID: "msg-2", Index: 0},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("Content-Type = %q, want application/zip", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("Content-Disposition = %q, want attachment prefix", cd)
	}
	// Same file name in two messages: the second entry must be renamed, or
	// extraction silently overwrites the first file.
	names := zipEntryNames(t, w.Body.Bytes())
	if len(names) != 2 || names[0] != "report.pdf" || names[1] != "report (2).pdf" {
		t.Fatalf("entry names = %v, want [report.pdf report (2).pdf]", names)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("parse zip: %v", err)
	}
	if got := zipEntryContent(t, zr, "report.pdf"); got != "A-BYTES" {
		t.Fatalf("report.pdf content = %q", got)
	}
	if got := zipEntryContent(t, zr, "report (2).pdf"); got != "B-BYTES" {
		t.Fatalf("report (2).pdf content = %q", got)
	}
}

func TestBatchDownloadArtifacts_SkipsInaccessibleAndSanitizesNames(t *testing.T) {
	h := batchDownloadHandler(map[string][]byte{
		"local://42/exports/a.pdf": []byte("A-BYTES"),
		// b.pdf bytes exist but the blob for evil.txt is deliberately absent
		// to exercise the "open before entry" ordering.
		"local://42/exports/b.pdf": []byte("B-BYTES"),
	}, "msg-1", "msg-2")
	router := newBatchArtifactTestRouter(h)
	w := postBatchDownload(router, []batchArtifactDownloadItem{
		{SessionID: "sess-1", MessageID: "msg-1", Index: 0},
		// Unknown session: dropped silently instead of failing the bundle.
		{SessionID: "sess-x", MessageID: "msg-9", Index: 0},
		// Out-of-range index on msg-2: dropped as well.
		{SessionID: "sess-1", MessageID: "msg-2", Index: 9},
		// Traversal name + missing blob: skipped without a truncated entry.
		{SessionID: "sess-1", MessageID: "msg-2", Index: 1},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	names := zipEntryNames(t, w.Body.Bytes())
	if len(names) != 1 || names[0] != "report.pdf" {
		t.Fatalf("entry names = %v, want exactly [report.pdf]", names)
	}
	for _, name := range names {
		if strings.Contains(name, "..") {
			t.Fatalf("entry name %q retains traversal fragment", name)
		}
	}
}

func TestBatchDownloadArtifacts_NothingDownloadable(t *testing.T) {
	h := batchDownloadHandler(map[string][]byte{}, "msg-1")
	router := newBatchArtifactTestRouter(h)
	w := postBatchDownload(router, []batchArtifactDownloadItem{
		{SessionID: "sess-x", MessageID: "msg-1", Index: 0},
	})

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when every item is inaccessible", w.Code)
	}
}

func TestBatchDownloadArtifacts_RequestValidation(t *testing.T) {
	router := newBatchArtifactTestRouter(batchDownloadHandler(map[string][]byte{}))

	if w := postBatchDownload(router, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("empty items: status = %d, want 400", w.Code)
	}
	tooMany := make([]batchArtifactDownloadItem, maxBatchArtifactDownloads+1)
	for i := range tooMany {
		tooMany[i] = batchArtifactDownloadItem{SessionID: "sess-1", MessageID: "msg-1"}
	}
	if w := postBatchDownload(router, tooMany); w.Code != http.StatusBadRequest {
		t.Fatalf("over cap: status = %d, want 400", w.Code)
	}
}

func TestSanitizeZipEntryName(t *testing.T) {
	cases := map[string]string{
		"../../../evil.txt": "evil.txt",
		`..\..\evil.txt`:    "evil.txt",
		"a/b/c.csv":         "c.csv",
		"  spaced.txt  ":    "spaced.txt",
		"":                  "file",
		"normal.md":         "normal.md",
	}
	for input, want := range cases {
		if got := sanitizeZipEntryName(input); got != want {
			t.Fatalf("sanitizeZipEntryName(%q) = %q, want %q", input, got, want)
		}
	}
}
