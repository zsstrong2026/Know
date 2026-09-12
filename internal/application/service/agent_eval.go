package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	agentEvalAnswerLimit    = 4000
	agentEvalRetentionLimit = 50
	agentEvalHTTPTimeout    = 120 * time.Second
)

var ErrAgentEvalNotFound = errors.New("agent eval run not found")

type AgentEvalConflictError struct {
	RunID string
}

func (e *AgentEvalConflictError) Error() string {
	return "agent eval run already in progress: " + e.RunID
}

type agentEvalService struct {
	cfg          *config.Config
	repo         interfaces.AgentEvalRepository
	agentService interfaces.CustomAgentService
	modelService interfaces.ModelService
	mu           sync.Mutex
	httpClient   *http.Client
}

func NewAgentEvalService(
	cfg *config.Config,
	repo interfaces.AgentEvalRepository,
	agentService interfaces.CustomAgentService,
	modelService interfaces.ModelService,
) interfaces.AgentEvalService {
	return &agentEvalService{
		cfg:          cfg,
		repo:         repo,
		agentService: agentService,
		modelService: modelService,
		httpClient:   &http.Client{Timeout: agentEvalHTTPTimeout},
	}
}

func (s *agentEvalService) TriggerRun(ctx context.Context, tenantID uint64, agentID string, creds types.AgentEvalCredentials) (*types.AgentEvalRun, error) {
	agent, err := s.agentService.GetAgentByIDAndTenant(ctx, agentID, tenantID)
	if err != nil {
		if errors.Is(err, ErrAgentNotFound) {
			return nil, ErrAgentEvalNotFound
		}
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	running, err := s.repo.FindRunningByAgent(ctx, tenantID, agentID)
	if err != nil {
		return nil, err
	}
	if running != nil {
		return nil, &AgentEvalConflictError{RunID: running.ID}
	}
	judgeModel, judgeModelError := s.resolveJudgeModel(ctx)
	now := time.Now()
	run := &types.AgentEvalRun{
		ID:        uuid.NewString(),
		TenantID:  tenantID,
		AgentID:   agentID,
		Status:    types.AgentEvalRunStatusRunning,
		Total:     len(agentEvalSmokeCases()),
		Trigger:   types.AgentEvalTriggerManual,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.CreateRun(ctx, run); err != nil {
		return nil, err
	}
	go s.executeRun(run, agent.Config.ModelID == "", creds, judgeModel, judgeModelError)
	return run, nil
}

func (s *agentEvalService) ListRuns(ctx context.Context, tenantID uint64, agentID string) ([]*types.AgentEvalRun, error) {
	return s.repo.ListRunsByAgent(ctx, tenantID, agentID, 0)
}

func (s *agentEvalService) GetRunDetail(ctx context.Context, tenantID uint64, agentID string, runID string) (*types.AgentEvalRun, []*types.AgentEvalCaseResult, error) {
	run, err := s.repo.GetRun(ctx, runID, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrAgentEvalRunNotFound) {
			return nil, nil, ErrAgentEvalNotFound
		}
		return nil, nil, err
	}
	if run.AgentID != agentID {
		return nil, nil, ErrAgentEvalNotFound
	}
	results, err := s.repo.ListCaseResultsByRun(ctx, runID, tenantID)
	if err != nil {
		return nil, nil, err
	}
	return run, results, nil
}

func (s *agentEvalService) DeleteRun(ctx context.Context, tenantID uint64, agentID string, runID string) error {
	run, err := s.repo.GetRun(ctx, runID, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrAgentEvalRunNotFound) {
			return ErrAgentEvalNotFound
		}
		return err
	}
	if run.AgentID != agentID {
		return ErrAgentEvalNotFound
	}
	return s.repo.DeleteRun(ctx, runID, tenantID)
}

func (s *agentEvalService) RecoverOrphanRuns(ctx context.Context) error {
	_, err := s.repo.RecoverOrphanRuns(ctx, "服务重启中断")
	return err
}

func (s *agentEvalService) resolveJudgeModel(ctx context.Context) (chat.Chat, error) {
	models, err := s.modelService.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	var fallback *types.Model
	for _, m := range models {
		if m.Type != types.ModelTypeKnowledgeQA {
			continue
		}
		if m.IsDefault {
			return s.modelService.GetChatModel(ctx, m.ID)
		}
		if fallback == nil {
			fallback = m
		}
	}
	if fallback == nil {
		return nil, errors.New("no knowledge QA model available")
	}
	return s.modelService.GetChatModel(ctx, fallback.ID)
}

func (s *agentEvalService) executeRun(run *types.AgentEvalRun, agentNoModel bool, creds types.AgentEvalCredentials, judgeModel chat.Chat, judgeModelError error) {
	ctx := context.Background()
	start := time.Now()
	fatalReason := ""
	for _, c := range agentEvalSmokeCases() {
		result, fatal := s.runCase(ctx, run, c, agentNoModel, creds, judgeModel, judgeModelError)
		if err := s.repo.CreateCaseResult(ctx, result); err != nil {
			logger.Errorf(ctx, "agent eval: create case result failed, run %s, case %s: %v", run.ID, c.ID, err)
		}
		if result.Status == types.AgentEvalCaseStatusPassed {
			run.Passed++
		} else {
			run.Failed++
		}
		if fatal {
			fatalReason = result.Error
			break
		}
	}
	now := time.Now()
	run.DurationMS = time.Since(start).Milliseconds()
	run.UpdatedAt = now
	run.FinishedAt = &now
	if fatalReason != "" {
		run.Status = types.AgentEvalRunStatusFailed
		run.Error = fatalReason
	} else {
		run.Status = types.AgentEvalRunStatusCompleted
	}
	if err := s.repo.UpdateRun(ctx, run); err != nil {
		logger.Errorf(ctx, "agent eval: update run failed, run %s: %v", run.ID, err)
	}
	if _, err := s.repo.PruneRunsByAgent(ctx, run.TenantID, run.AgentID, agentEvalRetentionLimit); err != nil {
		logger.Errorf(ctx, "agent eval: prune runs failed, run %s: %v", run.ID, err)
	}
}

func (s *agentEvalService) runCase(ctx context.Context, run *types.AgentEvalRun, c agentEvalSmokeCase, agentNoModel bool, creds types.AgentEvalCredentials, judgeModel chat.Chat, judgeModelError error) (*types.AgentEvalCaseResult, bool) {
	now := time.Now()
	result := &types.AgentEvalCaseResult{
		ID:        uuid.NewString(),
		RunID:     run.ID,
		TenantID:  run.TenantID,
		CaseID:    c.ID,
		CaseTitle: c.Title,
		Query:     c.Query,
		CreatedAt: now,
		UpdatedAt: now,
	}
	start := time.Now()
	defer func() {
		result.DurationMS = time.Since(start).Milliseconds()
		result.UpdatedAt = time.Now()
	}()

	if agentNoModel {
		result.Error = "智能体未配置模型"
		result.Assertions = runAgentEvalAssertions(c.Assertions, agentEvalObservation{})
		result.Status = types.AgentEvalCaseStatusFailed
		return result, false
	}

	sessionID, err := s.createEvalSession(ctx, creds)
	if err != nil {
		result.Error = "创建会话失败: " + err.Error()
		result.Assertions = runAgentEvalAssertions(c.Assertions, agentEvalObservation{})
		result.Status = types.AgentEvalCaseStatusFailed
		return result, true
	}

	collector, statusCode, streamErr := s.runAgentStream(ctx, run.AgentID, sessionID, c.Query, creds)
	obs := collector.Observation()
	result.Answer = truncateRunes(obs.Answer, agentEvalAnswerLimit)
	result.ReferenceCount = obs.ReferenceCount
	result.ToolCalls = collector.ToolCalls
	result.Assertions = runAgentEvalAssertions(c.Assertions, obs)

	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		result.Error = fmt.Sprintf("认证失败（HTTP %d），已中止剩余用例", statusCode)
		result.Status = types.AgentEvalCaseStatusFailed
		return result, true
	}
	if streamErr != nil {
		result.Error = streamErr.Error()
	} else if obs.ErrorMessage != "" {
		result.Error = obs.ErrorMessage
	}

	passed := allAgentEvalAssertionsPassed(result.Assertions)
	if passed && len(c.JudgeMetrics) > 0 {
		result.Judge = s.runJudge(ctx, c.JudgeMetrics, c.Query, result.Answer, obs.ReferenceCount, judgeModel, judgeModelError)
	}
	if passed {
		result.Status = types.AgentEvalCaseStatusPassed
	} else {
		result.Status = types.AgentEvalCaseStatusFailed
	}
	return result, false
}

func (s *agentEvalService) createEvalSession(ctx context.Context, creds types.AgentEvalCredentials) (string, error) {
	body, err := json.Marshal(map[string]string{"title": "agent-eval"})
	if err != nil {
		return "", err
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Second)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.evalBaseURL()+"/api/v1/sessions", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		applyEvalCredentials(req, creds)
		resp, err := s.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		sessionID, err := decodeEvalSessionResponse(resp)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return sessionID, nil
	}
	return "", lastErr
}

func decodeEvalSessionResponse(resp *http.Response) (string, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("create session http status %d", resp.StatusCode)
	}
	var payload struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode session response failed: %w", err)
	}
	if !payload.Success || payload.Data.ID == "" {
		return "", fmt.Errorf("create session failed: %s", payload.Message)
	}
	return payload.Data.ID, nil
}

func (s *agentEvalService) runAgentStream(ctx context.Context, agentID, sessionID, query string, creds types.AgentEvalCredentials) (*agentEvalStreamCollector, int, error) {
	collector := &agentEvalStreamCollector{}
	body, err := json.Marshal(map[string]interface{}{
		"query":         query,
		"disable_title": true,
		"channel":       "api",
		"agent_enabled": true,
		"agent_id":      agentID,
	})
	if err != nil {
		return collector, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.evalBaseURL()+"/api/v1/agent-chat/"+sessionID, bytes.NewReader(body))
	if err != nil {
		return collector, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	applyEvalCredentials(req, creds)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return collector, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return collector, resp.StatusCode, fmt.Errorf("agent chat http status %d", resp.StatusCode)
	}
	if err := consumeAgentEvalSSE(resp.Body, collector); err != nil {
		return collector, resp.StatusCode, err
	}
	return collector, resp.StatusCode, nil
}

func (s *agentEvalService) runJudge(ctx context.Context, metrics []string, query, answer string, referenceCount int, judgeModel chat.Chat, judgeModelError error) types.AgentEvalJudgeList {
	items := make(types.AgentEvalJudgeList, 0, len(metrics))
	for _, metric := range metrics {
		item := types.AgentEvalJudgeItem{Metric: metric}
		if judgeModel == nil {
			if judgeModelError == nil {
				judgeModelError = errors.New("judge model unavailable")
			}
			item.Error = judgeModelError.Error()
			items = append(items, item)
			continue
		}
		resp, err := judgeModel.Chat(ctx, []chat.Message{{Role: "user", Content: agentEvalJudgePrompt(metric, query, answer, referenceCount)}}, &chat.ChatOptions{})
		if err != nil {
			item.Error = err.Error()
			items = append(items, item)
			continue
		}
		score, reason, err := parseJudgeOutput(resp.Content)
		if err != nil {
			item.Error = err.Error()
			items = append(items, item)
			continue
		}
		item.Score = score
		item.Reason = reason
		items = append(items, item)
	}
	return items
}

func agentEvalJudgePrompt(metric, query, answer string, referenceCount int) string {
	var criteria string
	switch metric {
	case "relevancy":
		criteria = "回答是否切题、且与检索内容一致"
	case "honesty":
		criteria = "面对无答案问题，是否诚实说明信息不足，而非编造事实"
	default:
		criteria = "回答质量是否良好"
	}
	return fmt.Sprintf(
		"你是质量评估助手，请根据评分标准对回答评分。\n评分标准：%s\n问题：%s\n检索引用条数：%d\n回答：%s\n只输出 JSON 对象：{\"score\": 0到1之间的小数, \"reason\": \"一句话理由\"}",
		criteria, query, referenceCount, answer,
	)
}

func (s *agentEvalService) evalBaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.cfg.Server.Port)
}

func applyEvalCredentials(req *http.Request, creds types.AgentEvalCredentials) {
	if creds.Authorization != "" {
		req.Header.Set("Authorization", creds.Authorization)
	}
	if creds.XAPIKey != "" {
		req.Header.Set("X-API-Key", creds.XAPIKey)
	}
	if creds.XTenantID != "" {
		req.Header.Set("X-Tenant-ID", creds.XTenantID)
	}
}
