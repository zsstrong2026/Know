package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
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

// GenerateOpening 主持人生成开场消息并落库广播（message.created + discussion.flow_confirmation）。
// LLM 不可用/失败时落 system 兜底消息，房间保持在 awaiting_confirmation，用户可直接发言隐式开跑。
func (e *meetingEngine) GenerateOpening(ctx context.Context, room *types.MeetingRoom, feedbackMessageID string) error {
	feedback := ""
	if feedbackMessageID != "" {
		if msg, err := e.messageRepo.GetByID(ctx, feedbackMessageID); err == nil {
			feedback = msg.Content
		}
	}
	agents := e.loadRoomAgents(ctx, room)
	content := e.generateOpeningContent(ctx, room, agents, feedback)
	if content == "" {
		return nil // 兜底 system 消息已在 generateOpeningContent 内落库
	}
	payload, err := json.Marshal(openingPayload{Stage: MeetingOpeningStage})
	if err != nil {
		return err
	}
	msg := &types.MeetingMessage{
		MeetingRoomID: room.ID,
		SenderKind:    types.MeetingSenderHost,
		Content:       content,
		Payload:       payload,
	}
	if err := e.messageRepo.Create(ctx, msg); err != nil {
		return err
	}
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventMessageCreated, Data: msg})
	e.broadcaster.Publish(room.ID, MeetingEvent{Type: MeetingEventFlowConfirmation, Data: map[string]interface{}{
		"room_id": room.ID, "message_id": msg.ID, "stage": MeetingOpeningStage,
	}})
	return nil
}

// loadRoomAgents 加载参会智能体实体（开场/产物生成取名称与专长用）
func (e *meetingEngine) loadRoomAgents(ctx context.Context, room *types.MeetingRoom) []*types.CustomAgent {
	participants, err := e.participantRepo.ListByRoom(ctx, room.ID)
	if err != nil {
		return nil
	}
	out := make([]*types.CustomAgent, 0)
	for _, p := range participants {
		if p.Kind != types.ParticipantKindAgent || p.AgentID == "" {
			continue
		}
		if agent, err := e.agentRepo.GetAgentByID(ctx, p.AgentID, room.TenantID); err == nil {
			out = append(out, agent)
		}
	}
	return out
}

// generateOpeningContent 主持人生成开场正文；模型不可用/生成失败时落 system 兜底消息并返回空串
func (e *meetingEngine) generateOpeningContent(ctx context.Context, room *types.MeetingRoom, agents []*types.CustomAgent, feedback string) string {
	hostChat, err := e.resolveHostChat(ctx)
	if err == nil {
		resp, cerr := hostChat.Chat(ctx,
			[]chat.Message{{Role: "user", Content: buildOpeningPrompt(room, agents, feedback)}},
			&chat.ChatOptions{Temperature: 0.3})
		if cerr != nil {
			logger.Warnf(ctx, "meeting opening generate failed, room=%s err=%v", room.ID, cerr)
		} else if resp != nil && strings.TrimSpace(resp.Content) != "" {
			return resp.Content
		}
	} else {
		logger.Warnf(ctx, "meeting opening host model unavailable, room=%s err=%v", room.ID, err)
	}
	e.publishSystemMessage(ctx, room.ID, "主持人开场生成失败，可直接发言开始会议")
	return ""
}
