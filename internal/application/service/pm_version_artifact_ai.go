// Deprecated: PM 工作站已并入 SOP 工作台（T58 起写路径下线）。
// 这里的产物生成逻辑仅作为只读归档期间的实现保留，M1–M9 期间可回滚；
// 新的需求/版本/资料生成请用 SOP 模板与运行，不再从这里扩展。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// PRD 章节 key：前端勾选、任务载荷与逐章生成共用同一套标识
const (
	prdChapterOverview      = "overview"
	prdChapterRequirements  = "requirements"
	prdChapterFeatures      = "features"
	prdChapterInteraction   = "interaction"
	prdChapterDataAPI       = "data_api"
	prdChapterNonFunctional = "nonfunctional"
	prdChapterAcceptance    = "acceptance"
	prdChapterReview        = "review"
)

// 单章/单次原型生成的模型调用超时，避免异常模型拖死任务
const (
	prdChapterTimeout   = 3 * time.Minute
	prototypeTimeout    = 4 * time.Minute
	prdChapterMaxRunes  = 3000 // 后文引用前文时的截断上限
	prdRequirementRunes = 1500 // 单条需求描述截断
	prdAnalysisRunes    = 800  // AI 分析单字段截断
	prdAttachmentRunes  = 2000 // 单个附件正文截断
	prdContextMaxRunes  = 20000
)

// pmPRDChapter 章节定义：Heading 与 pm-prd-generator 技能契约保持一致。
// UseLLM=false 的章节由确定性模板渲染（表格/评审意见不能交给模型自由发挥）。
type pmPRDChapter struct {
	Key     string
	Heading string
	Guide   string
	UseLLM  bool
}

// pmPRDChapters 固定顺序即产物章节顺序，前端勾选也按此顺序回传
var pmPRDChapters = []pmPRDChapter{
	{
		Key: prdChapterOverview, Heading: "1. 版本概述", UseLLM: true,
		Guide: "概述版本目标、范围、目标用户与核心价值，说明与上一版本的差异；不少于 150 字。",
	},
	{Key: prdChapterRequirements, Heading: "2. 需求清单"},
	{
		Key: prdChapterFeatures, Heading: "3. 功能详述", UseLLM: true,
		Guide: "按「模块 → 功能点 → 操作项」三级组织；每个操作项写清触发条件、主流程、异常分支，覆盖全部需求。",
	},
	{
		Key: prdChapterInteraction, Heading: "4. 交互与页面要求", UseLLM: true,
		Guide: "按页面/模块描述布局、字段、默认值、空态、加载态、错误态与关键交互反馈。",
	},
	{
		Key: prdChapterDataAPI, Heading: "5. 数据与接口要求", UseLLM: true,
		Guide: "列出涉及的数据实体、关键字段与读写时机；如涉及接口，给出方法、路径、入参、出参与错误码建议。",
	},
	{
		Key: prdChapterNonFunctional, Heading: "6. 非功能要求", UseLLM: true,
		Guide: "覆盖性能、安全与权限、兼容性、可观测性、数据保留；只写与本次版本相关的约束。",
	},
	{
		Key: prdChapterAcceptance, Heading: "7. 验收标准", UseLLM: true,
		Guide: "按需求逐条给出可验证的验收项（前置条件、操作步骤、预期结果），避免主观描述。",
	},
	{Key: prdChapterReview, Heading: "8. 评审意见处理"},
}

// normalizePRDChapters 过滤非法 key、按固定顺序去重；未选择或全部非法时回退为全部章节
func normalizePRDChapters(selected []string) []string {
	all := make([]string, 0, len(pmPRDChapters))
	for _, c := range pmPRDChapters {
		all = append(all, c.Key)
	}
	if len(selected) == 0 {
		return all
	}
	want := make(map[string]struct{}, len(selected))
	for _, key := range selected {
		if key = strings.TrimSpace(key); key != "" {
			want[key] = struct{}{}
		}
	}
	out := make([]string, 0, len(want))
	for _, c := range pmPRDChapters {
		if _, ok := want[c.Key]; ok {
			out = append(out, c.Key)
		}
	}
	if len(out) == 0 {
		return all // 非法输入不阻断生成
	}
	return out
}

func prdChapterByKey(key string) (pmPRDChapter, bool) {
	for _, c := range pmPRDChapters {
		if c.Key == key {
			return c, true
		}
	}
	return pmPRDChapter{}, false
}

// resolveArtifactChatModel 解析产物生成用的对话模型：优先租户默认知识问答模型
func (s *pmVersionService) resolveArtifactChatModel(ctx context.Context) (chat.Chat, error) {
	if s.deps.Models == nil {
		return nil, errors.New("pm version: model service is not configured")
	}
	models, err := s.deps.Models.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	var fallback *types.Model
	for _, m := range models {
		if m.Type != types.ModelTypeKnowledgeQA {
			continue
		}
		if m.IsDefault {
			return s.deps.Models.GetChatModel(ctx, m.ID)
		}
		if fallback == nil {
			fallback = m
		}
	}
	if fallback == nil {
		return nil, errors.New("pm version: no knowledge QA model available")
	}
	return s.deps.Models.GetChatModel(ctx, fallback.ID)
}

// artifactBasePrompt 产物生成的公共上下文：项目/版本信息 + 需求完整详情 + 评审意见。
// 原型与各 PRD 章节共用，保证同一轮产物口径一致。
func (s *pmVersionService) artifactBasePrompt(
	ctx context.Context, tenantID uint64, project *types.PMProject,
	v *types.PMVersion, reqs []*types.PMRequirement, regenerate bool,
) string {
	var sb strings.Builder
	sb.WriteString("你是 PM 工作站的产品经理助手，按 pm-requirement-analysis 与 pm-prd-generator 技能规范撰写 PRD。\n")
	sb.WriteString("写作要求：结论先行、可执行、只使用给定信息；不臆造需求之外的功能；表格与列表保持 Markdown 合法。\n\n")

	sb.WriteString("## 项目\n\n")
	writePromptKV(&sb, "项目名称", project.Name)
	writePromptKV(&sb, "项目描述", clipPromptRunes(strings.TrimSpace(project.Description), 500))

	sb.WriteString("\n## 版本信息\n\n")
	writePromptKV(&sb, "版本名称", v.Name)
	writePromptKV(&sb, "版本号", versionLabel(v))
	writePromptKV(&sb, "版本状态", v.Status)
	writePromptKV(&sb, "规划日期", formatPMDate(v.PlanDate))
	writePromptKV(&sb, "计划上线日", formatPMDate(v.PlanLaunchDate))
	writePromptKV(&sb, "版本说明", clipPromptRunes(strings.TrimSpace(v.ReleaseNote), 1000))
	writePromptKV(&sb, "更新内容大纲", clipPromptRunes(strings.TrimSpace(v.UpdateOutline), 2000))

	if review := versionReviewText(v); review != "" {
		sb.WriteString("\n## 评审意见（最近一轮）\n\n")
		sb.WriteString(review)
		sb.WriteString("\n")
	}
	if regenerate {
		sb.WriteString("\n本次为重新生成：必须回应上述评审意见，并在「8. 评审意见处理」逐条说明处理结果。\n")
	}

	sb.WriteString("\n## 需求详情\n\n")
	sb.WriteString(s.artifactRequirementContext(ctx, tenantID, reqs))
	return sb.String()
}

// artifactRequirementContext 逐条输出需求的完整详情：
// 基础字段 + 描述 + 设计方案 + AI 分析四要素 + 附件正文，总量超限时截断并标注。
func (s *pmVersionService) artifactRequirementContext(
	ctx context.Context, tenantID uint64, reqs []*types.PMRequirement,
) string {
	var sb strings.Builder
	for i, r := range reqs {
		var block strings.Builder
		fmt.Fprintf(&block, "### 需求 %d：%s\n\n", i+1, strings.TrimSpace(r.Title))
		writePromptKV(&block, "优先级", r.Priority)
		writePromptKV(&block, "来源", r.Source)
		writePromptKV(&block, "提出人", r.Proposer)
		writePromptKV(&block, "当前状态", r.Status)
		if desc := strings.TrimSpace(r.Description); desc != "" {
			block.WriteString("\n**需求描述**\n\n" + clipPromptRunes(desc, prdRequirementRunes) + "\n")
		}
		if plan := strings.TrimSpace(r.DesignPlan); plan != "" {
			block.WriteString("\n**设计方案**\n\n" + clipPromptRunes(plan, prdRequirementRunes) + "\n")
		}
		if ai := formatAIAnalysis(r.AIAnalysis); ai != "" {
			block.WriteString("\n**AI 分析结论**\n\n" + ai + "\n")
		}
		block.WriteString(s.attachmentContext(ctx, tenantID, r.ID))
		block.WriteString("\n")

		if sb.Len()+block.Len() > prdContextMaxRunes {
			sb.WriteString("（需求较多，后续需求详情已截断）\n\n")
			break
		}
		sb.WriteString(block.String())
	}
	return sb.String()
}

// attachmentContext 附件清单与文本正文；无附件或未装配仓储时返回空串
func (s *pmVersionService) attachmentContext(
	ctx context.Context, tenantID uint64, requirementID string,
) string {
	if s.deps.Attachments == nil {
		return ""
	}
	list, err := s.deps.Attachments.ListByRequirement(ctx, tenantID, requirementID)
	if err != nil || len(list) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n**需求附件**\n\n")
	for _, a := range list {
		fmt.Fprintf(&sb, "- %s（%s，%d KB）\n", a.FileName, a.ContentType, (a.Size+1023)/1024)
		if text := strings.TrimSpace(a.TextContent); text != "" {
			sb.WriteString("\n" + clipPromptRunes(text, prdAttachmentRunes) + "\n\n")
		}
	}
	return sb.String()
}

// formatAIAnalysis 解析 AI 分析 JSON（value/impact/risk/design_plan 四要素）
func formatAIAnalysis(raw types.JSON) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	labels := [][2]string{
		{"value", "需求价值"},
		{"impact", "关联影响"},
		{"risk", "技术风险"},
		{"design_plan", "实现方案"},
	}
	var sb strings.Builder
	for _, item := range labels {
		if text := strings.TrimSpace(m[item[0]]); text != "" {
			sb.WriteString("- " + item[1] + "：" + clipPromptRunes(text, prdAnalysisRunes) + "\n")
		}
	}
	return sb.String()
}

// versionReviewText 渲染最近一轮评审意见：轮次 + 结论 + 逐条意见 + 变更明细
func versionReviewText(v *types.PMVersion) string {
	if len(v.ReviewOpinions) == 0 {
		return ""
	}
	var rounds []types.PMVersionReviewRecord
	if err := json.Unmarshal(v.ReviewOpinions, &rounds); err != nil || len(rounds) == 0 {
		return ""
	}
	last := rounds[len(rounds)-1]
	var sb strings.Builder
	fmt.Fprintf(&sb, "- 轮次：第 %d 轮（结论：%s）\n", last.Round, last.Conclusion)
	if strings.TrimSpace(last.Reviewers) != "" {
		sb.WriteString("- 参会人：" + strings.TrimSpace(last.Reviewers) + "\n")
	}
	var payload pmReviewOpinionPayload
	if err := json.Unmarshal(last.Opinions, &payload); err == nil {
		for _, it := range payload.Items {
			text := strings.TrimSpace(it.Text)
			if text == "" {
				continue
			}
			sb.WriteString(fmt.Sprintf("- 意见：%s（状态：%s）\n", text, opinionStatusLabel(it.Status)))
			if note := strings.TrimSpace(it.Note); note != "" {
				sb.WriteString("  - 处理说明：" + note + "\n")
			}
		}
		if note := strings.TrimSpace(payload.ChangeNote); note != "" {
			sb.WriteString("- 变更明细：" + note + "\n")
		}
	}
	return sb.String()
}

// pmReviewOpinionPayload 评审意见载荷：与前端提交结构保持一致
type pmReviewOpinionPayload struct {
	Items      []types.PMReviewOpinionItem `json:"items"`
	ChangeNote string                      `json:"change_note"`
}

func opinionStatusLabel(status string) string {
	if status == types.ReviewOpinionResolved {
		return "已解决"
	}
	return "待处理"
}

// generatePRDChapter 单章 LLM 生成：失败或空返回空串，由调用方降级为模板
func (s *pmVersionService) generatePRDChapter(
	ctx context.Context, model chat.Chat, chapter pmPRDChapter, basePrompt string, prior string,
) string {
	var sb strings.Builder
	sb.WriteString(basePrompt)
	if prior = strings.TrimSpace(prior); prior != "" {
		sb.WriteString("\n## 已生成的章节（保持前后一致，可复用其中术语）\n\n")
		sb.WriteString(tailPromptRunes(prior, prdChapterMaxRunes))
		sb.WriteString("\n")
	}
	sb.WriteString("\n## 本次任务\n\n")
	fmt.Fprintf(&sb, "只输出「%s」的正文 Markdown，不要输出章节标题本身，不要使用代码围栏。\n", chapter.Heading)
	sb.WriteString(chapter.Guide + "\n")

	reqCtx, cancel := context.WithTimeout(ctx, prdChapterTimeout)
	defer cancel()
	resp, err := model.Chat(reqCtx, []chat.Message{{Role: "user", Content: sb.String()}}, &chat.ChatOptions{})
	if err != nil {
		logger.Warnf(ctx, "[PMVersion] PRD 章节生成失败，降级为模板: chapter=%s err=%v", chapter.Key, err)
		return ""
	}
	body := stripCodeFence(resp.Content)
	if strings.TrimSpace(body) == "" {
		return ""
	}
	return body
}

// generatePrototypeHTML 基于 PRD 生成单文件自包含原型；失败返回空串由调用方降级
func (s *pmVersionService) generatePrototypeHTML(
	ctx context.Context, model chat.Chat, project *types.PMProject,
	v *types.PMVersion, prd string,
) string {
	var sb strings.Builder
	sb.WriteString("你是资深前端工程师，按 pm-prototype 技能规范生成可交互原型。\n\n")
	fmt.Fprintf(&sb, "## 项目\n\n- 项目名称：%s\n- 版本：%s（%s）\n\n",
		project.Name, versionLabel(v), v.Name)
	sb.WriteString("## PRD\n\n")
	sb.WriteString(tailPromptRunes(prd, 8000))
	sb.WriteString("\n## 输出要求\n\n")
	sb.WriteString("1. 只输出一个完整 HTML 文档，从 <!DOCTYPE html> 开始，不要任何解释文字与代码围栏。\n")
	sb.WriteString("2. CSS 与 JS 全部内联，禁止引用任何外部 CDN、字体、图片 URL；图标用内联 SVG 或字符。\n")
	sb.WriteString("3. 左侧为模块导航，右侧为页面主区域，按 PRD 第 3 章的功能点组织页面并可切换。\n")
	sb.WriteString("4. 使用 mock 数据填充列表/表单，需包含空态、加载态、错误态示例与基本交互反馈。\n")
	sb.WriteString("5. 桌面优先，最小宽度 1280px；字体栈使用系统字体。\n")

	reqCtx, cancel := context.WithTimeout(ctx, prototypeTimeout)
	defer cancel()
	resp, err := model.Chat(reqCtx, []chat.Message{{Role: "user", Content: sb.String()}}, &chat.ChatOptions{})
	if err != nil {
		logger.Warnf(ctx, "[PMVersion] 原型生成失败，降级为模板: err=%v", err)
		return ""
	}
	html := sanitizeHTMLOutput(resp.Content)
	if html == "" {
		logger.Warnf(ctx, "[PMVersion] 原型输出不是完整 HTML，降级为模板")
	}
	return html
}

// sanitizeHTMLOutput 剥离代码围栏与模型前言，只保留完整 HTML 文档
func sanitizeHTMLOutput(raw string) string {
	out := strings.TrimSpace(stripCodeFence(raw))
	lower := strings.ToLower(out)
	start := -1
	for _, marker := range []string{"<!doctype", "<html"} {
		if idx := strings.Index(lower, marker); idx >= 0 && (start < 0 || idx < start) {
			start = idx
		}
	}
	if start < 0 {
		return ""
	}
	out = out[start:]
	if !strings.Contains(strings.ToLower(out), "</html>") {
		return ""
	}
	return out + "\n"
}

// stripCodeFence 去掉首尾的 Markdown 代码围栏
func stripCodeFence(text string) string {
	out := strings.TrimSpace(text)
	if !strings.HasPrefix(out, "```") {
		return out
	}
	if idx := strings.Index(out, "\n"); idx >= 0 {
		out = out[idx+1:]
	}
	if idx := strings.LastIndex(out, "```"); idx >= 0 {
		out = out[:idx]
	}
	return strings.TrimSpace(out)
}

// tailPromptRunes 保留文本尾部：后续章节更关心最近生成的内容
func tailPromptRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return "…" + string(runes[len(runes)-limit:])
}
