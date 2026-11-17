// Package insight 实现工作台「洞察」模块：主题管理、两阶段分析、报告与自我洞悉。
//
// 分层约定与 workbench 一致：
//   - handler 只做参数绑定与响应封装；
//   - 本包每个方法都从 ctx 取 (tenantID, ownerID)，不接受调用方传入；
//   - 仓储的 gorm.ErrRecordNotFound 统一翻译成 2400（404），不用 403，
//     避免通过错误码探测他人资源是否存在。
package insight

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// WorkKBProvider 取（必要时懒创建）当前用户的私有工作知识库。
type WorkKBProvider interface {
	EnsureWorkKnowledgeBase(ctx context.Context, tenantID uint64, userID string) (*types.KnowledgeBase, error)
}

// KnowledgeProvider 只暴露洞察需要的知识读取能力。
type KnowledgeProvider interface {
	ListKnowledgeByKnowledgeBaseID(ctx context.Context, tenantID uint64, kbID string) ([]*types.Knowledge, error)
}

// ChunkProvider 只暴露按知识读取正文分块的能力。
type ChunkProvider interface {
	ListChunksByKnowledgeID(ctx context.Context, tenantID uint64, knowledgeID string) ([]*types.Chunk, error)
}

// ModelProvider 是聊天模型能力面，默认模型选择沿用 workbench 的口径。
type ModelProvider interface {
	GetChatModel(ctx context.Context, modelID string) (chat.Chat, error)
	ListModels(ctx context.Context) ([]*types.Model, error)
}

// Service 洞察服务。
type Service struct {
	repo      interfaces.InsightRepository
	workKB    WorkKBProvider
	knowledge KnowledgeProvider
	chunks    ChunkProvider
	models    ModelProvider
}

// NewService 构造洞察服务。
func NewService(
	repo interfaces.InsightRepository,
	workKB WorkKBProvider,
	knowledge KnowledgeProvider,
	chunks ChunkProvider,
	models ModelProvider,
) *Service {
	return &Service{repo: repo, workKB: workKB, knowledge: knowledge, chunks: chunks, models: models}
}

// callerScope 从 ctx 取调用者的租户与用户；缺一按未认证处理，不做公共空间兜底。
func callerScope(ctx context.Context) (tenantID uint64, userID string, err error) {
	caller := types.CallerFromContext(ctx)
	if caller.TenantID == 0 {
		return 0, "", apperrors.NewUnauthorizedError("workspace context is required")
	}
	if caller.UserID == "" {
		return 0, "", apperrors.NewUnauthorizedError("user context is required")
	}
	if types.IsSyntheticUserID(caller.UserID) {
		return 0, "", apperrors.NewUnauthorizedError("insight requires a real user")
	}
	return caller.TenantID, caller.UserID, nil
}

// notFound 是“资源不存在或不属于当前用户”的唯一出口。
func notFound(message string) error {
	if message == "" {
		message = "insight resource not found"
	}
	return apperrors.NewWorkbenchNotFoundError(message)
}

// ListThemes 返回主题列表与顶部汇总；首次调用时幂等种入内置主题。
func (s *Service) ListThemes(ctx context.Context) (*types.InsightThemeListResponse, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.seedBuiltinThemes(ctx, tenantID, ownerID); err != nil {
		return nil, err
	}
	themes, err := s.repo.ListThemes(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	// 文档列表只取一次：既用于顶部汇总，也用于「上次分析后有更新」提醒。
	// 拿不到时按空处理，列表不应因为工作库暂时不可用而整页失败。
	var docs []*types.Knowledge
	if list, docsErr := s.listWorkDocs(ctx, tenantID, ownerID); docsErr == nil {
		docs = list
	}
	views := make([]*types.InsightThemeView, 0, len(themes))
	for _, theme := range themes {
		view, viewErr := s.themeView(ctx, tenantID, ownerID, theme, docs)
		if viewErr != nil {
			return nil, viewErr
		}
		views = append(views, view)
	}
	reportCount, err := s.repo.CountReports(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	return &types.InsightThemeListResponse{
		Themes: views,
		Stats:  types.InsightThemeStats{ReportCount: reportCount, DocCount: len(docs)},
	}, nil
}

// CreateTheme 新建自定义主题。
func (s *Service) CreateTheme(
	ctx context.Context, req types.CreateInsightThemeRequest,
) (*types.InsightThemeView, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	dims, err := normalizeDimensions(req.Dimensions)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if err := validateThemeName(name); err != nil {
		return nil, err
	}
	now := time.Now()
	theme := &types.InsightTheme{
		ID:           uuid.New().String(),
		Name:         name,
		Description:  strings.TrimSpace(req.Description),
		Sensitive:    req.Sensitive,
		Glyph:        "i-spark",
		Instructions: strings.TrimSpace(req.Instructions),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := theme.SetDimensions(dims); err != nil {
		return nil, apperrors.NewValidationError("invalid dimensions")
	}
	if err := s.repo.CreateTheme(ctx, tenantID, ownerID, theme); err != nil {
		return nil, err
	}
	return s.themeView(ctx, tenantID, ownerID, theme, nil)
}

// UpdateTheme 覆盖更新主题；内置主题允许改口径，但不允许删除。
func (s *Service) UpdateTheme(
	ctx context.Context, id string, req types.CreateInsightThemeRequest,
) (*types.InsightThemeView, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	theme, err := s.repo.GetTheme(ctx, tenantID, ownerID, id)
	if err != nil {
		return nil, mapNotFound(err, "insight theme not found")
	}
	dims, err := normalizeDimensions(req.Dimensions)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if err := validateThemeName(name); err != nil {
		return nil, err
	}
	theme.Name = name
	theme.Description = strings.TrimSpace(req.Description)
	theme.Sensitive = req.Sensitive
	theme.Instructions = strings.TrimSpace(req.Instructions)
	if err := theme.SetDimensions(dims); err != nil {
		return nil, apperrors.NewValidationError("invalid dimensions")
	}
	if err := s.repo.UpdateTheme(ctx, tenantID, ownerID, theme); err != nil {
		return nil, err
	}
	return s.themeView(ctx, tenantID, ownerID, theme, nil)
}

// DeleteTheme 删除自定义主题；内置主题拒绝删除，报告不做级联。
func (s *Service) DeleteTheme(ctx context.Context, id string) error {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return err
	}
	theme, err := s.repo.GetTheme(ctx, tenantID, ownerID, id)
	if err != nil {
		return mapNotFound(err, "insight theme not found")
	}
	if theme.Builtin {
		return apperrors.NewValidationError("builtin insight theme cannot be deleted")
	}
	return s.repo.DeleteTheme(ctx, tenantID, ownerID, id)
}

// Checkup 对工作知识库做素材体检，只按元数据归类，不逐篇读取 chunk。
func (s *Service) Checkup(
	ctx context.Context, themeID string, filter types.InsightScopeFilter,
) (*types.InsightCheckup, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.GetTheme(ctx, tenantID, ownerID, themeID); err != nil {
		return nil, mapNotFound(err, "insight theme not found")
	}
	docs, err := s.listWorkDocs(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	scoped := filterDocs(docs, filter)
	tally := s.classifyDocsWithText(ctx, tenantID, scoped)
	checkup := tally.checkup
	checkup.Archived = len(scoped)
	checkup.Folders = collectFolders(docs)
	checkup.SampleSize = minInt(types.InsightSampleDocLimit, checkup.Analyzable)
	return &checkup, nil
}

// ListRuns 返回主题最近的运行记录。
func (s *Service) ListRuns(ctx context.Context, themeID string) ([]*types.InsightRun, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.GetTheme(ctx, tenantID, ownerID, themeID); err != nil {
		return nil, mapNotFound(err, "insight theme not found")
	}
	return s.repo.ListRunsByTheme(ctx, tenantID, ownerID, themeID, 20)
}

// themeView 给主题补上报告计数、上次运行时间、运行中标记与「有更新」提醒数。
func (s *Service) themeView(
	ctx context.Context, tenantID uint64, ownerID string, theme *types.InsightTheme,
	docs []*types.Knowledge,
) (*types.InsightThemeView, error) {
	view := &types.InsightThemeView{InsightTheme: *theme}
	count, err := s.repo.CountReportsByTheme(ctx, tenantID, ownerID, theme.ID)
	if err != nil {
		return nil, err
	}
	view.ReportCount = count
	if count > 0 && len(docs) > 0 {
		// 以最新报告为基线：之后更新过的文档可能没进入那份快照。
		if report, reportErr := s.repo.LatestReportByTheme(ctx, tenantID, ownerID, theme.ID); reportErr == nil {
			view.UpdatedDocs = countDocsUpdatedAfter(docs, report.CreatedAt)
		}
	}
	run, runErr := s.repo.LatestRunByTheme(ctx, tenantID, ownerID, theme.ID)
	if runErr == nil && run != nil {
		started := run.CreatedAt
		view.LastRunAt = &started
		view.Running = isRunningStatus(run.Status)
	} else if runErr != nil && !errors.Is(runErr, gorm.ErrRecordNotFound) {
		return nil, runErr
	}
	return view, nil
}

// countDocsUpdatedAfter 统计在 since 之后有更新的文档数。
func countDocsUpdatedAfter(docs []*types.Knowledge, since time.Time) int {
	n := 0
	for _, doc := range docs {
		if doc != nil && doc.UpdatedAt.After(since) {
			n++
		}
	}
	return n
}

// listWorkDocs 取当前用户私有工作知识库中的全部文档。
func (s *Service) listWorkDocs(
	ctx context.Context, tenantID uint64, ownerID string,
) ([]*types.Knowledge, error) {
	if s.workKB == nil || s.knowledge == nil {
		return nil, apperrors.NewServiceUnavailableError("insight knowledge source is not configured")
	}
	kb, err := s.workKB.EnsureWorkKnowledgeBase(ctx, tenantID, ownerID)
	if err != nil || kb == nil {
		return nil, apperrors.NewInternalServerError("work workbench knowledge base is unavailable")
	}
	return s.knowledge.ListKnowledgeByKnowledgeBaseID(ctx, tenantID, kb.ID)
}

// docTally 是素材归类结果；每篇文档只进入一个桶。
type docTally struct {
	checkup    types.InsightCheckup
	analyzable []*types.Knowledge
}

// classifyDocsWithText 归类顺序：空 → 附件 → 重复 → 过短 → 可分析。
//
// 工作台归档的手动知识 FileSize 恒为 0，不能据此判空；这类文档改为读取
// 正文分块的实际字数。读取失败时保守地归入可分析，交给阶段一逐篇跳过。
func (s *Service) classifyDocsWithText(
	ctx context.Context, tenantID uint64, docs []*types.Knowledge,
) docTally {
	tally := docTally{analyzable: make([]*types.Knowledge, 0, len(docs))}
	seenHashes := make(map[string]struct{}, len(docs))
	for _, doc := range docs {
		if doc == nil {
			continue
		}
		trimmedTitle := strings.TrimSpace(doc.Title)
		trimmedDesc := strings.TrimSpace(doc.Description)
		if trimmedTitle == "" && trimmedDesc == "" && doc.FileSize == 0 {
			tally.checkup.Empty++
			continue
		}
		if isAttachment(doc) {
			tally.checkup.Attachment++
			continue
		}
		if doc.FileHash != "" {
			if _, ok := seenHashes[doc.FileHash]; ok {
				tally.checkup.Duplicate++
				continue
			}
		}
		textLen := 0
		if doc.FileSize == 0 {
			n, err := s.docTextLen(ctx, tenantID, doc.ID)
			if err != nil {
				// 读正文失败不当作空文档，否则一次数据库抖动就会把整个库判成不可分析。
				tally.checkup.Analyzable++
				tally.analyzable = append(tally.analyzable, doc)
				continue
			}
			textLen = n
		}
		switch {
		case doc.FileSize == 0 && textLen == 0:
			tally.checkup.Empty++
		case doc.FileSize > 0 && doc.FileSize < 1024 && trimmedDesc == "":
			tally.checkup.TooShort++
		case doc.FileSize == 0 && textLen < 120:
			tally.checkup.TooShort++
		default:
			tally.checkup.Analyzable++
			tally.analyzable = append(tally.analyzable, doc)
		}
		if doc.FileHash != "" {
			seenHashes[doc.FileHash] = struct{}{}
		}
	}
	return tally
}

// docTextLen 统计一篇文档正文分块的总字数（不含空分块）。
func (s *Service) docTextLen(ctx context.Context, tenantID uint64, knowledgeID string) (int, error) {
	if s.chunks == nil {
		return 0, nil
	}
	chunks, err := s.chunks.ListChunksByKnowledgeID(ctx, tenantID, knowledgeID)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		total += utf8.RuneCountInString(strings.TrimSpace(chunk.Content))
	}
	return total, nil
}

// filterDocs 按文件夹与最近天数筛选文档；零值表示不限制。
func filterDocs(docs []*types.Knowledge, filter types.InsightScopeFilter) []*types.Knowledge {
	folder := normalizeFolderPath(filter.FolderPath)
	var cutoff time.Time
	if filter.RecentDays > 0 {
		cutoff = time.Now().AddDate(0, 0, -filter.RecentDays)
	}
	out := make([]*types.Knowledge, 0, len(docs))
	for _, doc := range docs {
		if doc == nil {
			continue
		}
		if folder != "" && !inFolder(doc.FolderPath, folder) {
			continue
		}
		if !cutoff.IsZero() && doc.UpdatedAt.Before(cutoff) {
			continue
		}
		out = append(out, doc)
	}
	return out
}

// normalizeFolderPath 去掉首尾空白与斜杠，避免同一路径出现两种写法。
func normalizeFolderPath(path string) string {
	return strings.Trim(strings.TrimSpace(path), "/")
}

// inFolder 判断文档是否位于目标文件夹或其子文件夹内。
func inFolder(docPath, folder string) bool {
	normalized := normalizeFolderPath(docPath)
	return normalized == folder || strings.HasPrefix(normalized, folder+"/")
}

// collectFolders 汇总全部归档文档出现过的文件夹路径，供范围选择器使用。
func collectFolders(docs []*types.Knowledge) []string {
	seen := make(map[string]struct{}, len(docs))
	folders := make([]string, 0, len(docs))
	for _, doc := range docs {
		if doc == nil {
			continue
		}
		path := normalizeFolderPath(doc.FolderPath)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		folders = append(folders, path)
	}
	sort.Strings(folders)
	return folders
}

// isAttachment 按 FileType 与文件后缀双重判断图片/音视频。
func isAttachment(doc *types.Knowledge) bool {
	fileType := strings.ToLower(strings.TrimSpace(doc.FileType))
	switch fileType {
	case "image", "audio", "video":
		return true
	}
	name := strings.ToLower(strings.TrimSpace(doc.FileName))
	if name == "" {
		return false
	}
	ext := ""
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		ext = name[idx+1:]
	}
	switch ext {
	case "png", "jpg", "jpeg", "gif", "webp", "bmp", "svg", "heic", "heif", "tif", "tiff",
		"mp3", "wav", "m4a", "aac", "flac", "ogg", "amr",
		"mp4", "mov", "avi", "mkv", "webm", "flv", "wmv":
		return true
	default:
		return false
	}
}

// selectAnalyzedDocs 按小样/全量口径挑文档：小样按 UpdatedAt 倒序取 20 篇。
func selectAnalyzedDocs(analyzable []*types.Knowledge, req types.StartInsightRunRequest) []*types.Knowledge {
	ordered := make([]*types.Knowledge, len(analyzable))
	copy(ordered, analyzable)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].UpdatedAt.After(ordered[j].UpdatedAt)
	})
	limit := req.MaxDocs
	if req.Sample {
		limit = types.InsightSampleDocLimit
	} else {
		// 未显式给上限时按全量上限处理，避免「全量」请求被误当成 20 篇小样。
		if limit <= 0 {
			limit = types.InsightMaxDocLimit
		}
		if limit > types.InsightMaxDocLimit {
			limit = types.InsightMaxDocLimit
		}
	}
	if limit > len(ordered) {
		limit = len(ordered)
	}
	return ordered[:limit]
}

// normalizeDimensions 校验并补默认 scale；维度数量限制在 1–8 之间。
func normalizeDimensions(dims []types.InsightDimension) ([]types.InsightDimension, error) {
	if len(dims) == 0 || len(dims) > 8 {
		return nil, apperrors.NewValidationError("dimensions must contain between 1 and 8 items")
	}
	out := make([]types.InsightDimension, 0, len(dims))
	for _, dim := range dims {
		name := strings.TrimSpace(dim.Name)
		if name == "" || utf8.RuneCountInString(name) > types.InsightDimensionMaxLen {
			return nil, apperrors.NewValidationError("invalid dimension name")
		}
		scale := strings.TrimSpace(dim.Scale)
		if scale == "" {
			scale = "0-100"
		}
		out = append(out, types.InsightDimension{
			Name:     name,
			Criteria: strings.TrimSpace(dim.Criteria),
			Scale:    scale,
		})
	}
	return out, nil
}

// validateThemeName 校验主题名必填且不超过 80 个字符。
func validateThemeName(name string) error {
	if name == "" {
		return apperrors.NewValidationError("insight theme name is required")
	}
	if utf8.RuneCountInString(name) > 80 {
		return apperrors.NewValidationError("insight theme name is too long")
	}
	return nil
}

// mapNotFound 把 gorm 的未找到统一翻译成 2400/404。
func mapNotFound(err error, message string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return notFound(message)
	}
	return err
}

// isRunningStatus 判断运行是否仍在流程中（含等待用户确认证据）。
func isRunningStatus(status string) bool {
	switch status {
	case types.InsightStatusPending, types.InsightStatusStage1,
		types.InsightStatusWaitingConfirm, types.InsightStatusStage2:
		return true
	default:
		return false
	}
}

// isTerminalStatus 判断运行是否已结束。
func isTerminalStatus(status string) bool {
	switch status {
	case types.InsightStatusDone, types.InsightStatusFailed, types.InsightStatusCancelled:
		return true
	default:
		return false
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
