package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/common/redislock"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// ErrArtifactBusy 房间已有产物生成任务进行中（handler 判等映射 409）
var ErrArtifactBusy = errors.New("meeting: another artifact generation is running")

// artifactLockKey 房间级产物生成互斥锁（同房间串行，避免采集器 diff 去重互相干扰）
func artifactLockKey(roomID string) string { return fmt.Sprintf("meeting:%s:artifact_lock", roomID) }

// artifactNeedsFile 该形态的交付物是否必须为沙箱产出的文件
func artifactNeedsFile(kindType string) bool {
	switch kindType {
	case types.MeetingArtifactTypeDocx, types.MeetingArtifactTypeXlsx, types.MeetingArtifactTypePptx,
		types.MeetingArtifactTypeHtmlReport, types.MeetingArtifactTypeChartImage:
		return true
	}
	return false
}

// artifactExpectedExts 形态 → 期望文件扩展名（命中者优先作为交付物）
func artifactExpectedExts(kindType string) []string {
	switch kindType {
	case types.MeetingArtifactTypeDocx:
		return []string{".docx"}
	case types.MeetingArtifactTypeXlsx:
		return []string{".xlsx"}
	case types.MeetingArtifactTypePptx:
		return []string{".pptx"}
	case types.MeetingArtifactTypeHtmlReport:
		return []string{".html", ".htm"}
	case types.MeetingArtifactTypeChartImage:
		return []string{".png", ".jpg", ".jpeg", ".svg", ".webp"}
	}
	return nil
}

// pickArtifactFile 从本次采集结果中挑出交付物：期望扩展优先，否则降级取第一个；无文件返回 false
func pickArtifactFile(arts types.MessageArtifacts, kindType string) (types.MessageArtifact, bool) {
	if len(arts) == 0 {
		return types.MessageArtifact{}, false
	}
	for _, ext := range artifactExpectedExts(kindType) {
		for _, a := range arts {
			if strings.EqualFold(filepath.Ext(a.FileName), ext) {
				return a, true
			}
		}
	}
	return arts[0], true
}

// sanitizeArtifactFileName 标题 → 安全文件名基：仅保留字母/数字/CJK，其余折叠为连字符
func sanitizeArtifactFileName(title string) string {
	if title == "" {
		return "artifact"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r >= 0x4e00 && r <= 0x9fff:
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "artifact"
	}
	if r := []rune(out); len(r) > 60 { // 文件名长度保险
		out = string(r[:60])
	}
	return out
}

// defaultArtifactTitle 未填标题时按形态回缺省名
func defaultArtifactTitle(kindType string) string {
	switch kindType {
	case types.MeetingArtifactTypeSolution:
		return "方案"
	case types.MeetingArtifactTypeMinutes:
		return "会议纪要"
	case types.MeetingArtifactTypeTodoList:
		return "待办事项"
	case types.MeetingArtifactTypeDocx:
		return "Word 文档"
	case types.MeetingArtifactTypeXlsx:
		return "Excel 表格"
	case types.MeetingArtifactTypePptx:
		return "PPT 演示"
	case types.MeetingArtifactTypeHtmlReport:
		return "网页报告"
	case types.MeetingArtifactTypeChartImage:
		return "图表"
	}
	return "Markdown 报告"
}

// artifactKindInstruction 形态 → 执行指令（注入生成 prompt；文件形态给出建议文件名）
func artifactKindInstruction(kindType, title string) string {
	base := sanitizeArtifactFileName(title)
	switch kindType {
	case types.MeetingArtifactTypeSolution:
		return "请以主持人身份，将本次会议讨论汇总为一份结构化方案，固定包含以下小节：\n" +
			"## 背景与目标\n## 分歧与未达成一致的点\n## 方案要点\n## 风险与待确认事项\n## 下一步行动\n" +
			"要求：「## 分歧与未达成一致的点」必须逐条列出尚未形成一致意见的点及各方立场，不得只写共识；" +
			"若确无分歧，须写明「本次讨论未出现分歧」并给出依据。其余小节不得引入讨论记录之外的新观点。" +
			"直接在最终回答中输出完整 Markdown 正文，不要生成文件。"
	case types.MeetingArtifactTypeReportMarkdown:
		return "请在最终回答中直接输出完整的 Markdown 报告正文，不要生成文件。"
	case types.MeetingArtifactTypeMinutes:
		return "请在最终回答中直接输出完整的会议纪要（Markdown，含议题、结论、分歧、行动项），不要生成文件。"
	case types.MeetingArtifactTypeTodoList:
		return "请在最终回答中直接输出待办事项清单（Markdown 复选框列表，含负责人建议与优先级），不要生成文件。"
	case types.MeetingArtifactTypeDocx:
		return fmt.Sprintf("请调用工具在沙箱工作目录生成 Word 文档，文件名建议为 %s.docx，并在最终回答中说明产物要点。", base)
	case types.MeetingArtifactTypeXlsx:
		return fmt.Sprintf("请调用工具在沙箱工作目录生成 Excel 表格，文件名建议为 %s.xlsx，并在最终回答中说明产物要点。", base)
	case types.MeetingArtifactTypePptx:
		return fmt.Sprintf("请调用工具在沙箱工作目录生成 PPT 演示文稿，文件名建议为 %s.pptx，并在最终回答中说明产物要点。", base)
	case types.MeetingArtifactTypeHtmlReport:
		return fmt.Sprintf("请调用工具在沙箱工作目录生成单文件 HTML 报告（自包含样式、无外部依赖），文件名建议为 %s.html，并在最终回答中说明产物要点。", base)
	case types.MeetingArtifactTypeChartImage:
		return fmt.Sprintf("请调用工具在沙箱工作目录生成图表图片（优先 PNG），文件名建议为 %s.png，并在最终回答中说明图表结论。", base)
	}
	return ""
}

// buildArtifactPrompt 组装产物生成 prompt（纯函数，单测覆盖）
func buildArtifactPrompt(
	room *types.MeetingRoom, recent, adopted []*types.MeetingMessage,
	kindType, title, requirement string,
	labelOf func(*types.MeetingMessage) string, instruction string,
) string {
	var b strings.Builder
	b.WriteString("你是会议室主持人，现在需要为本会议产出一份交付物。\n")
	fmt.Fprintf(&b, "会议主题：%s\n", effectiveTopic(room))
	if room.Description != "" {
		b.WriteString("会议描述：" + room.Description + "\n")
	}
	fmt.Fprintf(&b, "产物标题：%s\n产物形态：%s\n", title, kindType)
	if requirement != "" {
		b.WriteString("用户补充要求：" + requirement + "\n")
	}
	if len(recent) > 0 {
		b.WriteString("\n【会议讨论记录（截取）】\n")
		for _, m := range recent {
			fmt.Fprintf(&b, "%s: %s\n", labelOf(m), truncate(m.Content, 400))
		}
	}
	if len(adopted) > 0 {
		b.WriteString("\n【用户已采纳的意见】\n")
		for _, m := range adopted {
			fmt.Fprintf(&b, "- %s: %s\n", labelOf(m), truncate(m.Content, 400))
		}
	}
	b.WriteString("\n【执行要求】\n")
	b.WriteString(instruction + "\n")
	b.WriteString("内容必须忠于会议记录与采纳意见；需要补充事实、数据时，取自知识库检索或联网搜索结果并注明来源与发布日期，不得编造。")
	if artifactNeedsFile(kindType) {
		// 显式给出收敛条件：检索是为了交付物服务，不能因个别来源核验不到就放弃生成文件
		b.WriteString("\n检索次数上限 5 次；关键事实核验完成、或来源确实缺失时，必须立即按上述要求生成文件，" +
			"无法核验的内容在文档中标注「待核验」，不得因继续检索而不交付文件。")
	}
	return b.String()
}

// buildArtifactResearchPrompt 组装第一段「资料核验」提示词：只产出事实清单，不产出交付物。
// 与生成段分离，是为了让核验与排版各自消耗独立迭代预算，避免单段任务在检索阶段耗尽轮次。
func buildArtifactResearchPrompt(
	room *types.MeetingRoom, recent, adopted []*types.MeetingMessage,
	labelOf func(*types.MeetingMessage) string,
) string {
	var b strings.Builder
	b.WriteString("你是会议室的资料核验助手。本阶段只核实事实，不写报告、不生成任何文件。\n")
	fmt.Fprintf(&b, "会议主题：%s\n", effectiveTopic(room))
	if room.Description != "" {
		b.WriteString("会议描述：" + room.Description + "\n")
	}
	if len(recent) > 0 {
		b.WriteString("\n【会议讨论记录（截取）】\n")
		for _, m := range recent {
			fmt.Fprintf(&b, "%s: %s\n", labelOf(m), truncate(m.Content, 400))
		}
	}
	if len(adopted) > 0 {
		b.WriteString("\n【用户已采纳的意见】\n")
		for _, m := range adopted {
			fmt.Fprintf(&b, "- %s: %s\n", labelOf(m), truncate(m.Content, 400))
		}
	}
	b.WriteString("\n【核验任务】\n")
	b.WriteString("- 针对讨论中提到的关键数据、事件、价格与结论联网核实，检索次数上限 5 次。\n")
	b.WriteString("- 逐条输出事实清单，每条固定五要素：结论｜数据｜发布日期｜来源链接｜来源类型（第三方研究/厂商新闻稿/媒体报道）。\n")
	b.WriteString("- 无法核实的条目写「未核实」并说明原因，不得用记忆中的数据补齐。\n")
	b.WriteString("- 只输出事实清单，不要写报告、不要生成文件、不要输出文档脚本。\n")
	return b.String()
}

// buildMeetingArtifactAgent 构造产物执行智能体（合成、不落库）。
// 模型 = 租户默认 KnowledgeQA（resolveChatModelID 对非空 ModelID 强校验类型）；
// 沙箱 = 调用方解析的租户具名配置（空则 AgentQA 走部署级 lite/禁用策略）；
// SkillsSelectionMode=all 打开沙箱内已装技能，兑现「可调用工具箱技能」；
// kbIDs 非空时按 selected 模式注入，让产物可基于参会智能体关联知识库补足事实；
// webSearchEnabled 继承会议级联网总闸，开启后可用平台搜索工具核验事实（AgentQA 侧还需 req 同为真）。
func buildMeetingArtifactAgent(tenantID uint64, modelID, sandboxConfigID string, kbIDs []string, rerankModelID string, webSearchEnabled bool) *types.CustomAgent {
	cfg := types.CustomAgentConfig{
		AgentMode:           "smart-reasoning",
		ModelID:             modelID,
		SandboxConfigID:     sandboxConfigID,
		SystemPrompt:        "你是会议室的产物生成助手，负责把会议讨论沉淀为指定形态的交付物。严格遵循用户的形态与文件要求，善用可用的工具与技能，最终回答用中文。",
		SkillsSelectionMode: "all",
		MCPSelectionMode:    "none",
		WebSearchEnabled:    webSearchEnabled,
		// 默认不注入知识库；调用方给出范围时切到 selected，并配套 rerank 模型（AgentQA 硬性校验）
		KBSelectionMode: "none",
		// 文件形态需要「先检索核验、再生成交付物」，20 轮易在核验阶段耗尽；
		// 抬高上限给核验+写文件留出预算，同时由 prompt 约束检索次数避免无限检索。
		MaxIterations: 40,
		Temperature:   0.3,
	}
	if len(kbIDs) > 0 && rerankModelID != "" {
		// 检索工具强制要求 rerank 模型；租户未配置时保持不注入，避免整条生成链路报错
		cfg.KBSelectionMode = "selected"
		cfg.KnowledgeBases = kbIDs
		cfg.RerankModelID = rerankModelID
	}
	agent := &types.CustomAgent{
		ID:       "meeting-artifact-executor",
		TenantID: tenantID,
		Name:     "会议产物生成器",
		Config:   cfg,
	}
	agent.EnsureDefaults()
	// EnsureDefaults 对 smart-reasoning 会强制开启多轮历史；产物生成是单次任务，须在其后显式关闭
	agent.Config.MultiTurnEnabled = false
	return agent
}

// buildMeetingArtifactResearchAgent 构造第一段「资料核验」智能体（合成、不落库）。
// 与执行体的差异：不挂沙箱/技能，只开联网与知识库，产出纯文本事实清单；
// 独立的小迭代预算（12 轮），核验失败时可整体退回单段生成。
func buildMeetingArtifactResearchAgent(tenantID uint64, modelID string, kbIDs []string, rerankModelID string) *types.CustomAgent {
	cfg := types.CustomAgentConfig{
		AgentMode:           "smart-reasoning",
		ModelID:             modelID,
		SystemPrompt:        "你是会议室的资料核验助手，只负责联网核实事实并输出带来源与发布日期的事实清单，不写报告、不生成文件。",
		SkillsSelectionMode: "none",
		MCPSelectionMode:    "none",
		WebSearchEnabled:    true,
		KBSelectionMode:     "none",
		MaxIterations:       12,
		Temperature:         0.2,
	}
	if len(kbIDs) > 0 && rerankModelID != "" {
		cfg.KBSelectionMode = "selected"
		cfg.KnowledgeBases = kbIDs
		cfg.RerankModelID = rerankModelID
	}
	agent := &types.CustomAgent{
		ID:       "meeting-artifact-researcher",
		TenantID: tenantID,
		Name:     "会议资料核验器",
		Config:   cfg,
	}
	agent.EnsureDefaults()
	agent.Config.MultiTurnEnabled = false
	return agent
}

// meetingArtifactRerankModelID 产物智能体启用知识库检索所需的 rerank 模型：
// 取租户默认 rerank（同内置智能体的 workspace 回退策略），没有则返回空。
func (e *meetingEngine) meetingArtifactRerankModelID(ctx context.Context) string {
	if e.modelService == nil {
		return ""
	}
	models, err := e.modelService.ListModels(ctx)
	if err != nil {
		logger.Warnf(ctx, "meeting artifact list models failed: %v", err)
		return ""
	}
	return pickWorkspaceRerankModelID(models)
}

// ensureArtifactSession 懒创建（并持久化到房间）会议产物后台会话；已存在则直接复用。
// 会话属主 = 会议创建者（复用既有会话产物下载链路的属主校验），描述带隐藏标记。
func (e *meetingEngine) ensureArtifactSession(ctx context.Context, room *types.MeetingRoom) (string, error) {
	if room.ArtifactSessionID != "" {
		return room.ArtifactSessionID, nil
	}
	sess, err := e.sessionService.CreateSession(ctx, &types.Session{
		TenantID:    room.TenantID,
		UserID:      room.CreatedBy,
		Title:       "会议产物生成",
		Description: types.MeetingArtifactSessionMarker + room.ID,
	})
	if err != nil {
		return "", err
	}
	room.ArtifactSessionID = sess.ID
	if err := e.roomRepo.Update(ctx, room); err != nil {
		return "", err
	}
	return sess.ID, nil
}

// meetingArtifactSandboxConfigID 取租户第一个具名沙箱配置作为产物执行沙箱；
// 无配置回退空（文本形态仍可产出，文件形态将失败并写入 error）
func (e *meetingEngine) meetingArtifactSandboxConfigID(ctx context.Context, tenantID uint64) string {
	if e.sandboxConfigRepo == nil {
		return ""
	}
	list, err := e.sandboxConfigRepo.ListByTenant(ctx, tenantID)
	if err != nil || len(list) == 0 {
		return ""
	}
	return list[0].ID
}

// buildArtifactQuery 拉取会议上下文（最近记录 + 采纳意见）并组装产物生成 query。
// kbIDs 非空时追加知识库使用要求：素材 = 会议讨论 + 参会智能体关联知识库。
func (e *meetingEngine) buildArtifactQuery(ctx context.Context, room *types.MeetingRoom, a *types.MeetingArtifact, kbIDs []string) string {
	recent, adopted, labelOf := e.meetingArtifactContext(ctx, room)
	query := buildArtifactPrompt(room, recent, adopted, a.KindType, a.Title, a.Requirement,
		labelOf, artifactKindInstruction(a.KindType, a.Title))
	if len(kbIDs) > 0 {
		query += "\n\n【知识库】已为你开放本次会议参会智能体关联的知识库。需要补充事实、数据或既有资料时，" +
			"请先检索知识库再引用；知识库内容与会议讨论结论要区分表述，不得用知识库内容替换会议已达成的结论。"
	}
	return query
}

// meetingArtifactContext 统一拉取会议上下文（最近讨论 + 已采纳意见）与说话人标签解析。
// 「核验」「生成」两段共用同一份上下文，避免两段各拉一次消息造成口径漂移。
func (e *meetingEngine) meetingArtifactContext(ctx context.Context, room *types.MeetingRoom) ([]*types.MeetingMessage, []*types.MeetingMessage, func(*types.MeetingMessage) string) {
	recent, err := e.messageRepo.ListByRoom(ctx, room.ID, "", e.contextMax)
	if err != nil {
		logger.Warnf(ctx, "meeting artifact list messages failed, room=%s err=%v", room.ID, err)
		recent = nil
	}
	adopted, err := e.messageRepo.ListByReviewState(ctx, room.ID, types.ReviewStateAdopted)
	if err != nil {
		adopted = nil
	}
	labelOf := func(m *types.MeetingMessage) string {
		if m.SenderKind == types.MeetingSenderAgent {
			return e.agentDisplayName(ctx, room.ID, m.SenderID)
		}
		return senderLabel(m)
	}
	return recent, adopted, labelOf
}

// buildArtifactResearchQuery 组装第一段「联网核验」query：只出事实清单，不生成文件。
func (e *meetingEngine) buildArtifactResearchQuery(ctx context.Context, room *types.MeetingRoom) string {
	recent, adopted, labelOf := e.meetingArtifactContext(ctx, room)
	return buildArtifactResearchPrompt(room, recent, adopted, labelOf)
}

// meetingParticipantKnowledgeBaseIDs 参会智能体关联知识库 ∪ 会议级知识库，作为产物生成的知识来源。
// 智能体为 all 模式时展开为该租户全部知识库；取不到时该智能体不贡献范围（不阻断生成）。
func (e *meetingEngine) meetingParticipantKnowledgeBaseIDs(ctx context.Context, room *types.MeetingRoom) []string {
	ids := append([]string{}, room.KnowledgeBaseIDs...)
	if e.participantRepo == nil || e.agentRepo == nil {
		return ids
	}
	ps, err := e.participantRepo.ListByRoom(ctx, room.ID)
	if err != nil {
		logger.Warnf(ctx, "meeting artifact load participants failed, room=%s err=%v", room.ID, err)
		return ids
	}
	for _, p := range filterAgentParticipants(ps) {
		agent, err := e.agentRepo.GetAgentByID(ctx, p.AgentID, room.TenantID)
		if err != nil || agent == nil {
			continue
		}
		ids = mergeUnique(ids, e.agentKnowledgeBaseIDs(ctx, agent))
	}
	return ids
}

// agentKnowledgeBaseIDs 单个智能体的知识库范围：all → 租户全部；其余 → 显式选择列表
func (e *meetingEngine) agentKnowledgeBaseIDs(ctx context.Context, agent *types.CustomAgent) []string {
	if agent.Config.KBSelectionMode != "all" {
		return agent.Config.KnowledgeBases
	}
	if e.kbService == nil {
		return nil
	}
	all, err := e.kbService.ListKnowledgeBases(ctx)
	if err != nil {
		logger.Warnf(ctx, "meeting artifact list knowledge bases failed, agent=%s err=%v", agent.ID, err)
		return nil
	}
	ids := make([]string, 0, len(all))
	for _, kb := range all {
		if kb != nil {
			ids = append(ids, kb.ID)
		}
	}
	return ids
}

// runArtifactAgentQA 在后台会话上执行 AgentQA 并捕获最终回答（模式同 runAgentTurn）。
// 知识库刻意不注入：生成素材为会议记录 + 采纳意见；纯 agent 模式同时避开 rerank 模型硬性校验。
func (e *meetingEngine) runArtifactAgentQA(ctx context.Context, room *types.MeetingRoom, agent *types.CustomAgent, sessionID, assistantMessageID, query string) (string, error) {
	// 生成段仍沿用会议级联网总闸；检索段由 runArtifactResearch 自行指定
	return e.runMeetingAgentQA(ctx, room, agent, sessionID, assistantMessageID, query, room.WebSearchEnabled)
}

// runArtifactResearch 第一段：基于会议讨论联网核验事实，产出纯文本清单（内存态会话，不落库）。
func (e *meetingEngine) runArtifactResearch(ctx context.Context, room *types.MeetingRoom, modelID string, kbIDs []string) (string, error) {
	agent := buildMeetingArtifactResearchAgent(room.TenantID, modelID, kbIDs, e.meetingArtifactRerankModelID(ctx))
	query := e.buildArtifactResearchQuery(ctx, room)
	// 内存态 ID：核验过程不写入房间消息，也不污染后台产物会话
	return e.runMeetingAgentQA(ctx, room, agent, uuid.New().String(), uuid.New().String(), query, true)
}

// runMeetingAgentQA 会议室内的单次 AgentQA 执行：收集最终回答与错误，空回答视为失败。
// webSearch 显式传入，供「核验段强制联网」「生成段继承会议开关」两种语义共用。
func (e *meetingEngine) runMeetingAgentQA(ctx context.Context, room *types.MeetingRoom, agent *types.CustomAgent, sessionID, assistantMessageID, query string, webSearch bool) (string, error) {
	eventBus := event.NewEventBus()
	var answer strings.Builder
	var runErr error
	eventBus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		if data, ok := evt.Data.(event.AgentFinalAnswerData); ok {
			answer.WriteString(data.Content)
		}
		return nil
	})
	eventBus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		if data, ok := evt.Data.(event.ErrorData); ok {
			runErr = fmt.Errorf("stage=%s err=%v", data.Stage, data.Error)
		}
		return nil
	})
	req := &types.QARequest{
		Session:            &types.Session{ID: sessionID, TenantID: room.TenantID},
		Query:              query,
		AssistantMessageID: assistantMessageID,
		CustomAgent:        agent,
		// 请求侧开关与智能体配置为合取关系，二者同时为真才注册搜索工具
		WebSearchEnabled: webSearch,
	}
	if err := e.sessionService.AgentQA(ctx, req, eventBus); err != nil {
		return "", err
	}
	if runErr != nil {
		return "", runErr
	}
	if strings.TrimSpace(answer.String()) == "" {
		return "", errors.New("meeting: artifact agent produced no answer")
	}
	return answer.String(), nil
}

// failArtifact 生成失败收口：置 failed + error 字段 + 广播 artifact.failed
func (e *meetingEngine) failArtifact(ctx context.Context, room *types.MeetingRoom, a *types.MeetingArtifact, cause error) error {
	a.Status = types.MeetingArtifactStatusFailed
	a.Error = cause.Error()
	if err := e.artifactRepo.Update(ctx, a); err != nil {
		logger.Errorf(ctx, "meeting artifact fail-mark update failed, room=%s artifact=%s err=%v", room.ID, a.ID, err)
	}
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventArtifactFailed, Data: map[string]interface{}{
		"artifact_id": a.ID, "error": a.Error,
	}})
	// 只追加事件、不改阶段：失败可能发生在拿锁冲突或产出中，贸然回退会掩盖真实阶段
	recordMeetingEvent(ctx, e.eventRepo, room, "", types.MeetingEventArtifactFailed,
		types.MeetingSenderHost, a.CreatedBy, "产物生成失败", map[string]any{"artifact_id": a.ID, "error": a.Error})
	return cause
}

// RunArtifactGeneration 产物生成主链路：
// room 级互斥 → 懒创建后台会话 → 合成执行智能体 → AgentQA（工具/技能/沙箱）
// → 采集器采集 → 后台会话落 assistant 消息（携带产物索引）→ 回写 MeetingArtifact → 广播。
// 任一环节失败走 failArtifact（置 failed + 广播），锁由 defer 释放（30min lease 兜底）。
func (e *meetingEngine) RunArtifactGeneration(ctx context.Context, room *types.MeetingRoom, a *types.MeetingArtifact) error {
	// Lite 模式下 e.redis 为 typed-nil，直接传入 redislock 会绕过其 nil 校验并在 SetNX 处 panic
	if e.redis == nil {
		return e.failArtifact(ctx, room, a, errors.New("meeting: redis unavailable for artifact generation"))
	}
	token, err := redislock.NewToken()
	if err != nil {
		return e.failArtifact(ctx, room, a, err)
	}
	ok, err := redislock.TryAcquire(ctx, e.redis, artifactLockKey(room.ID), token, 30*time.Minute)
	if err != nil {
		return e.failArtifact(ctx, room, a, err)
	}
	if !ok {
		// 同步预检之后的竞态窗口内被抢锁：本任务失败，前端可重试
		return e.failArtifact(ctx, room, a, ErrArtifactBusy)
	}
	defer func() {
		if _, rerr := redislock.Release(ctx, e.redis, artifactLockKey(room.ID), token); rerr != nil {
			logger.Warnf(ctx, "meeting artifact lock release failed, room=%s err=%v", room.ID, rerr)
		}
	}()

	// 拿到锁才进入产出阶段；失败路径由 failArtifact 记录，阶段不在此处回退
	e.advancePhase(ctx, room, types.MeetingPhaseProducing, types.MeetingEventArtifactStarted,
		types.MeetingSenderHost, a.CreatedBy, "开始生成产物",
		map[string]any{"artifact_id": a.ID, "kind_type": a.KindType})

	sessionID, err := e.ensureArtifactSession(ctx, room)
	if err != nil {
		return e.failArtifact(ctx, room, a, err)
	}
	modelID, err := e.resolveHostModelID(ctx)
	if err != nil {
		return e.failArtifact(ctx, room, a, err)
	}
	if e.collector == nil || e.messageService == nil {
		return e.failArtifact(ctx, room, a, errors.New("meeting: artifact pipeline dependencies unavailable"))
	}
	// 知识来源 = 参会智能体关联知识库 ∪ 会议级知识库；无任何范围时退回纯文本生成
	kbIDs := e.meetingParticipantKnowledgeBaseIDs(ctx, room)

	// 两段式：先联网核验事实，再单独生成交付物。
	// 单段任务里「检索核验 + 排版产出」共用一份迭代预算，实测会在核验阶段耗尽轮次；
	// 拆段后核验段只出事实清单，生成段直接消费清单，并关闭联网避免二次检索。
	query := e.buildArtifactQuery(ctx, room, a, kbIDs)
	factSheet := ""
	if artifactNeedsFile(a.KindType) && room.WebSearchEnabled {
		sheet, rerr := e.runArtifactResearch(ctx, room, modelID, kbIDs)
		if rerr != nil {
			// 核验失败不阻断：退回单段生成，由执行体自行处理来源核验
			logger.Warnf(ctx, "meeting artifact research failed, room=%s artifact=%s err=%v", room.ID, a.ID, rerr)
		} else if s := strings.TrimSpace(sheet); s != "" {
			factSheet = s
			query += "\n\n【已核验资料（检索阶段产出，直接引用，无需再次检索）】\n" + factSheet +
				"\n\n所有事实以【已核验资料】为准；不要再调用联网搜索，直接按执行要求生成文件。"
		}
	}
	execAgent := buildMeetingArtifactAgent(
		room.TenantID, modelID, e.meetingArtifactSandboxConfigID(ctx, room.TenantID),
		kbIDs, e.meetingArtifactRerankModelID(ctx), room.WebSearchEnabled && factSheet == "",
	)

	// 先落占位 assistant 消息再执行：Message.BeforeCreate 会强制重新生成 UUID，
	// 若拿本地 uuid 当 assistantMsgID，会与落库行失配（source_message_id 指向不存在的消息），
	// 导致详情取不到正文、下载定位不到文件。必须先落库拿到真实 ID（同聊天主链路）。
	assistantMsg, err := e.messageService.CreateMessage(ctx, &types.Message{
		SessionID: sessionID, AgentTenantID: room.TenantID,
		Role: "assistant", AgentID: execAgent.ID, IsCompleted: false,
	})
	if err != nil {
		return e.failArtifact(ctx, room, a, err)
	}
	assistantMsgID := assistantMsg.ID

	answer, err := e.runArtifactAgentQA(ctx, room, execAgent, sessionID, assistantMsgID, query)
	if err != nil {
		// 执行失败：清掉占位消息，避免后台会话残留空消息
		if derr := e.messageService.DeleteMessage(ctx, sessionID, assistantMsgID); derr != nil {
			logger.Warnf(ctx, "meeting artifact delete placeholder message failed, room=%s msg=%s err=%v", room.ID, assistantMsgID, derr)
		}
		return e.failArtifact(ctx, room, a, err)
	}

	// 采集沙箱输出目录（对齐 agent_stream_handler：CollectTarget 一次解析出目录与跳过决策）
	var collected types.MessageArtifacts
	if collectDir, skip := e.collector.CollectTarget(ctx, sessionID); !skip {
		if collectDir == "" {
			collectDir = skills.ArtifactOutputDir()
		}
		collected, err = e.collector.Collect(ctx, sessionID, assistantMsgID, room.TenantID, collectDir)
		if err != nil {
			// 采集失败降级为无文件：文本形态仍可交付，文件形态由下方 needsFile 判定失败
			logger.Warnf(ctx, "meeting artifact collect failed, room=%s err=%v", room.ID, err)
		}
	}

	// 回写占位 assistant 消息：产物行按 (session, message) JOIN 寻址的前提（GetSessionArtifacts 联 messages）
	// AgentTenantID 记有效租户：产物下载鉴权在缺 resource 行时以其作为属主兜底（access/files.go）
	if err := e.messageService.UpdateMessage(ctx, &types.Message{
		ID: assistantMsgID, SessionID: sessionID, AgentTenantID: room.TenantID,
		Role: "assistant", Content: answer, AgentID: execAgent.ID, IsCompleted: true,
		Artifacts: collected,
	}); err != nil {
		// 回写失败不阻断交付：正文与产物行已在库，详情/下载仍可用，仅记录便于排查
		logger.Warnf(ctx, "meeting artifact update assistant message failed, room=%s msg=%s err=%v", room.ID, assistantMsgID, err)
	}

	file, hasFile := pickArtifactFile(collected, a.KindType)
	// 方案为纯文本形态：即便沙箱里残留文件也不挂为交付物，避免方案卡片出现无关附件
	if a.KindType == types.MeetingArtifactTypeSolution {
		hasFile = false
	}
	if artifactNeedsFile(a.KindType) && !hasFile {
		return e.failArtifact(ctx, room, a, fmt.Errorf("meeting: expected %s file but sandbox produced none", a.KindType))
	}

	a.Status = types.MeetingArtifactStatusReady
	a.Error = ""
	a.SourceMessageID = assistantMsgID
	if hasFile {
		a.FileName, a.FileType, a.FileSize = file.FileName, file.FileType, file.FileSize
		a.URL, a.ContentHash = file.URL, file.ContentHash
	}
	// 方案形态：正文同时落一条 is_solution 会议消息，并推进房间「当前方案」指针，
	// 让历史产物里的方案与会议室内的版本/回滚语义保持一致（失败不阻断产物交付）
	if a.KindType == types.MeetingArtifactTypeSolution {
		e.attachSolutionMessage(ctx, room, a, answer)
	}
	if err := e.artifactRepo.Update(ctx, a); err != nil {
		// 末次回写失败同样置 failed 并广播：否则产物永久停在 generating，前端无法重试
		return e.failArtifact(ctx, room, a, err)
	}
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventArtifactCreated, Data: a})
	// 产物交付后回到讨论阶段，避免 producing 成为滞留态
	e.advancePhase(ctx, room, types.MeetingPhaseDiscussing, types.MeetingEventArtifactReady,
		types.MeetingSenderHost, a.CreatedBy, "产物生成完成",
		map[string]any{"artifact_id": a.ID, "file_name": a.FileName, "kind_type": a.KindType})
	return nil
}

// attachSolutionMessage 把方案形态产物的正文落为方案消息（版本号沿用产物行），
// 并把房间当前方案指针指向它；任一步失败仅记录，产物本身仍可交付。
func (e *meetingEngine) attachSolutionMessage(
	ctx context.Context, room *types.MeetingRoom, a *types.MeetingArtifact, content string,
) {
	msg := &types.MeetingMessage{
		MeetingRoomID:   room.ID,
		SenderKind:      types.MeetingSenderHost,
		Content:         content,
		IsSolution:      true,
		SolutionVersion: a.Version,
		ReviewState:     types.ReviewStateNone,
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		logger.Warnf(ctx, "meeting artifact create solution message failed, room=%s artifact=%s err=%v", room.ID, a.ID, err)
		return
	}
	// 方案产物同时挂 SolutionMessageID：导出/详情/回滚沿用既有 solution 读路径
	a.SolutionMessageID = msg.ID
	room.CurrentSolutionMessageID = msg.ID
	if err := e.roomRepo.Update(ctx, room); err != nil {
		logger.Warnf(ctx, "meeting artifact update current solution failed, room=%s err=%v", room.ID, err)
	}
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventSolutionCreated, Data: a})
}
