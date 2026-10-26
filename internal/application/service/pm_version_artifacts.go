package service

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// 产物写入知识库的通道标识：与知识库文档来源列表保持一致
const (
	pmPRDChannel       = "pm-prd"
	pmPrototypeChannel = "pm-prototype"
)

// decorate 回填列表/详情所需的派生字段：需求数、生成后需求集合是否变动
func (s *pmVersionService) decorate(ctx context.Context, v *types.PMVersion) {
	ids, err := s.deps.Repo.ListRequirementIDs(ctx, v.ID)
	if err != nil {
		return // 派生字段失败不影响主流程，前端按 0/false 展示
	}
	v.RequirementCount = len(ids)
	v.RequirementsChanged = requirementSetChanged(v.ArtifactRequirementIDs, ids)
}

// requirementSetChanged 对比生成快照与当前需求集合（顺序不敏感）
func requirementSetChanged(snapshot types.JSON, current []string) bool {
	if len(snapshot) == 0 {
		return false // 尚未生成过产物，无需提示重新生成
	}
	var ids []string
	if err := json.Unmarshal(snapshot, &ids); err != nil {
		return false
	}
	if len(ids) != len(current) {
		return true
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		seen[id] = struct{}{}
	}
	for _, id := range current {
		if _, ok := seen[id]; !ok {
			return true
		}
	}
	return false
}

// ListVersionRequirements 按加入版本的顺序返回关联需求
func (s *pmVersionService) ListVersionRequirements(
	ctx context.Context, tenantID uint64, versionID string,
) ([]*types.PMRequirement, error) {
	// 先按版本取租户校验，避免跨租户读取需求
	if _, err := s.deps.Repo.GetByID(ctx, tenantID, versionID); err != nil {
		return nil, err
	}
	if s.deps.RequirementRepo == nil {
		return nil, nil
	}
	ids, err := s.deps.Repo.ListRequirementIDs(ctx, versionID)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	list, err := s.deps.RequirementRepo.ListByIDs(ctx, tenantID, ids)
	if err != nil {
		return nil, err
	}
	// ListByIDs 不保证顺序，按关联顺序重排
	byID := make(map[string]*types.PMRequirement, len(list))
	for _, r := range list {
		byID[r.ID] = r
	}
	ordered := make([]*types.PMRequirement, 0, len(ids))
	for _, id := range ids {
		if r, ok := byID[id]; ok {
			ordered = append(ordered, r)
		}
	}
	return ordered, nil
}

// GenerateArtifacts 创建版本产物异步任务：PRD 按章节生成 + 原型 + 功能清单同步。
// 返回 pending 任务载体供前端轮询，真正的生成在 worker 中执行（RunArtifactGeneration）。
func (s *pmVersionService) GenerateArtifacts(
	ctx context.Context, tenantID uint64, versionID string, regenerate bool, chapters []string,
) (*types.PMTask, error) {
	v, err := s.deps.Repo.GetByID(ctx, tenantID, versionID)
	if err != nil {
		return nil, err
	}
	// 只有规划中/评审中的需求集合可继续变动，定稿后不再重新生成
	if v.Status != types.VersionStatusPlanning && v.Status != types.VersionStatusReviewing {
		return nil, fmt.Errorf("%w: %s", ErrVersionNotEditable, v.Status)
	}
	if s.deps.Knowledge == nil || s.deps.ProjectRepo == nil {
		return nil, ErrVersionKnowledgeUnready
	}
	if s.deps.TaskRepo == nil || s.deps.Enqueuer == nil {
		return nil, ErrVersionTaskUnready
	}
	reqs, err := s.ListVersionRequirements(ctx, tenantID, versionID)
	if err != nil {
		return nil, err
	}
	if len(reqs) == 0 {
		return nil, ErrVersionNoRequirements
	}

	taskID := uuid.New().String()
	raw, err := json.Marshal(types.PMPayload{
		TenantID:   tenantID,
		TaskID:     taskID,
		TargetID:   v.ID,
		Title:      versionLabel(v),
		Regenerate: regenerate,
		Chapters:   normalizePRDChapters(chapters),
	})
	if err != nil {
		return nil, err
	}
	task := &types.PMTask{
		ID:        taskID,
		TenantID:  tenantID,
		ProjectID: v.ProjectID,
		TaskType:  types.TypePMPRDGenerate,
		Status:    types.PMTaskStatusPending,
		Payload:   types.JSON(raw),
		CreatedBy: v.OwnerID,
	}
	if err := s.deps.TaskRepo.Create(ctx, task); err != nil {
		return nil, err
	}
	if _, err := s.deps.Enqueuer.Enqueue(asynq.NewTask(types.TypePMPRDGenerate, raw)); err != nil {
		_ = s.deps.TaskRepo.Fail(ctx, task.ID, err.Error())
		return nil, err
	}
	return task, nil
}

// RunArtifactGeneration worker 执行入口：逐章生成 PRD、生成原型、落库并同步功能清单。
// 每完成一章推进一次任务进度；(章节数 + 原型 + 落库) 均分 10%-90%，收尾由 Succeed 置 100。
func (s *pmVersionService) RunArtifactGeneration(
	ctx context.Context, tenantID uint64, taskID string, versionID string,
	regenerate bool, chapters []string,
) (types.JSON, error) {
	v, err := s.deps.Repo.GetByID(ctx, tenantID, versionID)
	if err != nil {
		return nil, err
	}
	if s.deps.Knowledge == nil || s.deps.ProjectRepo == nil {
		return nil, ErrVersionKnowledgeUnready
	}
	reqs, err := s.ListVersionRequirements(ctx, tenantID, versionID)
	if err != nil {
		return nil, err
	}
	if len(reqs) == 0 {
		return nil, ErrVersionNoRequirements
	}
	project, err := s.deps.ProjectRepo.GetByID(ctx, tenantID, v.ProjectID)
	if err != nil {
		return nil, err
	}

	selected := normalizePRDChapters(chapters)
	total := len(selected) + 2 // 各章节 + 原型 + 落库
	done := 0
	round := v.ArtifactRound + 1

	// 模型不可用时不阻断流程：全部章节降级为模板
	model, modelErr := s.resolveArtifactChatModel(ctx)
	if modelErr != nil {
		logger.Warnf(ctx, "[PMVersion] 产物生成降级为模板: version=%s err=%v", v.ID, modelErr)
		model = nil
	}
	basePrompt := s.artifactBasePrompt(ctx, tenantID, project, v, reqs, regenerate)

	var prd strings.Builder
	fmt.Fprintf(&prd, "# %s PRD-%s-v%d\n\n", project.Name, versionLabel(v), round)
	headings := make([]string, 0, len(selected))
	llmChapters := 0
	for _, key := range selected {
		chapter, ok := prdChapterByKey(key)
		if !ok {
			continue
		}
		body := ""
		if model != nil && chapter.UseLLM {
			body = s.generatePRDChapter(ctx, model, chapter, basePrompt, prd.String())
			if body != "" {
				llmChapters++
			}
		}
		if strings.TrimSpace(body) == "" {
			body = chapterTemplate(key, project, v, reqs)
		}
		fmt.Fprintf(&prd, "## %s\n\n%s\n\n", chapter.Heading, strings.TrimSpace(body))
		headings = append(headings, chapter.Heading)
		done++
		s.reportArtifactProgress(ctx, taskID, done, total)
	}

	prototype := ""
	if model != nil {
		prototype = s.generatePrototypeHTML(ctx, model, project, v, prd.String())
	}
	if prototype == "" {
		prototype = renderVersionPrototype(project, v, reqs, round)
	}
	done++
	s.reportArtifactProgress(ctx, taskID, done, total)

	prdDoc, err := s.deps.Knowledge.CreateKnowledgeFromPassageSync(
		ctx, project.KBID, []string{prd.String()}, pmPRDChannel)
	if err != nil {
		return nil, err
	}
	protoDoc, err := s.deps.Knowledge.CreateKnowledgeFromPassageSync(
		ctx, project.KBID, []string{prototype}, pmPrototypeChannel)
	if err != nil {
		return nil, err
	}

	// 功能清单是可选投影：失败不回滚已生成产物，只记录告警
	features := versionFeatureItems(reqs)
	synced := false
	if s.deps.FeatureSvc != nil {
		if err := s.deps.FeatureSvc.SyncFromPRD(
			ctx, tenantID, v.ProjectID, v.ID, features); err != nil {
			logger.Warnf(ctx, "PM 版本同步功能清单失败: version=%s err=%v", v.ID, err)
		} else {
			synced = true
		}
	}

	now := time.Now()
	ids := make([]string, 0, len(reqs))
	for _, r := range reqs {
		ids = append(ids, r.ID)
	}
	rawIDs, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	v.PRDKnowledgeID = prdDoc.ID
	v.PrototypeKnowledgeID = protoDoc.ID
	v.ArtifactRound = round
	v.ArtifactFeatureCount = len(features)
	v.ArtifactRequirementIDs = types.JSON(rawIDs)
	v.GeneratedAt = &now
	if err := s.deps.Repo.Update(ctx, v); err != nil {
		return nil, err
	}
	done++
	s.reportArtifactProgress(ctx, taskID, done, total)

	result, err := json.Marshal(map[string]any{
		"version_id":             v.ID,
		"version_no":             versionLabel(v),
		"round":                  round,
		"chapters":               headings,
		"llm_chapters":           llmChapters,
		"prd_knowledge_id":       prdDoc.ID,
		"prototype_knowledge_id": protoDoc.ID,
		"feature_count":          len(features),
		"feature_sync":           synced,
		"regenerate":             regenerate,
	})
	if err != nil {
		return nil, err
	}
	return types.JSON(result), nil
}

// reportArtifactProgress 章节级进度上报：10% 起步、90% 封顶，最终由 Succeed 置 100
func (s *pmVersionService) reportArtifactProgress(
	ctx context.Context, taskID string, done int, total int,
) {
	if s.deps.TaskRepo == nil || taskID == "" || total <= 0 {
		return
	}
	progress := pmTaskProgressRunning + 80*done/total
	if progress > 90 {
		progress = 90
	}
	_ = s.deps.TaskRepo.UpdateStatus(ctx, taskID, types.PMTaskStatusRunning, progress)
}

// versionLabel 展示用版本标识：未定稿时用「草稿」，避免出现空前缀
func versionLabel(v *types.PMVersion) string {
	if strings.TrimSpace(v.VersionNo) != "" {
		return v.VersionNo
	}
	return "draft"
}

// versionFeatureItems 版本需求 → 功能清单条目：模块固定「版本需求」，需求标题作功能点
func versionFeatureItems(reqs []*types.PMRequirement) []types.PMFeature {
	items := make([]types.PMFeature, 0, len(reqs))
	for _, r := range reqs {
		items = append(items, types.PMFeature{
			ModuleName:    "版本需求",
			FeatureName:   strings.TrimSpace(r.Title),
			RequirementID: r.ID,
		})
	}
	return items
}

// chapterTemplate 章节降级模板：模型不可用或单章生成失败时按需求详情确定性渲染，
// 章节标题与顺序由 pmPRDChapters 统一控制，此处只产出正文。
func chapterTemplate(
	key string, project *types.PMProject, v *types.PMVersion, reqs []*types.PMRequirement,
) string {
	switch key {
	case prdChapterOverview:
		var sb strings.Builder
		writeKV(&sb, "项目", project.Name)
		writeKV(&sb, "版本名称", v.Name)
		writeKV(&sb, "版本号", versionLabel(v))
		writeKV(&sb, "规划日期", formatPMDate(v.PlanDate))
		writeKV(&sb, "计划上线日", formatPMDate(v.PlanLaunchDate))
		writeKV(&sb, "需求数量", fmt.Sprintf("%d", len(reqs)))
		if note := strings.TrimSpace(v.ReleaseNote); note != "" {
			sb.WriteString("\n**版本说明**\n\n" + note + "\n")
		}
		if outline := strings.TrimSpace(v.UpdateOutline); outline != "" {
			sb.WriteString("\n**更新内容大纲**\n\n" + outline + "\n")
		}
		return sb.String()

	case prdChapterRequirements:
		var sb strings.Builder
		sb.WriteString("| 序号 | 优先级 | 需求标题 | 来源 | 状态 |\n|---|---|---|---|---|\n")
		for i, r := range reqs {
			fmt.Fprintf(&sb, "| %d | %s | %s | %s | %s |\n", i+1,
				tableCell(r.Priority), tableCell(r.Title), tableCell(r.Source), tableCell(r.Status))
		}
		return sb.String()

	case prdChapterFeatures:
		var sb strings.Builder
		sb.WriteString("### 版本需求\n\n")
		for i, r := range reqs {
			fmt.Fprintf(&sb, "#### 功能点 %d：%s\n\n", i+1, strings.TrimSpace(r.Title))
			sb.WriteString("- 操作项：进入「" + strings.TrimSpace(r.Title) + "」页面，按需求描述完成主流程操作\n")
			if desc := strings.TrimSpace(r.Description); desc != "" {
				sb.WriteString("\n**需求描述**\n\n" + desc + "\n\n")
			}
			if plan := strings.TrimSpace(r.DesignPlan); plan != "" {
				sb.WriteString("**设计方案**\n\n" + plan + "\n\n")
			}
		}
		return sb.String()

	case prdChapterInteraction:
		var sb strings.Builder
		sb.WriteString("| 页面/模块 | 关键内容 | 空态 | 加载态 | 错误态 |\n|---|---|---|---|---|\n")
		for _, r := range reqs {
			fmt.Fprintf(&sb, "| %s | %s | 暂无数据 | 骨架屏/加载中 | 提示失败并支持重试 |\n",
				tableCell(r.Title), tableCell(clipPromptRunes(strings.TrimSpace(r.Description), 120)))
		}
		return sb.String()

	case prdChapterDataAPI:
		var sb strings.Builder
		sb.WriteString("| 数据实体 | 关键字段 | 读写时机 |\n|---|---|---|\n")
		for _, r := range reqs {
			fmt.Fprintf(&sb, "| %s | 标题、描述、状态、优先级、来源 | 创建/编辑需求时写入，列表与详情读取 |\n",
				tableCell(r.Title))
		}
		return sb.String()

	case prdChapterNonFunctional:
		return "- 性能：常规列表与详情页首屏加载不超过 2 秒。\n" +
			"- 安全与权限：按租户隔离数据，接口校验登录态与数据归属。\n" +
			"- 兼容性：支持主流浏览器最新两个大版本，桌面端最小宽度 1280px。\n" +
			"- 可观测性：关键操作记录日志，异常返回可定位的错误信息。\n" +
			"- 数据保留：需求与产物随项目生命周期保留，删除需二次确认。\n"

	case prdChapterAcceptance:
		var sb strings.Builder
		for i, r := range reqs {
			fmt.Fprintf(&sb, "### 验收项 %d：%s\n\n", i+1, strings.TrimSpace(r.Title))
			sb.WriteString("1. 前置条件：项目与版本已创建，需求已加入当前版本。\n")
			sb.WriteString("2. 操作步骤：按需求描述完成主流程操作。\n")
			sb.WriteString("3. 预期结果：功能可用，数据保存正确，异常场景有明确提示。\n\n")
		}
		return sb.String()

	case prdChapterReview:
		if review := versionReviewText(v); review != "" {
			return review
		}
		return "未检测到评审意见，按原需求重生成。"
	}
	return ""
}

// renderVersionPrototype 渲染自包含原型 HTML：版本头 + 需求卡片
func renderVersionPrototype(
	project *types.PMProject, v *types.PMVersion, reqs []*types.PMRequirement, round int,
) string {
	var sb strings.Builder
	title := fmt.Sprintf("%s 原型-%s-v%d", project.Name, versionLabel(v), round)
	sb.WriteString("<!DOCTYPE html>\n<html lang=\"zh-CN\">\n<head>\n<meta charset=\"utf-8\">\n")
	sb.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	fmt.Fprintf(&sb, "<title>%s</title>\n", html.EscapeString(title))
	sb.WriteString(`<style>
body{margin:0;padding:32px;font-family:-apple-system,"PingFang SC","Microsoft YaHei",sans-serif;background:#f5f6f8;color:#1f2329}
h1{margin:0 0 8px;font-size:22px}
.meta{color:#646a73;font-size:13px;margin-bottom:24px}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:16px}
.card{background:#fff;border:1px solid #e5e6eb;border-radius:8px;padding:16px}
.card h2{margin:0 0 8px;font-size:15px}
.tag{display:inline-block;padding:1px 8px;border-radius:10px;background:#eef1f6;color:#4e5969;font-size:12px;margin-right:6px}
.desc{color:#4e5969;font-size:13px;line-height:1.6;margin:8px 0 0;white-space:pre-wrap}
.empty{color:#86909c}
</style>
</head>
<body>
`)
	fmt.Fprintf(&sb, "<h1>%s</h1>\n", html.EscapeString(title))
	fmt.Fprintf(&sb, "<div class=\"meta\">规划日期 %s · 计划上线日 %s · 共 %d 个需求</div>\n",
		formatPMDate(v.PlanDate), formatPMDate(v.PlanLaunchDate), len(reqs))
	if note := strings.TrimSpace(v.ReleaseNote); note != "" {
		fmt.Fprintf(&sb, "<div class=\"meta\">%s</div>\n", html.EscapeString(note))
	}
	sb.WriteString("<div class=\"grid\">\n")
	for _, r := range reqs {
		sb.WriteString("<section class=\"card\">\n")
		fmt.Fprintf(&sb, "<h2>%s</h2>\n", html.EscapeString(strings.TrimSpace(r.Title)))
		fmt.Fprintf(&sb, "<div><span class=\"tag\">%s</span><span class=\"tag\">%s</span></div>\n",
			html.EscapeString(r.Priority), html.EscapeString(r.Status))
		if desc := strings.TrimSpace(r.Description); desc != "" {
			fmt.Fprintf(&sb, "<p class=\"desc\">%s</p>\n", html.EscapeString(desc))
		}
		sb.WriteString("</section>\n")
	}
	sb.WriteString("</div>\n</body>\n</html>\n")
	return sb.String()
}

// formatPMDate 日期缺省统一展示为「—」
func formatPMDate(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02")
}
