// 自我洞悉说明书内追问：上下文限定为「当前说明书 + 主题报告摘要」，不重新检索全库。
package insight

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// ListSelfMessages 返回自我洞悉追问的会话记录（时间正序）。
func (s *Service) ListSelfMessages(
	ctx context.Context,
) ([]*types.InsightSelfMessage, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.ListSelfMessages(ctx, tenantID, ownerID, types.InsightSelfMessageListLimit)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items, nil
}

// AskSelf 在当前自我说明书上下文中回答一个追问，并把问答落库。
//
// 与报告追问一致：模型失败不写任何消息，落库失败只记日志，不吞掉回答。
func (s *Service) AskSelf(ctx context.Context, question string) (string, error) {
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

	self, err := s.repo.GetSelf(ctx, tenantID, ownerID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", apperrors.NewValidationError("self insight is not ready")
		}
		return "", err
	}
	if self.Status != types.InsightSelfStatusReady || len(self.ContentJSON) == 0 {
		return "", apperrors.NewValidationError("self insight is not ready")
	}

	model, err := s.chatModelFor(ctx)
	if err != nil {
		return "", err
	}
	history, err := s.repo.ListSelfMessages(ctx, tenantID, ownerID, types.InsightAskHistoryLimit)
	if err != nil {
		return "", err
	}
	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}
	digests := s.selfReportDigests(ctx, tenantID, ownerID)

	messages := buildSelfAskMessages(self.ContentJSON, digests, history, trimmed)
	answer, err := chatOnce(ctx, model, messages)
	if err != nil {
		logger.Warnf(ctx, "insight self ask: model call failed: %v", err)
		return "", apperrors.NewInternalServerError("insight self answer failed")
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", apperrors.NewInternalServerError("insight self answer is empty")
	}

	now := time.Now()
	userMsg := &types.InsightSelfMessage{
		ID:        uuid.New().String(),
		Role:      types.InsightMessageRoleUser,
		Content:   trimmed,
		CreatedAt: now,
	}
	answerMsg := &types.InsightSelfMessage{
		ID:        uuid.New().String(),
		Role:      types.InsightMessageRoleAssistant,
		Content:   answer,
		CreatedAt: now.Add(time.Millisecond),
	}
	if err := s.repo.CreateSelfMessage(ctx, tenantID, ownerID, userMsg); err != nil {
		logger.Warnf(ctx, "insight self ask: persist user message failed: %v", err)
	}
	if err := s.repo.CreateSelfMessage(ctx, tenantID, ownerID, answerMsg); err != nil {
		logger.Warnf(ctx, "insight self ask: persist answer failed: %v", err)
	}
	return answer, nil
}

// selfReportDigests 取每个内置主题最新报告的结论摘要；读不到的主题直接跳过。
func (s *Service) selfReportDigests(
	ctx context.Context, tenantID uint64, ownerID string,
) []selfReportDigest {
	themes, err := s.builtinThemeList(ctx, tenantID, ownerID)
	if err != nil {
		return nil
	}
	digests := make([]selfReportDigest, 0, len(themes))
	for _, theme := range themes {
		report, reportErr := s.repo.LatestReportByTheme(ctx, tenantID, ownerID, theme.ID)
		if reportErr != nil {
			continue
		}
		var out reportOutput
		_ = decodeJSONObject(report.ContentJSON.ToString(), &out)
		digests = append(digests, selfReportDigest{
			ThemeName:    theme.Name,
			Title:        report.Title,
			CoverageNote: out.CoverageNote,
			Conclusion:   out.Conclusion,
		})
	}
	return digests
}
