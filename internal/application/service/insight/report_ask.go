// 报告内追问：上下文限定为「这份报告 + 它本次运行的证据」，不重新检索全库。
package insight

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// ListReportMessages 返回报告追问的会话记录（时间正序）。
func (s *Service) ListReportMessages(
	ctx context.Context, reportID string,
) ([]*types.InsightReportMessage, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.repo.GetReport(ctx, tenantID, ownerID, reportID); err != nil {
		return nil, mapNotFound(err, "insight report not found")
	}
	items, err := s.repo.ListReportMessages(ctx, tenantID, ownerID, reportID, 0)
	if err != nil {
		return nil, err
	}
	reverseMessages(items)
	return items, nil
}

// AskReport 在报告上下文中回答一个追问，并把用户消息与回答落库。
//
// 模型调用失败时不写任何消息，避免留下一个没有回答的孤立提问；
// 落库失败只记日志，不吞掉已经生成的回答。
func (s *Service) AskReport(
	ctx context.Context, reportID, question string,
) (string, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(question)
	if trimmed == "" {
		return "", apperrors.NewValidationError("question is required")
	}
	if utf8.RuneCountInString(trimmed) > types.InsightAskQuestionMaxRunes {
		return "", apperrors.NewValidationError("question is too long")
	}

	report, err := s.repo.GetReport(ctx, tenantID, ownerID, reportID)
	if err != nil {
		return "", mapNotFound(err, "insight report not found")
	}
	// 主题可能已被删除，但不影响对既有报告的追问。
	theme, themeErr := s.repo.GetTheme(ctx, tenantID, ownerID, report.ThemeID)
	if themeErr != nil {
		theme = nil
	}
	model, err := s.chatModelFor(ctx)
	if err != nil {
		return "", err
	}
	history, err := s.repo.ListReportMessages(
		ctx, tenantID, ownerID, reportID, types.InsightAskHistoryLimit,
	)
	if err != nil {
		return "", err
	}
	reverseMessages(history)
	evidence, err := s.repo.ListEvidenceForReport(
		ctx, tenantID, ownerID, report.RunID, types.InsightAskEvidenceLimit,
	)
	if err != nil {
		return "", err
	}

	messages := buildReportAskMessages(report, theme, evidence, history, trimmed)
	answer, err := chatOnce(ctx, model, messages)
	if err != nil {
		logger.Warnf(ctx, "insight report ask: model call failed, report=%s: %v", reportID, err)
		return "", apperrors.NewInternalServerError("insight report answer failed")
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", apperrors.NewInternalServerError("insight report answer is empty")
	}

	now := time.Now()
	userMsg := &types.InsightReportMessage{
		ID:        uuid.New().String(),
		ReportID:  reportID,
		Role:      types.InsightMessageRoleUser,
		Content:   trimmed,
		CreatedAt: now,
	}
	answerMsg := &types.InsightReportMessage{
		ID:        uuid.New().String(),
		ReportID:  reportID,
		Role:      types.InsightMessageRoleAssistant,
		Content:   answer,
		CreatedAt: now.Add(time.Millisecond),
	}
	if err := s.repo.CreateReportMessage(ctx, tenantID, ownerID, userMsg); err != nil {
		logger.Warnf(ctx, "insight report ask: persist user message failed, report=%s: %v", reportID, err)
	}
	if err := s.repo.CreateReportMessage(ctx, tenantID, ownerID, answerMsg); err != nil {
		logger.Warnf(ctx, "insight report ask: persist answer failed, report=%s: %v", reportID, err)
	}
	return answer, nil
}

// reverseMessages 把仓储返回的时间倒序翻成时间正序。
func reverseMessages(items []*types.InsightReportMessage) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}
