// brief.go 是项目简报服务：把黑板 + 事项 + 项目元信息渲染成 Markdown，
// 并按「黑板 seq 水位」增量同步到项目绑定的知识库。
//
// 三条口径（spec §3.10 / 全局约束 19）：
//   - 简报是黑板的投影，不是第二真相：只有本服务写这份文档（channel=sop，状态恒 publish）；
//   - seq 未变不写库：渲染水位 brief_rendered_seq 与黑板 MaxSeq 相等即跳过，
//     避免每次打开面板都触发一次知识库重写；
//   - 未绑知识库是降级不是错误：渲染照常可用（面板可预览），同步变 no-op。
package sop

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// BriefKnowledge 简报同步依赖的知识库能力（窄接口，测试给 stub 即可）。
type BriefKnowledge interface {
	CreateKnowledgeFromManual(
		ctx context.Context, kbID string, payload *types.ManualKnowledgePayload, channel string,
	) (*types.Knowledge, error)
	UpdateManualKnowledge(
		ctx context.Context, knowledgeID string,
		payload *types.ManualKnowledgePayload,
	) (*types.Knowledge, error)
}

// BriefItemLister 是简报所需的事项读取面：必须带派生字段（当前环节/进度/执行情况）。
//
// 计划原文写的是 interfaces.SopItemRepository，但仓储的 ListWithDerived 需要调用方
// 先聚合每个事项的最近一次运行；直接拿仓储会把所有事项的执行情况算成 none。
// 这里改为窄接口，由 ItemService（已实现同签名的聚合）满足。
type BriefItemLister interface {
	ListWithDerived(
		ctx context.Context, tenantID uint64, projectID string, f types.SopItemFilter,
	) ([]types.SopItemWithDerived, error)
}

// BriefDeps 简报服务依赖。
type BriefDeps struct {
	Projects  interfaces.SopProjectRepository
	Notes     interfaces.SopProjectNoteRepository
	Items     BriefItemLister
	Knowledge BriefKnowledge
	// Dispatcher 可选：条目写入后把重渲染排出去（生产由外汇总到 Trigger）。
	Dispatcher func(projectID string)
}

// BriefService 项目简报业务。
type BriefService struct {
	d BriefDeps

	// debounce 是 Trigger 的合并窗口（生产 2s；测试可缩短）。
	debounce time.Duration

	mu      sync.Mutex
	pending map[string]bool
}

// briefDebounceDefault 是简报重渲染的合并窗口。
const briefDebounceDefault = 2 * time.Second

// briefDocBanner 是简报文档正文首行（防止有人在知识库里手改后与黑板分叉）。
const briefDocBanner = "<!-- 自动生成，请勿手改；要补充请在项目动态里记一条 -->\n\n"

// NewBriefService 构造简报服务。
func NewBriefService(d BriefDeps) *BriefService {
	return &BriefService{d: d, debounce: briefDebounceDefault, pending: map[string]bool{}}
}

// Render 渲染项目简报正文（不写库、不改水位）。
//
// 事项来自 ItemService 的派生列表（执行情况按最近一次运行现算），大项按子项
// 汇总执行情况与完成计数；黑板取最近 200 条（置顶优先、seq 倒序）。
func (s *BriefService) Render(ctx context.Context, tenantID uint64, projectID string) (string, error) {
	proj, items, notes, err := s.load(ctx, tenantID, projectID)
	if err != nil {
		return "", err
	}
	return types.RenderProjectBrief(briefInputOf(proj, items, notes)), nil
}

// ProjectBrief 是 engine.BriefProvider 的实现：只读渲染（不写库、不动水位）。
//
// 引擎在每个节点执行前调用它，把正文物化到工作区 inputs/project-brief.md。
func (s *BriefService) ProjectBrief(ctx context.Context, tenantID uint64, projectID string) (string, error) {
	return s.Render(ctx, tenantID, projectID)
}

// load 拉项目、派生事项与黑板（Render 与 EnsureInitial 共用）。
func (s *BriefService) load(
	ctx context.Context, tenantID uint64, projectID string,
) (*types.SopProject, []types.SopItemWithDerived, []types.SopProjectNote, error) {
	if projectID == "" {
		return nil, nil, nil, fmt.Errorf("项目不存在")
	}
	proj, err := s.d.Projects.GetByID(ctx, tenantID, projectID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("项目不存在：%w", err)
	}
	var items []types.SopItemWithDerived
	if s.d.Items != nil {
		items, err = s.d.Items.ListWithDerived(ctx, tenantID, projectID, types.SopItemFilter{})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("读取事项失败：%w", err)
		}
	}
	notes, err := s.d.Notes.List(ctx, tenantID, projectID, "", nil, 200)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("读取项目动态失败：%w", err)
	}
	return proj, items, notes, nil
}

// briefInputOf 把仓储行组装成渲染入参：大项补子项计数与汇总执行情况。
func briefInputOf(
	proj *types.SopProject, items []types.SopItemWithDerived, notes []types.SopProjectNote,
) types.BriefInput {
	childrenOf := map[string][]types.SopItemWithDerived{}
	for _, it := range items {
		if it.ParentID != "" {
			childrenOf[it.ParentID] = append(childrenOf[it.ParentID], it)
		}
	}
	rows := make([]types.BriefItem, 0, len(items))
	for _, it := range items {
		if it.ParentID != "" {
			continue // 子项汇总进父项行，不单独占行
		}
		row := types.BriefItem{
			No: it.No, Title: it.Title, Status: it.Status, ExecState: it.ExecState,
		}
		if kids := childrenOf[it.ID]; len(kids) > 0 {
			states := make([]string, 0, len(kids))
			for _, kid := range kids {
				states = append(states, kid.ExecState)
				if kid.Status == types.SopItemStatusDone || kid.Status == types.SopItemStatusClosed {
					row.ChildDone++
				}
			}
			row.ChildTotal = len(kids)
			row.ExecState = types.DeriveParentExecState(states)
		}
		rows = append(rows, row)
	}
	return types.BriefInput{Project: *proj, Items: rows, Notes: notes}
}

// EnsureInitial 同步简报：seq 未变零写入，未绑库降级为 no-op。
//
// 首次写用 CreateKnowledgeFromManual 并把 knowledge id 回填 brief_knowledge_id；
// 之后一律覆盖同一文档（UpdateManualKnowledge），绝不新建第二份。
// 写入失败不前移水位——下次触发会重试。
func (s *BriefService) EnsureInitial(ctx context.Context, tenantID uint64, projectID string) error {
	proj, items, notes, err := s.load(ctx, tenantID, projectID)
	if err != nil {
		return err
	}
	maxSeq, err := s.d.Notes.MaxSeq(ctx, tenantID, projectID)
	if err != nil {
		return fmt.Errorf("读取项目动态序号失败：%w", err)
	}
	if maxSeq == proj.BriefRenderedSeq && strings.TrimSpace(proj.BriefKnowledgeID) != "" &&
		strings.TrimSpace(proj.KBID) != "" {
		return nil // 没有新动态：不写库
	}
	if strings.TrimSpace(proj.KBID) == "" {
		return nil // 未绑知识库：渲染可用、同步降级
	}
	if maxSeq == proj.BriefRenderedSeq && strings.TrimSpace(proj.BriefKnowledgeID) == "" {
		// 首次绑定但没有动态：也要写一份（检索侧要能看到「这个项目存在」）。
		maxSeq = 0
	}
	if s.d.Knowledge == nil {
		return fmt.Errorf("知识库服务未就绪")
	}

	doc := briefDocBanner + types.RenderProjectBrief(briefInputOf(proj, items, notes))
	payload := &types.ManualKnowledgePayload{
		Title:   proj.Name + " 项目简报",
		Content: doc,
		// 恒为 publish：简报必须能被检索命中（全局约束 19）。
		Status: "publish",
	}

	var knowledgeID string
	if strings.TrimSpace(proj.BriefKnowledgeID) == "" {
		k, err := s.d.Knowledge.CreateKnowledgeFromManual(ctx, proj.KBID, payload, "sop")
		if err != nil {
			return fmt.Errorf("写入项目简报失败：%w", err)
		}
		if k != nil {
			knowledgeID = k.ID
		}
	} else {
		knowledgeID = proj.BriefKnowledgeID
		if _, err := s.d.Knowledge.UpdateManualKnowledge(ctx, knowledgeID, payload); err != nil {
			return fmt.Errorf("更新项目简报失败：%w", err)
		}
	}

	proj.BriefKnowledgeID = knowledgeID
	proj.BriefRenderedSeq = maxSeq
	if err := s.d.Projects.Update(ctx, proj); err != nil {
		return fmt.Errorf("回填简报水位失败：%w", err)
	}
	return nil
}

// Trigger 把项目简报重渲染排进合并窗口（同项目窗口内只跑一次）。
//
// tenantID 必须随项目一起传：仓储的每次读写都按租户收口，只有 projectID
// 的签名在服务层无法完成读操作。
func (s *BriefService) Trigger(tenantID uint64, projectID string) {
	if strings.TrimSpace(projectID) == "" {
		return
	}
	s.mu.Lock()
	if s.pending[projectID] {
		s.mu.Unlock()
		return
	}
	s.pending[projectID] = true
	window := s.debounce
	s.mu.Unlock()

	time.AfterFunc(window, func() {
		s.mu.Lock()
		delete(s.pending, projectID)
		s.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := s.EnsureInitial(ctx, tenantID, projectID); err != nil {
			log.Printf("[sop-brief] 项目 %s 简报重新渲染失败：%v", projectID, err)
			return
		}
		if s.d.Dispatcher != nil {
			s.d.Dispatcher(projectID)
		}
	})
}

// BriefStatus 是简报面板的状态摘要（GET /projects/:id/brief 的响应体）。
type BriefStatus struct {
	Markdown         string `json:"markdown"`
	RenderedSeq      int64  `json:"rendered_seq"`
	NotesMaxSeq      int64  `json:"notes_max_seq"`
	KBBound          bool   `json:"kb_bound"`
	BriefKnowledgeID string `json:"brief_knowledge_id"`
	LastRenderedAt   string `json:"last_rendered_at"`
}

// Status 组装简报面板状态：正文 + 水位对比（前端据此显示「有 N 条新动态未同步」）。
func (s *BriefService) Status(ctx context.Context, tenantID uint64, projectID string) (*BriefStatus, error) {
	proj, items, notes, err := s.load(ctx, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	maxSeq, err := s.d.Notes.MaxSeq(ctx, tenantID, projectID)
	if err != nil {
		return nil, fmt.Errorf("读取项目动态序号失败：%w", err)
	}
	last := ""
	if !proj.UpdatedAt.IsZero() {
		last = proj.UpdatedAt.Format("2006-01-02 15:04")
	}
	return &BriefStatus{
		Markdown:         types.RenderProjectBrief(briefInputOf(proj, items, notes)),
		RenderedSeq:      proj.BriefRenderedSeq,
		NotesMaxSeq:      maxSeq,
		KBBound:          strings.TrimSpace(proj.KBID) != "",
		BriefKnowledgeID: proj.BriefKnowledgeID,
		LastRenderedAt:   last,
	}, nil
}
