// book.go 是项目黑板（共享上下文时间线）的业务层：人工记一条、
// 运行终态自动摘要、编辑/置顶/删除，以及写入后触发简报重渲染。
//
// 两条口径（spec §3.10）：
//   - 黑板是唯一事实来源：AI 只产草稿（不落库），人确认后才经 Create 写入；
//   - 任何写入都推进 seq 并触发简报重渲染——简报只是黑板的投影。
package sop

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ErrSopNoteProjectNotFound 黑板写入的目标项目不存在（或不属于该租户）。
var ErrSopNoteProjectNotFound = errors.New("事项所属项目不存在")

// NoteItemReader 是摘要所需的「事项标题」读取面（窄接口，避免整仓储耦合）。
type NoteItemReader interface {
	Get(ctx context.Context, tenantID uint64, id string) (*types.SopItem, error)
}

// NoteDeps 黑板服务的依赖（Projects/Items/Runs 均可 nil：只做条目读写时不需要）。
type NoteDeps struct {
	Projects interfaces.SopProjectRepository
	Notes    interfaces.SopProjectNoteRepository
	Items    NoteItemReader
	// Runs 可选：RecordRunFinal 用它自取节点运行与产物（引擎回调只给 run）。
	Runs interfaces.SopRunRepository
	// OnRecord 每次成功写入（新增/编辑/删除/置顶）后调用，用于触发简报重渲染。
	// 失败只记日志：简报迟到不是写入失败的理由。
	OnRecord func(tenantID uint64, projectID string)
}

// NotesService 项目黑板业务。
type NotesService struct {
	d NoteDeps
}

// NewNotesService 构造黑板服务。
func NewNotesService(d NoteDeps) *NotesService { return &NotesService{d: d} }

// Create 人工记一条：来源固定 human、kind 缺省 note。
func (s *NotesService) Create(
	ctx context.Context, tenantID uint64, userID string, n *types.SopProjectNote,
) error {
	if n == nil {
		return fmt.Errorf("%w: note is required", types.ErrInvalidSopNote)
	}
	if err := s.ensureProject(ctx, tenantID, n.ProjectID); err != nil {
		return err
	}
	n.TenantID = tenantID
	n.Kind = strings.TrimSpace(n.Kind)
	if n.Kind == "" {
		n.Kind = types.SopNoteKindNote
	}
	if n.Source == "" {
		n.Source = types.SopNoteSourceHuman
	}
	n.CreatedBy = userID
	if err := s.d.Notes.Create(ctx, n); err != nil {
		return err
	}
	s.notify(tenantID, n.ProjectID)
	return nil
}

// Edit 改标题/正文/类型与置顶（来源与归属列不随编辑变化）。
func (s *NotesService) Edit(
	ctx context.Context, tenantID uint64, userID string, n *types.SopProjectNote,
) error {
	if n == nil {
		return fmt.Errorf("%w: note is required", types.ErrInvalidSopNote)
	}
	existing, err := s.d.Notes.GetByID(ctx, tenantID, n.ID)
	if err != nil {
		return err
	}
	existing.Kind = n.Kind
	existing.Title = n.Title
	existing.Body = n.Body
	existing.Pinned = n.Pinned
	if err := s.d.Notes.Update(ctx, tenantID, existing); err != nil {
		return err
	}
	*n = *existing
	s.notify(tenantID, existing.ProjectID)
	return nil
}

// Pin 置顶/取消置顶（走 Update，同样推进 seq）。
func (s *NotesService) Pin(ctx context.Context, tenantID uint64, id string, pinned bool) error {
	existing, err := s.d.Notes.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	existing.Pinned = pinned
	if err := s.d.Notes.Update(ctx, tenantID, existing); err != nil {
		return err
	}
	s.notify(tenantID, existing.ProjectID)
	return nil
}

// SoftDelete 删除条目（软删，seq 让位给后续条目）。
func (s *NotesService) SoftDelete(ctx context.Context, tenantID uint64, id string) error {
	existing, err := s.d.Notes.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if err := s.d.Notes.SoftDelete(ctx, tenantID, id); err != nil {
		return err
	}
	s.notify(tenantID, existing.ProjectID)
	return nil
}

// Get 取单条（HTTP 层编辑回显用）。
func (s *NotesService) Get(ctx context.Context, tenantID uint64, id string) (*types.SopProjectNote, error) {
	return s.d.Notes.GetByID(ctx, tenantID, id)
}

// List 列项目黑板（kind 为空不过滤）。
func (s *NotesService) List(
	ctx context.Context, tenantID uint64, projectID, kind string,
) ([]types.SopProjectNote, error) {
	return s.d.Notes.List(ctx, tenantID, projectID, kind, nil, 0)
}

// ListFiltered 列项目黑板（kind/pinned/limit 均可选；limit<=0 用仓储默认上限）。
func (s *NotesService) ListFiltered(
	ctx context.Context, tenantID uint64, projectID, kind string, pinned *bool, limit int,
) ([]types.SopProjectNote, error) {
	if err := s.ensureProject(ctx, tenantID, projectID); err != nil {
		return nil, err
	}
	return s.d.Notes.List(ctx, tenantID, projectID, kind, pinned, limit)
}

// DecisionDraft 让 AI 基于最近黑板与运行摘要起草一条决策（不落库）。
//
// 只返回文本：落库必须由人确认后走 Create（AI 不能直接改项目事实）。
func (s *NotesService) DecisionDraft(
	ctx context.Context, tenantID uint64, projectID string, ai *AIService,
) (string, error) {
	if ai == nil {
		return "", fmt.Errorf("AI 助手暂未启用，请直接填写决策内容")
	}
	notes, err := s.d.Notes.List(ctx, tenantID, projectID, "", nil, 10)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, n := range notes {
		fmt.Fprintf(&b, "- [%s] %s：%s\n", types.SopNoteKindLabel(n.Kind), n.Title, firstLineText(n.Body))
	}
	draft, _, err := ai.runner.Ask(ctx, AIRequest{
		TenantID: tenantID,
		System: "你是项目决策助手。基于最近的项目动态，起草一条决策记录。" +
			"输出三行：第一行「决策：<标题>」，第二行「理由：<一句话>」，第三行「影响：<范围>」。" +
			"不要输出 Markdown 代码块，不要编造未出现过的事实。",
		Prompt: "最近动态：\n" + b.String(),
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(draft), nil
}

// AutoRecordRun 运行终态自动摘要：确定性文本（同入参恒等），来源恒 engine。
//
// 只记录事实（状态、节点、产物、用量、审批原话），不带主观评价——摘要不是第二个真相。
func (s *NotesService) AutoRecordRun(
	ctx context.Context, run *types.SopRun, itemTitle string,
	nodeRuns []types.SopNodeRun, arts []types.SopArtifact,
) error {
	if run == nil {
		return fmt.Errorf("%w: run is required", types.ErrInvalidSopNote)
	}
	if err := s.ensureProject(ctx, run.TenantID, run.ProjectID); err != nil {
		return err
	}
	title := strings.TrimSpace(itemTitle)
	if title == "" {
		title = "运行 " + run.ID
	}
	n := &types.SopProjectNote{
		TenantID:  run.TenantID,
		ProjectID: run.ProjectID,
		Kind:      types.SopNoteKindRunSummary,
		Title:     "运行结论：" + title,
		Body:      buildRunSummary(run, nodeRuns, arts),
		ItemID:    run.ItemID,
		RunID:     run.ID,
		Source:    types.SopNoteSourceEngine,
	}
	if err := s.d.Notes.Create(ctx, n); err != nil {
		return err
	}
	s.notify(run.TenantID, run.ProjectID)
	return nil
}

// RecordRunFinal 是引擎 OnRunFinal 的回调适配：自行读取节点运行、产物与事项标题后
// 走 AutoRecordRun。任何读取失败只记日志并返回错误，由调用方（引擎）吞掉。
func (s *NotesService) RecordRunFinal(ctx context.Context, run *types.SopRun) error {
	if run == nil {
		return fmt.Errorf("%w: run is required", types.ErrInvalidSopNote)
	}
	var nodeRuns []types.SopNodeRun
	var arts []types.SopArtifact
	if s.d.Runs != nil {
		if list, err := s.d.Runs.ListNodeRuns(ctx, run.TenantID, run.ID); err == nil {
			nodeRuns = list
		} else {
			log.Printf("[sop-notes] 运行 %s 节点读取失败：%v", run.ID, err)
		}
		if list, err := s.d.Runs.ListArtifacts(ctx, run.TenantID, run.ID); err == nil {
			arts = list
		} else {
			log.Printf("[sop-notes] 运行 %s 产物读取失败：%v", run.ID, err)
		}
	}
	title := ""
	if run.ItemID != "" && s.d.Items != nil {
		if it, err := s.d.Items.Get(ctx, run.TenantID, run.ItemID); err == nil {
			title = it.Title
		}
	}
	return s.AutoRecordRun(ctx, run, title, nodeRuns, arts)
}

// runSummaryStatusLabels 是运行状态的中文文案（摘要用，与前端文案口径一致）。
var runSummaryStatusLabels = map[string]string{
	types.SopRunSucceeded: "成功",
	types.SopRunFailed:    "失败",
	types.SopRunCancelled: "已取消",
	types.SopRunWaiting:   "等待中",
	types.SopRunRunning:   "运行中",
	types.SopRunPaused:    "已暂停",
	types.SopRunQueued:    "排队中",
}

// nodeSummaryStatusLabels 是节点状态的中文文案。
var nodeSummaryStatusLabels = map[string]string{
	types.SopNodeSucceeded:       "成功",
	types.SopNodeFailed:          "失败",
	types.SopNodeSkipped:         "跳过",
	types.SopNodeCancelled:       "已取消",
	types.SopNodeWaitingApproval: "等待审批",
	types.SopNodePendingInput:    "等待补充",
	types.SopNodeRunning:         "运行中",
	types.SopNodePending:         "未开始",
}

// buildRunSummary 组装确定性摘要正文（不依赖时间、不依赖 map 遍历顺序）。
func buildRunSummary(run *types.SopRun, nodeRuns []types.SopNodeRun, arts []types.SopArtifact) string {
	var b strings.Builder
	status := runSummaryStatusLabels[run.Status]
	if status == "" {
		status = run.Status
	}
	fmt.Fprintf(&b, "状态：%s\n", status)
	if run.Error != "" {
		fmt.Fprintf(&b, "原因：%s\n", firstLineText(run.Error))
	}

	if len(nodeRuns) > 0 {
		b.WriteString("\n节点：\n")
		for _, nr := range nodeRuns {
			label := nodeSummaryStatusLabels[nr.Status]
			if label == "" {
				label = nr.Status
			}
			fmt.Fprintf(&b, "- %s：%s\n", nr.NodeID, label)
		}
	}

	if len(arts) > 0 {
		b.WriteString("\n产物：\n")
		for _, art := range arts {
			sha := art.SHA256
			if len(sha) > 8 {
				sha = sha[:8]
			}
			if sha != "" {
				fmt.Fprintf(&b, "- %s（%s，sha256 %s）\n", art.Name, art.Path, sha)
			} else {
				fmt.Fprintf(&b, "- %s（%s）\n", art.Name, art.Path)
			}
		}
	}

	if run.Usage.InputTokens > 0 || run.Usage.OutputTokens > 0 {
		fmt.Fprintf(&b, "\nToken：输入 %d / 输出 %d\n", run.Usage.InputTokens, run.Usage.OutputTokens)
	}

	var approvals []string
	for _, h := range run.HumanContext {
		if h.Source == "approval" && strings.TrimSpace(h.Text) != "" {
			approvals = append(approvals, strings.TrimSpace(h.Text))
		}
	}
	if len(approvals) > 0 {
		b.WriteString("\n审批意见：\n")
		for _, a := range approvals {
			fmt.Fprintf(&b, "- %s\n", a)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ensureProject 校验条目归属的项目存在且属于该租户（未注入 Projects 时跳过校验）。
func (s *NotesService) ensureProject(ctx context.Context, tenantID uint64, projectID string) error {
	if projectID == "" {
		return fmt.Errorf("%w: project_id is required", types.ErrInvalidSopNote)
	}
	if s.d.Projects == nil {
		return nil
	}
	if _, err := s.d.Projects.GetByID(ctx, tenantID, projectID); err != nil {
		return ErrSopNoteProjectNotFound
	}
	return nil
}

func (s *NotesService) notify(tenantID uint64, projectID string) {
	if s.d.OnRecord == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[sop-notes] 触发简报重渲染 panic：%v", r)
		}
	}()
	s.d.OnRecord(tenantID, projectID)
}

// firstLineText 取首行（错误信息可能多行，摘要只留第一行）。
func firstLineText(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}
