// 自我洞悉：聚合 6 个内置主题各自最新一份报告，生成一份覆盖式更新的自我说明书。
package insight

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// GetSelf 返回门槛、生成状态与最新的自我洞悉内容。
func (s *Service) GetSelf(ctx context.Context) (*types.InsightSelfView, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	gate, err := s.selfGate(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	view := &types.InsightSelfView{
		Gate:   *gate,
		Status: types.InsightSelfStatusIdle,
	}
	self, err := s.repo.GetSelf(ctx, tenantID, ownerID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return view, nil
		}
		return nil, err
	}
	if self.Status != "" {
		view.Status = self.Status
	}
	if !self.UpdatedAt.IsZero() {
		updated := self.UpdatedAt
		view.UpdatedAt = &updated
	}
	view.Error = self.Error
	view.ModelID = self.ModelID
	view.Content = self.ContentJSON
	return view, nil
}

// GenerateSelf 校验门槛后异步生成自我洞悉；不等待模型返回。
func (s *Service) GenerateSelf(ctx context.Context) error {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return err
	}
	gate, err := s.selfGate(ctx, tenantID, ownerID)
	if err != nil {
		return err
	}
	if gate.Completed < gate.Required {
		return apperrors.NewValidationError("all builtin insight themes need a report first").
			WithDetails(map[string]interface{}{"missing": gate.Missing})
	}
	if _, err := s.chatModelFor(ctx); err != nil {
		return err
	}
	if err := s.saveSelfState(ctx, tenantID, ownerID, nil, types.InsightSelfStatusGenerating, "", ""); err != nil {
		return err
	}
	// 说明书是覆盖更新：旧追问基于旧结论，重跑时一并清空，避免误读。
	if delErr := s.repo.DeleteSelfMessages(ctx, tenantID, ownerID); delErr != nil {
		logger.Warnf(ctx, "insight self: clear previous messages failed: %v", delErr)
	}
	go s.executeSelfGeneration(tenantID, ownerID)
	return nil
}

// selfGate 统计 6 个内置主题中已有报告的数量与缺失名单。
func (s *Service) selfGate(
	ctx context.Context, tenantID uint64, ownerID string,
) (*types.InsightSelfGate, error) {
	if err := s.seedBuiltinThemes(ctx, tenantID, ownerID); err != nil {
		return nil, err
	}
	themes, err := s.repo.ListThemes(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	gate := &types.InsightSelfGate{Required: types.InsightGateRequired, Missing: []string{}}
	for _, theme := range themes {
		if !theme.Builtin {
			continue
		}
		// 只统计报告数量，避免为了判断门槛把 6 份报告正文都读出来。
		count, countErr := s.repo.CountReportsByTheme(ctx, tenantID, ownerID, theme.ID)
		if countErr != nil {
			return nil, countErr
		}
		if count > 0 {
			gate.Completed++
		} else {
			gate.Missing = append(gate.Missing, theme.Name)
		}
	}
	return gate, nil
}

// executeSelfGeneration 在后台聚合主题报告并调用模型。
func (s *Service) executeSelfGeneration(tenantID uint64, ownerID string) {
	ctx := types.WithExecutionTenant(context.Background(), tenantID)
	themes, err := s.builtinThemeList(ctx, tenantID, ownerID)
	if err != nil {
		s.saveSelfState(ctx, tenantID, ownerID, nil, types.InsightSelfStatusFailed, "读取主题失败", "")
		return
	}
	inputs := make([]selfThemeInput, 0, len(themes))
	missing := make([]string, 0)
	for _, theme := range themes {
		report, reportErr := s.repo.LatestReportByTheme(ctx, tenantID, ownerID, theme.ID)
		if reportErr != nil {
			if errors.Is(reportErr, gorm.ErrRecordNotFound) {
				missing = append(missing, theme.Name)
				continue
			}
			s.saveSelfState(ctx, tenantID, ownerID, nil, types.InsightSelfStatusFailed, "读取报告失败", "")
			return
		}
		inputs = append(inputs, selfThemeInput{Name: theme.Name, Content: report.ContentJSON})
	}
	docCount := 0
	if docs, docsErr := s.listWorkDocs(ctx, tenantID, ownerID); docsErr == nil {
		docCount = len(docs)
	}
	model, err := s.chatModelFor(ctx)
	if err != nil {
		s.saveSelfState(ctx, tenantID, ownerID, nil, types.InsightSelfStatusFailed, "没有可用的聊天模型", "")
		return
	}
	var out selfOutput
	if err := callChatJSON(ctx, model, buildSelfMessages(inputs, docCount, missing), &out); err != nil {
		logger.Warnf(ctx, "insight self: generate failed: %v", err)
		s.saveSelfState(ctx, tenantID, ownerID, nil, types.InsightSelfStatusFailed, "自我洞悉生成失败", model.GetModelID())
		return
	}
	if strings.TrimSpace(out.Portrait) == "" || len(out.Layers) == 0 {
		s.saveSelfState(ctx, tenantID, ownerID, nil, types.InsightSelfStatusFailed,
			"模型返回的自我洞悉结构不完整", model.GetModelID())
		return
	}
	raw, err := json.Marshal(out)
	if err != nil {
		s.saveSelfState(ctx, tenantID, ownerID, nil, types.InsightSelfStatusFailed, "内容序列化失败", model.GetModelID())
		return
	}
	if err := s.saveSelfState(
		ctx, tenantID, ownerID, types.JSON(raw), types.InsightSelfStatusReady, "", model.GetModelID(),
	); err != nil {
		logger.Warnf(ctx, "insight self: save content failed: %v", err)
	}
}

// builtinThemeList 返回内置主题（先确保种子已写入）。
func (s *Service) builtinThemeList(
	ctx context.Context, tenantID uint64, ownerID string,
) ([]*types.InsightTheme, error) {
	if err := s.seedBuiltinThemes(ctx, tenantID, ownerID); err != nil {
		return nil, err
	}
	themes, err := s.repo.ListThemes(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	out := make([]*types.InsightTheme, 0, len(themes))
	for _, theme := range themes {
		if theme.Builtin {
			out = append(out, theme)
		}
	}
	return out, nil
}

// saveSelfState 覆盖更新自我洞悉行；content 为 nil 表示清空旧内容。
func (s *Service) saveSelfState(
	ctx context.Context, tenantID uint64, ownerID string,
	content types.JSON, status, message, modelID string,
) error {
	self, err := s.repo.GetSelf(ctx, tenantID, ownerID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	now := time.Now()
	if self == nil {
		self = &types.InsightSelf{
			ID:        uuid.New().String(),
			Status:    status,
			ModelID:   modelID,
			Error:     message,
			CreatedAt: now,
		}
	} else {
		if self.ID == "" {
			self.ID = uuid.New().String()
		}
		self.Status = status
		self.Error = message
		self.ModelID = modelID
	}
	self.ContentJSON = content
	self.UpdatedAt = now
	return s.repo.SaveSelf(ctx, tenantID, ownerID, self)
}
