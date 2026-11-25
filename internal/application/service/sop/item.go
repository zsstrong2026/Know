// item.go 是事项（工作项）的业务层：编号分配、派生执行态、状态机、批量迁移，
// 以及附件/澄清/活动的读写。
//
// 三条不可越界的口径：
//  1. 事项状态的唯一写入口是仓储的 UpdateStatus（内部过 CanTransitSopItem）；
//     本层不直接写 status 列，只负责把状态机错误翻译成白话。引擎通过
//     ItemStatusUpdater 接口调用同一入口（于是 8 态状态机只在一处裁决）。
//  2. 编号 <code>-<n> 在事务内由项目游标分配（AllocItemNo），并发创建不会撞号。
//  3. 派生字段（执行情况/进度/当前环节）不落库：每次列表按「每个事项最近一次运行」
//     现算，避免出现「运行推进了但事项列表没变」的两份事实。
package sop

import (
	"context"
	"errors"
	"fmt"
	"log"
	"mime"
	"path"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// 事项层业务错误（handler 据此映射 400/404，前端直接展示文案）。
var (
	ErrSopItemTitleRequired  = errors.New("事项标题不能为空")
	ErrSopItemTooMany        = errors.New("一次最多处理 200 个事项")
	ErrSopAttachmentEmpty    = errors.New("附件内容为空")
	ErrSopAttachmentTooLarge = errors.New("附件超过 10MB 上限")
	ErrSopAttachmentType     = errors.New("不支持该附件类型")
	ErrSopClarificationOpen  = errors.New("该澄清已经答复，不能重复答复")
	// ErrSopIllegalTransition 状态机不允许的迁移（handler 映射 400）。
	ErrSopIllegalTransition = errors.New("事项状态迁移不合法")
)

// 事项附件口径（与 PM 需求附件一致：白名单 + 大小上限 + 文本抽取）。
const (
	sopAttachmentMaxSize    = 10 << 20
	sopAttachmentTextMaxLen = 20000
	sopBulkLimit            = 200
)

// sopSourceInternal 是「平台新建」来源键（与落库默认值一致）。
const sopSourceInternal = "internal"

// sopAttachmentExts 允许上传的附件扩展名（图片 + 常见文档）。
var sopAttachmentExts = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {},
	".pdf": {},
	".doc": {}, ".docx": {},
	".xls": {}, ".xlsx": {},
	".ppt": {}, ".pptx": {},
	".txt": {}, ".md": {}, ".csv": {}, ".json": {},
}

// sopAttachmentTextExts 可抽取纯文本作为任务书上下文的扩展名。
var sopAttachmentTextExts = map[string]struct{}{
	".txt": {}, ".md": {}, ".csv": {}, ".json": {},
}

// CreateItemInput 新建事项的请求（来源缺省 internal；状态固定 backlog）。
type CreateItemInput struct {
	Title       string
	Description string
	Kind        string
	Priority    string
	Source      string
	Proposer    string
	Owner       string
	Tags        []string
	TemplateID  string
	KBID        string
	IsBig       bool
	ParentID    string
}

// UpdateItemInput 修改事项可编辑字段（状态不在此：唯一入口是 UpdateStatus）。
type UpdateItemInput struct {
	Title       *string
	Description *string
	Kind        *string
	Priority    *string
	Source      *string
	Proposer    *string
	Owner       *string
	Tags        *[]string
	TemplateID  *string
	KBID        *string
	Outcome     *map[string]any
	DomainData  *map[string]any
}

// BulkResult 批量迁移结果：成功与被跳过（状态机不允许 / 正在执行 / 不存在）分开返回。
type BulkResult struct {
	Updated []string `json:"updated"`
	Skipped []string `json:"skipped"`
}

// ItemService 事项业务（实现 engine.ItemStatusUpdater）。
type ItemService struct {
	projects interfaces.SopProjectRepository
	items    interfaces.SopItemRepository
	extras   interfaces.SopItemExtraRepository
	runs     interfaces.SopRunRepository
}

// 编译期断言：引擎只依赖 UpdateStatus 这一个入口。
var _ interface {
	UpdateStatus(ctx context.Context, tenantID uint64, id, to string) error
} = (*ItemService)(nil)

// NewItemService 构造事项服务；runs 可 nil（列表派生退化为「无运行」）。
func NewItemService(
	projects interfaces.SopProjectRepository, items interfaces.SopItemRepository,
	extras interfaces.SopItemExtraRepository, runs interfaces.SopRunRepository,
) *ItemService {
	return &ItemService{projects: projects, items: items, extras: extras, runs: runs}
}

// Create 新建事项：事务内分配编号 → 组装 → 落库 → 业务活动。
//
// 编号格式 `<code>-<n>` 由项目游标决定；父项（IsBig 或带 ParentID）规则在 T62/T63
// 展开，这里只做「父项不存在就报错」的最小校验，避免孤儿。
func (s *ItemService) Create(
	ctx context.Context, tenantID uint64, userID, projectID string, in CreateItemInput,
) (*types.SopItem, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, ErrSopItemTitleRequired
	}
	proj, err := s.projects.GetByID(ctx, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	no, err := s.projects.AllocItemNo(ctx, tenantID, projectID)
	if err != nil {
		return nil, fmt.Errorf("分配事项编号失败：%w", err)
	}
	it := &types.SopItem{
		TenantID:   tenantID,
		ProjectID:  projectID,
		No:         fmt.Sprintf("%s-%d", proj.Code, no),
		Title:      title,
		Status:     types.SopItemStatusBacklog,
		Kind:       firstNonEmptyStr(in.Kind, "requirement"),
		Priority:   firstNonEmptyStr(in.Priority, "P2"),
		Source:     firstNonEmptyStr(in.Source, sopSourceInternal),
		Proposer:   in.Proposer,
		Owner:      in.Owner,
		Tags:       in.Tags,
		TemplateID: in.TemplateID,
		KBID:       in.KBID,
		IsBig:      in.IsBig,
		ParentID:   strings.TrimSpace(in.ParentID),
		CreatedBy:  userID,
	}
	it.Description = in.Description
	if it.ParentID != "" {
		if _, err := s.items.GetByID(ctx, tenantID, it.ParentID); err != nil {
			return nil, fmt.Errorf("父事项不存在：%w", err)
		}
	}
	if err := s.items.Create(ctx, it); err != nil {
		return nil, err
	}
	s.logActivity(ctx, tenantID, it.ID, userID, "create", "新建事项 "+it.No)
	return it, nil
}

// firstNonEmptyStr 返回第一个非空字符串。
func firstNonEmptyStr(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// Get 取事项详情（带派生字段）。
func (s *ItemService) Get(ctx context.Context, tenantID uint64, id string) (*types.SopItem, error) {
	return s.items.GetByID(ctx, tenantID, id)
}

// Update 修改可编辑字段；状态列由仓储强制保持原值（唯一入口是 UpdateStatus）。
func (s *ItemService) Update(
	ctx context.Context, tenantID uint64, userID, id string, in UpdateItemInput,
) (*types.SopItem, error) {
	it, err := s.items.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if title == "" {
			return nil, ErrSopItemTitleRequired
		}
		it.Title = title
	}
	applyStr := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	applyStr(&it.Description, in.Description)
	applyStr(&it.Kind, in.Kind)
	applyStr(&it.Priority, in.Priority)
	applyStr(&it.Source, in.Source)
	applyStr(&it.Proposer, in.Proposer)
	applyStr(&it.Owner, in.Owner)
	applyStr(&it.TemplateID, in.TemplateID)
	applyStr(&it.KBID, in.KBID)
	if in.Tags != nil {
		it.Tags = *in.Tags
	}
	if in.Outcome != nil {
		it.Outcome = *in.Outcome
	}
	if in.DomainData != nil {
		it.DomainData = *in.DomainData
	}
	if err := s.items.Update(ctx, it); err != nil {
		return nil, err
	}
	s.logActivity(ctx, tenantID, id, userID, "update", "修改事项")
	return it, nil
}

// Delete 软删事项：还有子项时拒绝，避免留下孤立子项。
//
// 守卫按「是否存在子项」判定而不是看 is_big：is_big 可被误清、历史数据也可能
// parent_id 有值但 is_big=false，按标记判定会留下孤儿。
func (s *ItemService) Delete(ctx context.Context, tenantID uint64, userID, id string) error {
	it, err := s.items.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	children, err := s.items.ListWithDerived(ctx, tenantID, it.ProjectID,
		types.SopItemFilter{ParentID: &id}, nil, nil)
	if err != nil {
		return err
	}
	if len(children) > 0 {
		return fmt.Errorf("该大项下还有 %d 个子项，请先处理子项", len(children))
	}
	if err := s.items.Delete(ctx, tenantID, id); err != nil {
		return err
	}
	s.logActivity(ctx, tenantID, id, userID, "delete", "删除事项")
	return nil
}

// UpdateStatus 状态迁移（引擎与 HTTP 共用同一入口）。
func (s *ItemService) UpdateStatus(ctx context.Context, tenantID uint64, id, to string) error {
	from := s.currentStatus(ctx, tenantID, id)
	if !types.CanTransitSopItem(from, to) {
		return fmt.Errorf("%w：%s → %s", ErrSopIllegalTransition, from, to)
	}
	if err := s.items.UpdateStatus(ctx, tenantID, id, to); err != nil {
		return err
	}
	s.logActivity(ctx, tenantID, id, "system", "status", "状态变更为 "+to)
	s.advanceParentIfAllDone(ctx, tenantID, id, to)
	return nil
}

// advanceParentIfAllDone 子项进入终态后检查兄弟项：全部 done/closed 就把父项推进为 done。
//
// 父项走同一个 UpdateStatus 入口（8 态状态机只在一处裁决），拒绝迁移时只记日志，
// 绝不因为父项推不动而回滚子项的写入。
func (s *ItemService) advanceParentIfAllDone(ctx context.Context, tenantID uint64, childID, to string) {
	if to != types.SopItemStatusDone && to != types.SopItemStatusClosed {
		return
	}
	kid, err := s.items.GetByID(ctx, tenantID, childID)
	if err != nil || kid.ParentID == "" {
		return
	}
	siblings, err := s.items.ListWithDerived(ctx, tenantID, kid.ProjectID,
		types.SopItemFilter{ParentID: &kid.ParentID}, nil, nil)
	if err != nil {
		log.Printf("[sop-item] 事项 %s 读取兄弟项失败：%v", childID, err)
		return
	}
	for _, sib := range siblings {
		if sib.Status != types.SopItemStatusDone && sib.Status != types.SopItemStatusClosed {
			return
		}
	}
	// 父项可能停在 backlog（大项常是新建后直接拆分）：8 态状态机不允许 backlog→done，
	// 按最短路逐步迁移，每一步都走同一个 UpdateStatus 入口（状态机只在一处裁决）。
	parent, err := s.items.GetByID(ctx, tenantID, kid.ParentID)
	if err != nil {
		return
	}
	for _, step := range types.SopItemStatusPath(parent.Status, types.SopItemStatusDone) {
		if err := s.UpdateStatus(ctx, tenantID, kid.ParentID, step); err != nil {
			log.Printf("[sop-item] 父项 %s 自动推进到 %s 失败：%v", kid.ParentID, step, err)
			return
		}
	}
	s.logActivity(ctx, tenantID, kid.ParentID, "system", "auto_done", "子项全部完成，大项自动完成")
}

// currentStatus 读当前状态；读失败返回空串（状态机对空串一律拒绝，等价于「不存在」）。
func (s *ItemService) currentStatus(ctx context.Context, tenantID uint64, id string) string {
	it, err := s.items.GetByID(ctx, tenantID, id)
	if err != nil || it == nil {
		return ""
	}
	return it.Status
}

// BulkUpdateStatus 批量迁移：正在执行（running/intervene）的事项跳过并回传，
// 其余逐个过状态机；非法迁移同样进 skipped，不中断整批。
func (s *ItemService) BulkUpdateStatus(
	ctx context.Context, tenantID uint64, userID string, ids []string, to string,
) (*BulkResult, error) {
	if len(ids) == 0 {
		return &BulkResult{Updated: []string{}, Skipped: []string{}}, nil
	}
	if len(ids) > sopBulkLimit {
		return nil, ErrSopItemTooMany
	}
	skip := make(map[string]bool, len(ids))
	for _, id := range ids {
		if s.isRunning(ctx, tenantID, id) {
			skip[id] = true
		}
	}
	updated, skipped := s.items.BulkUpdateStatus(ctx, tenantID, ids, to, skip)
	for _, id := range updated {
		s.logActivity(ctx, tenantID, id, userID, "status", "批量变更为 "+to)
	}
	if updated == nil {
		updated = []string{}
	}
	if skipped == nil {
		skipped = []string{}
	}
	return &BulkResult{Updated: updated, Skipped: skipped}, nil
}

// isRunning 判断事项的派生执行情况是否为「正在推进」或「需要介入」。
func (s *ItemService) isRunning(ctx context.Context, tenantID uint64, id string) bool {
	it, err := s.items.GetByID(ctx, tenantID, id)
	if err != nil || it == nil {
		return true // 不存在也跳过（不报错），批量语义是「能做的做掉」
	}
	status, hasRun := s.latestRunStatus(ctx, tenantID, id)
	switch types.DeriveExecState(it.Status, status, hasRun) {
	case types.SopExecStateRunning, types.SopExecStateIntervene:
		return true
	}
	return false
}

// ListWithDerived 列表 + 派生字段：先按「每个事项最近一次运行」聚合，再交给仓储
// 一次算完（T2 的纯函数），避免逐行回查。
func (s *ItemService) ListWithDerived(
	ctx context.Context, tenantID uint64, projectID string, f types.SopItemFilter,
) ([]types.SopItemWithDerived, error) {
	runStatusByItem, nodeRunsByItem, err := s.aggregateRuns(ctx, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	list, err := s.items.ListWithDerived(ctx, tenantID, projectID, f, runStatusByItem, nodeRunsByItem)
	if err != nil {
		return nil, err
	}
	return s.rollupParents(ctx, tenantID, projectID, list, runStatusByItem, nodeRunsByItem)
}

// rollupParents 给大项补子项计数与汇总执行情况。
//
// 大项自己的运行历史没有意义（大项不能直接触发），列表上的「执行情况」必须是子项
// 汇总（spec §3.10：intervene>running>pr>全 closed，空容器=none）。子项状态按项目
// 全量算一次——过滤条件可能把子项筛掉，用筛选后的行汇总会得出错误的 none。
func (s *ItemService) rollupParents(
	ctx context.Context, tenantID uint64, projectID string,
	list []types.SopItemWithDerived,
	runStatusByItem map[string]string, nodeRunsByItem map[string][]string,
) ([]types.SopItemWithDerived, error) {
	hasBig := false
	for _, it := range list {
		if it.IsBig {
			hasBig = true
			break
		}
	}
	if !hasBig || strings.TrimSpace(projectID) == "" {
		return list, nil
	}
	all, err := s.items.ListWithDerived(ctx, tenantID, projectID, types.SopItemFilter{},
		runStatusByItem, nodeRunsByItem)
	if err != nil {
		return nil, err
	}
	kidsOf := map[string][]types.SopItemWithDerived{}
	for _, it := range all {
		if it.ParentID != "" {
			kidsOf[it.ParentID] = append(kidsOf[it.ParentID], it)
		}
	}
	for i := range list {
		if !list[i].IsBig {
			continue
		}
		kids := kidsOf[list[i].ID]
		states := make([]string, 0, len(kids))
		done := 0
		for _, kid := range kids {
			states = append(states, kid.ExecState)
			if kid.Status == types.SopItemStatusDone || kid.Status == types.SopItemStatusClosed {
				done++
			}
		}
		exec := types.DeriveParentExecState(states)
		list[i].ChildTotal = len(kids)
		list[i].ChildDone = done
		list[i].ChildExec = exec
		list[i].ExecState = exec
	}
	return list, nil
}

// aggregateRuns 汇总项目内每个事项的「最近一次运行状态」与「该次运行的节点引用」。
//
// 只算最近一次：更早的运行属于历史，混进来会让进度与当前环节失真。
func (s *ItemService) aggregateRuns(
	ctx context.Context, tenantID uint64, projectID string,
) (map[string]string, map[string][]string, error) {
	runStatus := map[string]string{}
	nodeRefs := map[string][]string{}
	if s.runs == nil || strings.TrimSpace(projectID) == "" {
		return runStatus, nodeRefs, nil
	}
	runs, err := s.runs.ListRuns(ctx, tenantID, types.SopRunFilter{ProjectID: projectID}, 0, 0)
	if err != nil {
		return nil, nil, err
	}
	// ListRuns 按 created_at 倒序：每个事项第一次见到的那条就是最近一次。
	latest := map[string]types.SopRun{}
	for _, run := range runs {
		if run.ItemID == "" {
			continue
		}
		if _, ok := latest[run.ItemID]; !ok {
			latest[run.ItemID] = run
		}
	}
	for itemID, run := range latest {
		runStatus[itemID] = run.Status
		nrs, err := s.runs.ListNodeRuns(ctx, tenantID, run.ID)
		if err != nil {
			continue // 节点读失败只影响进度展示，不让整页失败
		}
		refs := make([]string, 0, len(nrs))
		for _, nr := range nrs {
			refs = append(refs, nr.Status+types.SopNodeRefSep+sopNodeName(&run, nr.NodeID))
		}
		nodeRefs[itemID] = refs
	}
	return runStatus, nodeRefs, nil
}

// latestRunStatus 取某事项最近一次运行的状态（没有运行返回 ""、false）。
func (s *ItemService) latestRunStatus(
	ctx context.Context, tenantID uint64, itemID string,
) (string, bool) {
	if s.runs == nil {
		return "", false
	}
	runs, err := s.runs.ListRuns(ctx, tenantID, types.SopRunFilter{ItemID: itemID}, 1, 1)
	if err != nil || len(runs) == 0 {
		return "", false
	}
	return runs[0].Status, true
}

// sopNodeName 从运行快照取节点名（快照里没有时退化为节点 id）。
func sopNodeName(run *types.SopRun, nodeID string) string {
	for i := range run.WorkflowSnapshot.Nodes {
		n := &run.WorkflowSnapshot.Nodes[i]
		if n.ID == nodeID {
			if name := strings.TrimSpace(n.Name); name != "" {
				return name
			}
			break
		}
	}
	return nodeID
}

// ---- 附件 ----

// AddAttachment 上传附件：校验大小与扩展名，文本类额外抽取正文。
func (s *ItemService) AddAttachment(
	ctx context.Context, tenantID uint64, userID, itemID, fileName, contentType string, data []byte,
) (*types.SopItemAttachment, error) {
	if _, err := s.items.GetByID(ctx, tenantID, itemID); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(path.Base(strings.ReplaceAll(fileName, "\\", "/")))
	if name == "" || len(data) == 0 {
		return nil, ErrSopAttachmentEmpty
	}
	if len(data) > sopAttachmentMaxSize {
		return nil, ErrSopAttachmentTooLarge
	}
	ext := strings.ToLower(path.Ext(name))
	if _, ok := sopAttachmentExts[ext]; !ok {
		return nil, fmt.Errorf("%w：%s", ErrSopAttachmentType, ext)
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = mime.TypeByExtension(ext)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	text := ""
	if _, ok := sopAttachmentTextExts[ext]; ok {
		text = truncateSopText(string(data), sopAttachmentTextMaxLen)
	}
	a := &types.SopItemAttachment{
		TenantID: tenantID, ItemID: itemID, Name: name, MediaType: contentType,
		Size: int64(len(data)), TextContent: text, Data: data,
	}
	if err := s.items.AddAttachment(ctx, a); err != nil {
		return nil, err
	}
	s.logActivity(ctx, tenantID, itemID, userID, "attachment", "上传附件 "+name)
	a.Data = nil // 响应不回传正文
	return a, nil
}

// ListAttachments 列附件（不含正文）。
func (s *ItemService) ListAttachments(
	ctx context.Context, tenantID uint64, itemID string,
) ([]types.SopItemAttachment, error) {
	if _, err := s.items.GetByID(ctx, tenantID, itemID); err != nil {
		return nil, err
	}
	return s.items.ListAttachments(ctx, tenantID, itemID)
}

// truncateSopText 截断抽取正文（按 rune，避免切坏多字节字符）。
func truncateSopText(s string, max int) string {
	if max <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return string(rs[:max]) + "\n…（内容已截断）"
}

// ---- 澄清 ----

// AddClarification 写入一条待澄清问题（AI 或人工发起）。
func (s *ItemService) AddClarification(
	ctx context.Context, tenantID uint64, itemID, question, source string,
) (*types.SopItemClarification, error) {
	if _, err := s.items.GetByID(ctx, tenantID, itemID); err != nil {
		return nil, err
	}
	q := strings.TrimSpace(question)
	if q == "" {
		return nil, fmt.Errorf("澄清问题不能为空")
	}
	// 新建一律 open 且不带答案：仓储层已强制，这里保证入参不会「顺着调用方」。
	c := &types.SopItemClarification{
		TenantID: tenantID, ItemID: itemID, Question: q, Answer: "", AnsweredAt: nil,
		Status: types.SopClarificationStatusOpen, Source: firstNonEmptyStr(source, "ai"),
	}
	if err := s.extras.AddClarification(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ListClarifications 列事项澄清。
func (s *ItemService) ListClarifications(
	ctx context.Context, tenantID uint64, itemID string,
) ([]types.SopItemClarification, error) {
	return s.extras.ListClarifications(ctx, tenantID, itemID)
}

// AnswerClarification 答复澄清：open → answered 单向；已回答再答报白话错误。
func (s *ItemService) AnswerClarification(
	ctx context.Context, tenantID uint64, itemID, clarificationID, answer string,
) error {
	text := strings.TrimSpace(answer)
	if text == "" {
		return fmt.Errorf("答复内容不能为空")
	}
	if err := s.extras.AnswerClarification(ctx, tenantID, clarificationID, text); err != nil {
		// 仓储用 ErrSopClarificationNotOpen 表达「已答复」；翻译成用户能懂的话。
		if strings.Contains(err.Error(), "not open") || strings.Contains(err.Error(), "澄清") {
			return ErrSopClarificationOpen
		}
		return err
	}
	s.logActivity(ctx, tenantID, itemID, "user", "clarify", "答复澄清")
	return nil
}

// ---- 活动 ----

// ListActivities 列事项活动（kind 空=不过滤）。
func (s *ItemService) ListActivities(
	ctx context.Context, tenantID uint64, itemID, kind string,
) ([]types.SopItemActivity, error) {
	return s.extras.ListActivities(ctx, tenantID, itemID, kind)
}

// logActivity 写一条业务活动（失败只记日志：活动流不该反向影响主操作）。
func (s *ItemService) logActivity(
	ctx context.Context, tenantID uint64, itemID, actor, action, detail string,
) {
	if s.extras == nil || itemID == "" {
		return
	}
	_ = s.extras.AddActivity(ctx, &types.SopItemActivity{
		TenantID: tenantID, ItemID: itemID, Kind: types.SopActivityKindBusiness,
		Actor: actor, Action: action, Detail: detail,
	})
}

// ---- 时间 ----

// nowFn 便于测试注入时间（当前所有写入都取系统时间）。
var nowFn = time.Now

// ---- 大需求拆分 ----

// 拆分相关业务错误（handler 映射 400）。
var (
	ErrSopItemSplitTooDeep  = errors.New("只能拆两层：子事项不能再拆分")
	ErrSopItemSplitEmpty    = errors.New("请至少填写一个子事项")
	ErrSopItemSplitTooMany  = errors.New("一次最多创建 20 个子事项")
	ErrSopItemParentIsBig   = errors.New("该事项是大项，不能直接触发，请对子项触发")
	ErrSopItemChildNotFound = errors.New("子事项不存在或不属于该大项")
)

// sopSplitChildLimit 是一次拆分/模板产出的子事项上限（spec §16.24）。
const sopSplitChildLimit = 20

// Split 把事项拆成子事项（两级上限）。
//
//   - 父项必须是顶层事项（parent_id 为空）——三级嵌套一律拒绝；
//   - 子项编号走项目游标（与独立事项同一序列），parent_id 指向父项；
//   - source 由入口决定（ai/manual），父项自动置 is_big=true（独立事项被拆即升级）；
//   - 只建子项不触发运行（spec 全局约束 21）。
func (s *ItemService) Split(
	ctx context.Context, tenantID uint64, userID, parentID string, specs []types.SopChildItemSpec, source string,
) ([]types.SopItem, error) {
	parent, err := s.items.GetByID(ctx, tenantID, parentID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(parent.ParentID) != "" {
		return nil, ErrSopItemSplitTooDeep
	}
	if len(specs) == 0 {
		return nil, ErrSopItemSplitEmpty
	}
	if len(specs) > sopSplitChildLimit {
		return nil, ErrSopItemSplitTooMany
	}
	created, err := s.createChildren(ctx, tenantID, userID, parent, specs, source)
	if err != nil {
		return nil, err
	}
	if !parent.IsBig {
		if err := s.items.SetBig(ctx, tenantID, parent.ID, true); err != nil {
			return created, err
		}
		parent.IsBig = true
		s.logActivity(ctx, tenantID, parent.ID, userID, "split", "事项升级为大项")
	}
	return created, nil
}

// CreateChildItems 供引擎在模板节点产出 out/child-items.json 后建子项（split_source=template）。
func (s *ItemService) CreateChildItems(
	ctx context.Context, tenantID uint64, parentID string, specs []types.SopChildItemSpec, source string,
) (int, error) {
	parent, err := s.items.GetByID(ctx, tenantID, parentID)
	if err != nil {
		return 0, err
	}
	if len(specs) > sopSplitChildLimit {
		return 0, ErrSopItemSplitTooMany
	}
	if len(specs) == 0 {
		return 0, nil
	}
	created, err := s.createChildren(ctx, tenantID, "system", parent, specs, source)
	if err != nil {
		return len(created), err
	}
	// 模板产出子项同样把父项标记为大项：大项不能直接触发，否则父子会双轨执行。
	if !parent.IsBig {
		if err := s.items.SetBig(ctx, tenantID, parent.ID, true); err != nil {
			return len(created), err
		}
		s.logActivity(ctx, tenantID, parent.ID, "system", "split", "模板产出子事项，事项升级为大项")
	}
	return len(created), nil
}

// createChildren 逐条落库子项（编号由项目游标分配）；空标题跳过。
func (s *ItemService) createChildren(
	ctx context.Context, tenantID uint64, userID string, parent *types.SopItem,
	specs []types.SopChildItemSpec, source string,
) ([]types.SopItem, error) {
	if source == "" {
		source = types.SopSplitSourceManual
	}
	proj, err := s.projects.GetByID(ctx, tenantID, parent.ProjectID)
	if err != nil {
		return nil, err
	}
	out := make([]types.SopItem, 0, len(specs))
	for _, spec := range specs {
		title := strings.TrimSpace(spec.Title)
		if title == "" {
			continue
		}
		no, err := s.projects.AllocItemNo(ctx, tenantID, parent.ProjectID)
		if err != nil {
			return out, fmt.Errorf("分配事项编号失败：%w", err)
		}
		kid := &types.SopItem{
			TenantID: tenantID, ProjectID: parent.ProjectID,
			No:    fmt.Sprintf("%s-%d", proj.Code, no),
			Title: title, Description: spec.Description,
			Status:   types.SopItemStatusBacklog,
			Kind:     firstNonEmptyStr(spec.Kind, "requirement"),
			Priority: "P2", Source: sopSourceInternal,
			TemplateID: spec.TemplateID,
			ParentID:   parent.ID, SplitSource: source,
			CreatedBy: userID,
		}
		if err := s.items.Create(ctx, kid); err != nil {
			return out, err
		}
		s.logActivity(ctx, tenantID, kid.ID, userID, "split", "由大项 "+parent.No+" 拆出")
		out = append(out, *kid)
	}
	return out, nil
}

// ListChildren 列某大项的子项（带派生字段）。
func (s *ItemService) ListChildren(
	ctx context.Context, tenantID uint64, parentID string,
) ([]types.SopItemWithDerived, error) {
	parent, err := s.items.GetByID(ctx, tenantID, parentID)
	if err != nil {
		return nil, err
	}
	pid := parent.ID
	return s.ListWithDerived(ctx, tenantID, parent.ProjectID, types.SopItemFilter{ParentID: &pid})
}

// DetachChild 把子项移出大项（置空 parent_id；父项 is_big 保持不变，可能是空容器）。
func (s *ItemService) DetachChild(
	ctx context.Context, tenantID uint64, userID, parentID, childID string,
) error {
	kid, err := s.items.GetByID(ctx, tenantID, childID)
	if err != nil {
		return err
	}
	if kid.ParentID != parentID {
		return ErrSopItemChildNotFound
	}
	if err := s.items.SetParent(ctx, tenantID, kid.ID, "", ""); err != nil {
		return err
	}
	s.logActivity(ctx, tenantID, childID, userID, "detach", "移出大项 "+parentID)
	return nil
}

// EnsureNotBigParent 拦截对父项的直接触发：大项、或已有子项的父项都必须对子项触发。
func (s *ItemService) EnsureNotBigParent(ctx context.Context, tenantID uint64, itemID string) error {
	if strings.TrimSpace(itemID) == "" {
		return nil
	}
	it, err := s.items.GetByID(ctx, tenantID, itemID)
	if err != nil {
		return nil // 交给后续「事项不存在」判定，不在这里吞掉错误类型
	}
	if it.IsBig {
		return ErrSopItemParentIsBig
	}
	kids, err := s.items.ListWithDerived(ctx, tenantID, it.ProjectID,
		types.SopItemFilter{ParentID: &itemID}, nil, nil)
	if err != nil {
		return nil
	}
	if len(kids) > 0 {
		return ErrSopItemParentIsBig
	}
	return nil
}
