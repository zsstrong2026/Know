package session

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// maxBatchArtifactDownloads caps how many files one request may bundle. The
// cap keeps a single zip from pinning connections and memory for an
// unbounded amount of time; the client selects what it sends.
const maxBatchArtifactDownloads = 100

// batchArtifactDownloadRequest addresses artifacts the same way the artifact
// library rows do: session + message + index. Each item re-runs the full
// ownership check, so a stale or forged list cannot read anyone else's files.
type batchArtifactDownloadRequest struct {
	Items []batchArtifactDownloadItem `json:"items"`
}

type batchArtifactDownloadItem struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	Index     int    `json:"index"`
}

// batchArtifactFile is one artifact that passed every check and is ready to
// be streamed into the zip. The context travels with the file service
// because per-tenant storage resolution returns a tenant-scoped context.
type batchArtifactFile struct {
	fileService interfaces.FileService
	ctx         context.Context
	path        string
	fileName    string
}

// BatchDownloadArtifacts godoc
// @Summary      批量下载产物
// @Description  将多个会话产物打包为 ZIP 下载；每个文件单独校验会话归属，不可访问的文件会被跳过
// @Tags         会话
// @Accept       json
// @Produce      application/zip
// @Param        request  body  batchArtifactDownloadRequest  true  "批量下载请求"
// @Success      200  {file}  file  "ZIP 压缩包"
// @Failure      400  {object}  errors.AppError  "请求参数错误"
// @Failure      404  {object}  errors.AppError  "没有任何可下载的产物"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /artifacts/download [post]
//
// The endpoint backs the artifact library's "batch download" action: the
// client sends the rows the user ticked, and gets one zip back instead of N
// browser downloads (which browsers throttle or block).
//
// Failures are per-item, not per-request: an artifact deleted or revoked
// between listing and downloading is skipped and logged, and the rest of the
// bundle still arrives. Only when nothing is downloadable does the request
// fail outright — that way the status code still means "empty archive".
func (h *Handler) BatchDownloadArtifacts(c *gin.Context) {
	ctx := c.Request.Context()

	var req batchArtifactDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(errors.NewBadRequestError("invalid request"))
		return
	}
	if len(req.Items) == 0 {
		_ = c.Error(errors.NewBadRequestError("items are required"))
		return
	}
	if len(req.Items) > maxBatchArtifactDownloads {
		_ = c.Error(errors.NewBadRequestError(fmt.Sprintf(
			"too many items: %d (max %d)", len(req.Items), maxBatchArtifactDownloads)))
		return
	}

	files := make([]batchArtifactFile, 0, len(req.Items))
	for _, item := range req.Items {
		file, ok := h.resolveBatchArtifact(ctx, item)
		if !ok {
			continue
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		_ = c.Error(errors.NewNotFoundError("no downloadable artifacts"))
		return
	}

	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", buildAttachmentHeader(zipArchiveName(time.Now())))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, no-store")

	zw := zip.NewWriter(c.Writer)
	defer zw.Close()
	used := make(map[string]bool, len(files))
	for _, file := range files {
		// Open the blob before creating the entry: a blob that cannot be read
		// must not leave a truncated zero-byte file inside the archive, and a
		// skipped file must not reserve an entry name either.
		reader, err := file.fileService.GetFile(file.ctx, file.path)
		if err != nil {
			logger.Warnf(ctx, "artifact batch download: read %s failed: %v", file.fileName, err)
			continue
		}
		entryName := uniqueZipEntryName(sanitizeZipEntryName(file.fileName), used)
		entry, err := zw.CreateHeader(&zip.FileHeader{
			Name:     entryName,
			Modified: time.Now().UTC(),
		})
		if err != nil {
			reader.Close()
			logger.Warnf(ctx, "artifact batch download: create entry %s failed: %v", entryName, err)
			continue
		}
		if _, err := io.Copy(entry, reader); err != nil {
			logger.Warnf(ctx, "artifact batch download: stream %s failed: %v", entryName, err)
		}
		reader.Close()
	}
}

// resolveBatchArtifact runs the per-item access chain: session ownership →
// message lookup → artifact resolution → tenant storage resolution. It
// mirrors DownloadMessageArtifact, collapsing every failure into "skip this
// file" because one bad row must not sink the whole bundle.
func (h *Handler) resolveBatchArtifact(ctx context.Context, item batchArtifactDownloadItem) (batchArtifactFile, bool) {
	sessionID := secutils.SanitizeForLog(item.SessionID)
	messageID := secutils.SanitizeForLog(item.MessageID)
	if sessionID == "" || messageID == "" || item.Index < 0 {
		return batchArtifactFile{}, false
	}
	// Ownership check: a session that is missing or belongs to someone else
	// is indistinguishable from a typo — skip without leaking which.
	if _, err := h.sessionService.GetSession(ctx, sessionID); err != nil {
		logger.Warnf(ctx, "artifact batch download: session %s rejected: %v", sessionID, err)
		return batchArtifactFile{}, false
	}
	msg, err := h.messageService.GetMessage(ctx, sessionID, messageID)
	if err != nil || msg == nil {
		logger.Warnf(ctx, "artifact batch download: message %s not found in session %s", messageID, sessionID)
		return batchArtifactFile{}, false
	}
	file, err := access.ResolveMessageArtifact(ctx, msg, item.Index, h.agentShareService, h.resourceCatalog,
		access.MessageKBShareAuthorizer{ShareGuard: h.kbShareService, KBs: h.knowledgebaseService})
	if err != nil {
		logger.Warnf(ctx, "artifact batch download: artifact %s/%s/%d rejected: %v",
			sessionID, messageID, item.Index, err)
		return batchArtifactFile{}, false
	}
	fileService, fileCtx, ok := h.resolveArtifactFileService(
		ctx, file.OwnerTenantID, file.Path, file.StorageBackendID, "artifact batch download",
	)
	if !ok {
		logger.Warnf(ctx, "artifact batch download: storage unavailable for %s/%s/%d",
			sessionID, messageID, item.Index)
		return batchArtifactFile{}, false
	}
	return batchArtifactFile{
		fileService: fileService,
		ctx:         fileCtx,
		path:        file.Path,
		fileName:    file.Filename,
	}, true
}

// zipArchiveName names the bundle after the download moment, e.g.
// artifacts-20260102-150405.zip. Timestamps avoid collisions in the user's
// downloads folder and keep the name ASCII-only (Content-Disposition above
// handles non-ASCII, but archive names never need to be).
func zipArchiveName(now time.Time) string {
	return "artifacts-" + now.Format("20060102-150405") + ".zip"
}

// sanitizeZipEntryName flattens a stored file name into a safe zip entry
// name: path separators and traversal fragments would otherwise let a
// hostile row write outside the folder the user extracts into.
func sanitizeZipEntryName(name string) string {
	name = strings.TrimSpace(name)
	// Strip parent fragments first so "a/../b" cannot survive Base().
	name = strings.ReplaceAll(name, "..", "_")
	// Take the final path segment, handling both separators (zip entries
	// always use "/", but the stored name may carry Windows ones).
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	// path.Base("") returns "." and an all-slash path returns "/" — neither is
	// a real file name, so drop them before the empty-name fallback below.
	if name == "." || name == "/" {
		name = ""
	}
	// Drop control characters; a name left empty falls back to "file".
	var b strings.Builder
	for _, r := range name {
		if r >= 0x20 && r != 0x7f {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "file"
	}
	return b.String()
}

// uniqueZipEntryName de-duplicates entry names: different sessions can hold
// identically named artifacts, and a duplicate zip entry would silently
// shadow the first file on extraction. Collisions become "name (2).ext".
func uniqueZipEntryName(name string, used map[string]bool) string {
	if !used[name] {
		used[name] = true
		return name
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}
