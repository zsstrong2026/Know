package service

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// MeetingOpeningStage 开场消息 payload.stage 固定值（前端据此渲染「开始会议 / 调整流程」按钮组）
const MeetingOpeningStage = "opening"

// openingPayload 开场消息的 jsonb 载荷
type openingPayload struct {
	Stage string `json:"stage"`
}

// buildOpeningPrompt 组装主持人的开场提示词（纯函数，单测覆盖）。
// feedback 为空 = 创建后的首次开场；非空 = 「调整流程」时用户对流程的修改意见。
func buildOpeningPrompt(room *types.MeetingRoom, agents []*types.CustomAgent, feedback string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "你是会议室主持人。会议主题：%s\n", room.Title)
	if room.Description != "" {
		b.WriteString("会议描述：" + room.Description + "\n")
	}
	if len(agents) > 0 {
		b.WriteString("参会智能体：\n")
		for _, a := range agents {
			line := "- " + a.Name
			if a.Description != "" {
				line += "：" + truncate(a.Description, 80)
			}
			b.WriteString(line + "\n")
		}
	}
	if feedback != "" {
		b.WriteString("\n用户对上一版会议流程提出了修改意见：\n" + feedback + "\n")
		b.WriteString("请结合该意见重新输出开场内容。\n")
	}
	b.WriteString("\n请以主持人身份输出开场发言，固定包含两个部分：\n")
	b.WriteString("## 主题分析\n对本次会议议题的理解与拆解（背景、要解决的核心问题、关键约束）。\n")
	b.WriteString("## 建议流程\n给出分段/分轮的会议流程建议：每个阶段的讨论重点、希望哪些智能体先发言，以及需要用户确认或拍板的点。\n")
	b.WriteString("\n要求：语言简洁、面向普通用户；不要编造会议中不存在的材料；不要展开具体讨论内容（讨论在用户确认后开始）。")
	return b.String()
}
