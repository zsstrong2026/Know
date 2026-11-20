package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/providers"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"gorm.io/gorm"
)

// ErrModelNotFound is returned when a model cannot be found in the repository
var ErrModelNotFound = errors.New("model not found")

// modelService implements the model service interface
type modelService struct {
	repo          interfaces.ModelRepository
	kbRepo        interfaces.KnowledgeBaseRepository
	agentRepo     interfaces.CustomAgentRepository
	tenantRepo    interfaces.TenantRepository
	sessionRepo   interfaces.SessionRepository
	ollamaService *ollama.OllamaService
	pooler        embedding.EmbedderPooler
	tenantService interfaces.TenantService
	// db 用于“改写引用 + 删除模型”的跨表事务。
	db *gorm.DB
}

// NewModelService creates a new model service instance
func NewModelService(repo interfaces.ModelRepository,
	kbRepo interfaces.KnowledgeBaseRepository,
	agentRepo interfaces.CustomAgentRepository,
	tenantRepo interfaces.TenantRepository,
	ollamaService *ollama.OllamaService,
	pooler embedding.EmbedderPooler,
	tenantService interfaces.TenantService,
	db *gorm.DB,
	sessionRepo interfaces.SessionRepository,
) interfaces.ModelService {
	return &modelService{
		repo:          repo,
		kbRepo:        kbRepo,
		agentRepo:     agentRepo,
		tenantRepo:    tenantRepo,
		sessionRepo:   sessionRepo,
		ollamaService: ollamaService,
		pooler:        pooler,
		tenantService: tenantService,
		db:            db,
	}
}

// decryptAppSecret 解密 AppSecret（如果为空或 cryptoSvc 为空则原样返回）
func (s *modelService) decryptAppSecret(encrypted string) string {
	if encrypted == "" {
		return encrypted
	}
	if key := utils.GetAESKey(); key != nil {
		if encrypted, err := utils.DecryptAESGCM(encrypted, key); err == nil {
			return encrypted
		}
	}
	return encrypted
}

// resolveWeKnoraCloudCredentials 为 WeKnoraCloud 厂商模型补全 AppID/AppSecret。
// 当模型自身参数中未存储凭证时，自动从空间配置中获取（SaveCredentials 保存的凭证）。
func (s *modelService) resolveWeKnoraCloudCredentials(ctx context.Context, params *types.ModelParameters) (appID, appSecret string) {
	appID = params.AppID
	appSecret = s.decryptAppSecret(params.AppSecret)

	if params.Provider != providers.WeKnoraCloudID {
		return
	}
	if appID != "" && appSecret != "" {
		return
	}

	if s.tenantService == nil {
		return
	}
	creds := s.tenantService.GetWeKnoraCloudCredentials(ctx)
	if creds == nil {
		return
	}
	if appID == "" {
		appID = creds.AppID
	}
	if appSecret == "" {
		appSecret = creds.AppSecret
	}
	return
}

// CreateModel creates a new model in the repository
// For local models, it initiates an asynchronous download process
// Remote models are immediately set to active status
func (s *modelService) CreateModel(ctx context.Context, model *types.Model) error {
	logger.Infof(ctx, "Creating model: %s, type: %s, source: %s", model.Name, model.Type, model.Source)

	// Handle remote models (e.g., OpenAI, Azure)
	if model.Source == types.ModelSourceRemote {
		logger.Info(ctx, "Remote model detected, setting status to active")
		model.Status = types.ModelStatusActive

		logger.Info(ctx, "Saving remote model to repository")
		err := s.repo.Create(ctx, model)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"model_name": model.Name,
				"model_type": model.Type,
			})
			return err
		}

		logger.Infof(ctx, "Remote model created successfully: %s", model.ID)
		return nil
	}

	// Handle local models (e.g., Ollama)
	logger.Info(ctx, "Local model detected, setting status to downloading")
	model.Status = types.ModelStatusDownloading

	logger.Info(ctx, "Saving local model to repository")
	err := s.repo.Create(ctx, model)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_name": model.Name,
			"model_type": model.Type,
		})
		return err
	}

	s.downloadModelAsync(ctx, model)

	logger.Infof(ctx, "Model creation initiated successfully: %s", model.ID)
	return nil
}

// CopyModel clones a tenant-owned model. The stored name stays the upstream
// model identifier. displayName is the label chosen by the caller. Credentials
// are copied from the source row because list responses never include them.
// An active local model is already present, so the copy does not download it
// again. A local model that is still downloading or failed starts a pull.
func (s *modelService) CopyModel(ctx context.Context, sourceID, displayName string) (*types.Model, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil, apperrors.NewBadRequestError("model ID cannot be empty")
	}
	displayName = strings.TrimSpace(utils.SanitizeForLog(displayName))
	if displayName == "" {
		return nil, apperrors.NewBadRequestError("display name is required")
	}
	if utf8.RuneCountInString(displayName) > types.ModelDisplayNameMaxLen {
		return nil, apperrors.NewBadRequestError("display name is too long")
	}

	tenantID := types.MustTenantIDFromContext(ctx)
	source, err := s.repo.GetByID(ctx, tenantID, sourceID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id": sourceID,
		})
		return nil, err
	}
	if source == nil {
		return nil, ErrModelNotFound
	}
	if source.IsBuiltin {
		return nil, apperrors.NewBadRequestError("builtin models cannot be copied")
	}

	params, err := cloneModelParameters(source.Parameters)
	if err != nil {
		return nil, err
	}
	model := &types.Model{
		TenantID:    tenantID,
		Name:        source.Name,
		DisplayName: displayName,
		Type:        source.Type,
		Source:      source.Source,
		Description: source.Description,
		Parameters:  params,
	}
	logger.Infof(ctx, "Copying model %s as %q", source.ID, displayName)

	// Remote rows, and local rows that already finished downloading, are
	// configuration copies. Only a local row that is not active needs a pull.
	if model.Source != types.ModelSourceLocal || source.Status == types.ModelStatusActive {
		model.Status = types.ModelStatusActive
		if err := s.repo.Create(ctx, model); err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"model_id":   source.ID,
				"model_name": model.Name,
			})
			return nil, err
		}
		return model, nil
	}

	model.Status = types.ModelStatusDownloading
	if err := s.repo.Create(ctx, model); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   source.ID,
			"model_name": model.Name,
		})
		return nil, err
	}
	s.downloadModelAsync(ctx, model)
	return model, nil
}

func (s *modelService) downloadModelAsync(ctx context.Context, model *types.Model) {
	logger.Infof(ctx, "Starting background download for model: %s", model.Name)
	newCtx := logger.CloneContext(ctx)
	go func() {
		logger.Info(newCtx, "Background download started")
		err := s.ollamaService.PullModel(newCtx, model.Name)
		if err != nil {
			logger.ErrorWithFields(newCtx, err, map[string]interface{}{
				"model_name": model.Name,
			})
			model.Status = types.ModelStatusDownloadFailed
		} else {
			logger.Infof(newCtx, "Model download completed successfully: %s", model.Name)
			model.Status = types.ModelStatusActive
		}
		logger.Infof(newCtx, "Updating model status to: %s", model.Status)
		s.repo.Update(newCtx, model)
	}()
}

func cloneModelParameters(params types.ModelParameters) (types.ModelParameters, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return types.ModelParameters{}, err
	}
	var cloned types.ModelParameters
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return types.ModelParameters{}, err
	}
	return cloned, nil
}

// GetModelByID retrieves a model by its ID
// Returns an error if the model is not found or is in a non-active state
func (s *modelService) GetModelByID(ctx context.Context, id string) (*types.Model, error) {
	// Check if ID is empty
	if id == "" {
		logger.Error(ctx, "Model ID is empty")
		return nil, errors.New("model ID cannot be empty")
	}

	tenantID := types.MustTenantIDFromContext(ctx)

	// Fetch model from repository
	model, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":  id,
			"tenant_id": tenantID,
		})
		return nil, err
	}

	// Check if model exists
	if model == nil {
		logger.Error(ctx, "Model not found")
		return nil, ErrModelNotFound
	}

	logger.Infof(ctx, "Model found, name: %s, status: %s", model.Name, model.Status)

	// Check model status
	if model.Status == types.ModelStatusActive {
		return model, nil
	}

	if model.Status == types.ModelStatusDownloading {
		logger.Warn(ctx, "Model is currently downloading")
		return nil, errors.New("model is currently downloading")
	}

	if model.Status == types.ModelStatusDownloadFailed {
		logger.Error(ctx, "Model download failed")
		return nil, errors.New("model download failed")
	}

	logger.Error(ctx, "Model status is abnormal")
	return nil, errors.New("abnormal model status")
}

// ListModels returns all models belonging to the tenant
func (s *modelService) ListModels(ctx context.Context) ([]*types.Model, error) {
	logger.Info(ctx, "Start listing models")

	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Listing models for tenant ID: %d", tenantID)

	// List models from repository with no additional filters
	models, err := s.repo.List(ctx, tenantID, "", "")
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
		})
		return nil, err
	}
	// GetModelByID is left alone: its result is written back by UpdateModel,
	// and this overlay must never be persisted.
	if settings := s.workspaceDefaultSettings(ctx, tenantID); settings != nil {
		settings.ApplyModelDefaults(models)
	}

	logger.Infof(ctx, "Retrieved %d models successfully", len(models))
	return models, nil
}

// workspaceDefaultSettings prefers the tenant the request already loaded and
// reads it from storage for background contexts that carry only the ID.
func (s *modelService) workspaceDefaultSettings(ctx context.Context, tenantID uint64) *types.DefaultSettings {
	if tenant, ok := types.TenantInfoFromContext(ctx); ok && tenant.ID == tenantID {
		return tenant.DefaultSettings
	}
	if s.tenantService == nil {
		return nil
	}
	tenant, err := s.tenantService.GetTenantByID(ctx, tenantID)
	if err != nil || tenant == nil {
		return nil
	}
	return tenant.DefaultSettings
}

// UpdateModel updates an existing model in the repository
func (s *modelService) UpdateModel(ctx context.Context, model *types.Model) error {
	logger.Info(ctx, "Start updating model")
	logger.Infof(ctx, "Updating model ID: %s, name: %s", model.ID, model.Name)

	// Built-in models are platform-wide. Tenant administrators may view them,
	// but only a system administrator may change their shared configuration.
	tenantID := types.MustTenantIDFromContext(ctx)
	existingModel, err := s.repo.GetByID(ctx, tenantID, model.ID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id": model.ID,
		})
		return err
	}
	if existingModel != nil && existingModel.IsBuiltin {
		if !types.IsSystemAdminFromContext(ctx) {
			logger.Warnf(ctx, "Non-system-admin attempted to update builtin model: %s", model.ID)
			return apperrors.NewForbiddenError("only system administrators can update builtin models")
		}
		// A UI edit is an explicit runtime override. Clear YAML ownership so
		// the startup reconciler does not silently replace the saved values.
		model.TenantID = existingModel.TenantID
		model.IsBuiltin = true
		model.ManagedBy = ""
	}

	// Update model in repository
	err = s.repo.Update(ctx, model)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   model.ID,
			"model_name": model.Name,
		})
		return err
	}

	logger.Infof(ctx, "Model updated successfully: %s", model.ID)
	return nil
}

// UpdateModelCredentials writes one or more credential fields on the model's
// Parameters jsonb. Models are not pooled per-instance the way MCP clients
// are (each call to GetEmbeddingModel/GetChatModel rebuilds the client from
// the current Parameters), so no explicit cache invalidation is required —
// the next call will pick up the new credential automatically.
func (s *modelService) UpdateModelCredentials(
	ctx context.Context, id string, apiKey, appSecret *string,
) (*types.Model, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	existing, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrModelNotFound
	}
	if existing.IsBuiltin && !types.IsSystemAdminFromContext(ctx) {
		return nil, apperrors.NewForbiddenError(
			"only system administrators can modify builtin model credentials")
	}

	changed := false
	if apiKey != nil && *apiKey != "" && *apiKey != existing.Parameters.APIKey {
		existing.Parameters.APIKey = *apiKey
		changed = true
	}
	if appSecret != nil && *appSecret != "" && *appSecret != existing.Parameters.AppSecret {
		existing.Parameters.AppSecret = *appSecret
		changed = true
	}
	if !changed {
		return existing, nil
	}
	if existing.IsBuiltin {
		// Credential changes are also runtime overrides of YAML-managed data.
		existing.ManagedBy = ""
	}
	if err := s.repo.Update(ctx, existing); err != nil {
		return nil, err
	}
	logger.Infof(ctx, "Model credentials updated: id=%s", id)
	return existing, nil
}

// ClearModelCredential removes a single credential field. Idempotent.
func (s *modelService) ClearModelCredential(ctx context.Context, id, field string) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	existing, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrModelNotFound
	}
	if existing.IsBuiltin && !types.IsSystemAdminFromContext(ctx) {
		return apperrors.NewForbiddenError(
			"only system administrators can modify builtin model credentials")
	}

	changed := false
	switch field {
	case "api_key":
		if existing.Parameters.APIKey != "" {
			existing.Parameters.APIKey = ""
			changed = true
		}
	case "app_secret":
		if existing.Parameters.AppSecret != "" {
			existing.Parameters.AppSecret = ""
			changed = true
		}
	default:
		return errors.New("unknown credential field: " + field)
	}
	if !changed {
		return nil
	}
	if existing.IsBuiltin {
		existing.ManagedBy = ""
	}
	if err := s.repo.Update(ctx, existing); err != nil {
		return err
	}
	logger.Infof(ctx, "Model credential cleared by user: id=%s field=%s", id, field)
	return nil
}

// DeleteModel removes a model from the repository
func (s *modelService) DeleteModel(ctx context.Context, id string) error {
	logger.Info(ctx, "Start deleting model")
	logger.Infof(ctx, "Deleting model ID: %s", id)

	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Tenant ID: %d", tenantID)

	// Check if the model is builtin - builtin models cannot be deleted
	existingModel, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id": id,
		})
		return err
	}
	if existingModel == nil {
		return ErrModelNotFound
	}
	if existingModel.IsBuiltin {
		logger.Warnf(ctx, "Attempted to delete builtin model: %s", id)
		return apperrors.NewBadRequestError("builtin models cannot be deleted")
	}

	usage, err := s.getModelUsageDetails(ctx, tenantID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id": id,
		})
		return err
	}
	if usage.InUse() {
		kbCount := usage.KnowledgeBaseTotal
		if kbCount == 0 {
			kbCount = int64(len(usage.KnowledgeBases))
		}
		agentCount := usage.AgentTotal
		if agentCount == 0 {
			agentCount = int64(len(usage.Agents))
		}
		memoryInUse := len(usage.LongTermMemory.Bindings) > 0
		logger.Warnf(ctx, "Model %s is in use: kb=%d agent=%d doc=%d memory=%t chatHistory=%t",
			id, kbCount, agentCount, usage.DocumentTotal, memoryInUse, usage.ChatHistoryEmbedding)
		return apperrors.NewModelInUseError(
			formatModelInUseMessage(
				kbCount, agentCount, usage.DocumentTotal, memoryInUse, usage.ChatHistoryEmbedding,
			),
			usage,
		)
	}

	// Delete model from repository
	err = s.repo.Delete(ctx, tenantID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":  id,
			"tenant_id": tenantID,
		})
		return err
	}

	// 会话里的输入栏记忆不阻塞删除，但删除后必须清理，避免前端恢复出悬空模型。
	s.reassignSessionModelMemory(ctx, tenantID, id)

	logger.Infof(ctx, "Model deleted successfully: %s", id)
	return nil
}

func (s *modelService) getModelUsageDetails(
	ctx context.Context, tenantID uint64, modelID string,
) (types.ModelUsageDetails, error) {
	details := types.ModelUsageDetails{
		KnowledgeBases: make([]types.ModelUsageResource, 0),
		Agents:         make([]types.ModelUsageResource, 0),
		LongTermMemory: types.ModelUsageMemory{Bindings: make([]types.ModelUsageBinding, 0)},
	}

	kbCount, err := s.kbRepo.CountByModelID(ctx, tenantID, modelID)
	if err != nil {
		return details, err
	}
	details.KnowledgeBaseTotal = kbCount
	if kbCount > 0 {
		details.KnowledgeBases, err = s.kbRepo.ListModelUsages(ctx, tenantID, modelID)
		if err != nil {
			return details, err
		}
		if details.KnowledgeBases == nil {
			details.KnowledgeBases = make([]types.ModelUsageResource, 0)
		}
	}

	agentCount, err := s.agentRepo.CountByModelID(ctx, tenantID, modelID)
	if err != nil {
		return details, err
	}
	details.AgentTotal = agentCount
	if agentCount > 0 {
		details.Agents, err = s.agentRepo.ListModelUsages(ctx, tenantID, modelID)
		if err != nil {
			return details, err
		}
		if details.Agents == nil {
			details.Agents = make([]types.ModelUsageResource, 0)
		}
	}

	// 文档级向量引用无法迁移，必须计入删除守卫。
	docCount, err := s.kbRepo.CountKnowledgeDocumentsByEmbeddingModel(ctx, nil, tenantID, modelID)
	if err != nil {
		return details, err
	}
	details.DocumentTotal = docCount

	if s.tenantService == nil {
		return details, nil
	}
	tenant, err := s.tenantService.GetTenantByID(ctx, tenantID)
	if err != nil {
		return details, err
	}
	if tenant == nil {
		return details, nil
	}

	// 聊天历史索引一旦建立索引就不能更换 Embedding 模型。
	if cfg := tenant.ChatHistoryConfig; cfg != nil &&
		cfg.Enabled && strings.TrimSpace(cfg.EmbeddingModelID) == modelID {
		details.ChatHistoryEmbedding = true
	}
	if tenant.MemoryConfig == nil {
		return details, nil
	}

	// Both memory model pins have to be checked. Deleting either one leaves
	// the workspace pointing at a model that no longer exists.
	if strings.TrimSpace(tenant.MemoryConfig.EmbeddingModelID) == modelID {
		details.LongTermMemory.Bindings = append(
			details.LongTermMemory.Bindings,
			types.ModelUsageBindingEmbeddingModel,
		)
	}
	if strings.TrimSpace(tenant.MemoryConfig.ExtractModelID) == modelID {
		details.LongTermMemory.Bindings = append(
			details.LongTermMemory.Bindings,
			types.ModelUsageBindingExtractModel,
		)
	}
	return details, nil
}

// modelReassignmentBindingOrder 固定改写项的返回顺序，便于前端与测试断言。
var modelReassignmentBindingOrder = []types.ModelUsageBinding{
	types.ModelUsageBindingEmbeddingModel,
	types.ModelUsageBindingSummaryModel,
	types.ModelUsageBindingImageProcessingModel,
	types.ModelUsageBindingVLMModel,
	types.ModelUsageBindingASRModel,
	types.ModelUsageBindingWikiSynthesisModel,
	types.ModelUsageBindingAutoTagModel,
	types.ModelUsageBindingChatModel,
	types.ModelUsageBindingRerankModel,
	types.ModelUsageBindingQueryUnderstandModel,
	types.ModelUsageBindingFollowUpModel,
	types.ModelUsageBindingExtractModel,
}

// GetModelReassignmentPlan 计算删除模型前，各引用可改写到的默认模型。
func (s *modelService) GetModelReassignmentPlan(
	ctx context.Context, id string,
) (*types.ModelReassignmentPlan, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	model, err := s.loadDeletableModel(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}

	kbCounts, err := s.kbRepo.CountModelBindings(ctx, nil, tenantID, id)
	if err != nil {
		return nil, err
	}
	agentCounts, err := s.agentRepo.CountModelBindings(ctx, nil, tenantID, id)
	if err != nil {
		return nil, err
	}
	kbTotal, err := s.kbRepo.CountByModelID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	agentTotal, err := s.agentRepo.CountByModelID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	memoryBindings := s.memoryModelBindings(ctx, tenantID, id)
	sessionCount, err := s.countSessionModelBindings(ctx, nil, tenantID, id)
	if err != nil {
		return nil, err
	}

	plan := &types.ModelReassignmentPlan{
		ModelID:            id,
		ModelName:          model.Name,
		CanDelete:          true,
		Items:              make([]types.ModelReassignmentItem, 0),
		Blockers:           make([]types.ModelReassignmentBlocker, 0),
		KnowledgeBaseTotal: kbTotal,
		AgentTotal:         agentTotal,
		LongTermMemory:     types.ModelUsageMemory{Bindings: memoryBindings},
		SessionModelCount:  sessionCount,
	}

	// 知识库已有文档时，Embedding 模型不能替换。
	if kbCounts[types.ModelUsageBindingEmbeddingModel] > 0 {
		count, err := s.kbRepo.CountKnowledgeBasesWithDocumentsByEmbeddingModel(ctx, nil, tenantID, id)
		if err != nil {
			return nil, err
		}
		if count > 0 {
			plan.Blockers = append(plan.Blockers, types.ModelReassignmentBlocker{
				Code:               types.ModelReassignmentBlockerEmbeddingHasDocuments,
				Binding:            types.ModelUsageBindingEmbeddingModel,
				ModelType:          types.ModelTypeEmbedding,
				KnowledgeBaseCount: count,
			})
		}
	}
	// 文档级向量引用（例如知识库已换模型但旧文档仍指向该模型）同样不可迁移。
	docCount, err := s.kbRepo.CountKnowledgeDocumentsByEmbeddingModel(ctx, nil, tenantID, id)
	if err != nil {
		return nil, err
	}
	if docCount > 0 {
		plan.Blockers = append(plan.Blockers, types.ModelReassignmentBlocker{
			Code:          types.ModelReassignmentBlockerEmbeddingInDocuments,
			Binding:       types.ModelUsageBindingEmbeddingModel,
			ModelType:     types.ModelTypeEmbedding,
			DocumentCount: docCount,
		})
	}
	// 聊天历史索引已使用该 Embedding 模型时不能替换。
	if s.chatHistoryUsesModel(ctx, tenantID, id) {
		plan.Blockers = append(plan.Blockers, types.ModelReassignmentBlocker{
			Code:      types.ModelReassignmentBlockerChatHistoryEmbedding,
			Binding:   types.ModelUsageBindingEmbeddingModel,
			ModelType: types.ModelTypeEmbedding,
		})
	}

	models, err := s.repo.List(ctx, tenantID, "", "")
	if err != nil {
		return nil, err
	}
	settings := s.workspaceDefaultSettings(ctx, tenantID)

	// 会话记忆改写为默认对话模型；没有可用默认时留空表示清空选择。
	if sessionCount > 0 {
		if target, _ := resolveReassignmentTarget(
			models, settings, types.ModelUsageBindingChatModel, id,
		); target != nil {
			plan.SessionTargetModelID = target.ID
			plan.SessionTargetModelName = target.Name
		}
	}

	memorySet := make(map[types.ModelUsageBinding]bool, len(memoryBindings))
	for _, binding := range memoryBindings {
		memorySet[binding] = true
	}

	for _, binding := range modelReassignmentBindingOrder {
		kbCount := kbCounts[binding]
		agentCount := agentCounts[binding]
		inMemory := memorySet[binding]
		if kbCount == 0 && agentCount == 0 && !inMemory {
			continue
		}
		modelType, ok := types.ModelUsageBindingModelType(binding)
		if !ok {
			continue
		}
		target, source := resolveReassignmentTarget(models, settings, binding, id)
		if target == nil {
			plan.Blockers = append(plan.Blockers, types.ModelReassignmentBlocker{
				Code:      types.ModelReassignmentBlockerNoDefaultModel,
				Binding:   binding,
				ModelType: modelType,
			})
			continue
		}
		plan.Items = append(plan.Items, types.ModelReassignmentItem{
			Binding:            binding,
			ModelType:          modelType,
			TargetModelID:      target.ID,
			TargetModelName:    target.Name,
			TargetSource:       source,
			KnowledgeBaseCount: kbCount,
			AgentCount:         agentCount,
			LongTermMemory:     inMemory,
		})
	}

	plan.CanDelete = len(plan.Blockers) == 0
	return plan, nil
}

// loadDeletableModel 读取模型并拒绝内置模型。
func (s *modelService) loadDeletableModel(
	ctx context.Context, tenantID uint64, id string,
) (*types.Model, error) {
	model, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if model == nil {
		return nil, ErrModelNotFound
	}
	if model.IsBuiltin {
		return nil, apperrors.NewBadRequestError("builtin models cannot be deleted")
	}
	return model, nil
}

// memoryModelBindings 返回长期记忆中引用该模型的绑定。
func (s *modelService) memoryModelBindings(
	ctx context.Context, tenantID uint64, modelID string,
) []types.ModelUsageBinding {
	bindings := make([]types.ModelUsageBinding, 0)
	if s.tenantService == nil {
		return bindings
	}
	tenant, err := s.tenantService.GetTenantByID(ctx, tenantID)
	if err != nil || tenant == nil || tenant.MemoryConfig == nil {
		return bindings
	}
	if strings.TrimSpace(tenant.MemoryConfig.EmbeddingModelID) == modelID {
		bindings = append(bindings, types.ModelUsageBindingEmbeddingModel)
	}
	if strings.TrimSpace(tenant.MemoryConfig.ExtractModelID) == modelID {
		bindings = append(bindings, types.ModelUsageBindingExtractModel)
	}
	return bindings
}

// reassignSessionModelMemory 清理删除模型后会话输入栏里的悬空引用。
// 只影响 UI 记忆，失败仅记录日志，不影响删除结果。
func (s *modelService) reassignSessionModelMemory(
	ctx context.Context, tenantID uint64, modelID string,
) {
	if s.sessionRepo == nil || s.db == nil {
		return
	}
	targetID := ""
	if models, err := s.repo.List(ctx, tenantID, "", ""); err == nil {
		if target, _ := resolveReassignmentTarget(
			models, s.workspaceDefaultSettings(ctx, tenantID),
			types.ModelUsageBindingChatModel, modelID,
		); target != nil {
			targetID = target.ID
		}
	}
	if err := s.sessionRepo.ReassignLastRequestModel(
		ctx, s.db, tenantID, modelID, targetID,
	); err != nil {
		logger.Warnf(ctx, "failed to clear session model memory for %s: %v", modelID, err)
	}
}

// countSessionModelBindings 统计把该模型作为“上次输入栏选择”的会话数。
func (s *modelService) countSessionModelBindings(
	ctx context.Context, db *gorm.DB, tenantID uint64, modelID string,
) (int64, error) {
	if s.sessionRepo == nil {
		return 0, nil
	}
	return s.sessionRepo.CountSessionsByLastRequestModel(ctx, db, tenantID, modelID)
}

// chatHistoryUsesModel 判断工作空间的聊天历史索引是否绑定该 Embedding 模型。
func (s *modelService) chatHistoryUsesModel(
	ctx context.Context, tenantID uint64, modelID string,
) bool {
	cfg := s.tenantChatHistoryConfig(ctx, tenantID)
	return cfg != nil && cfg.Enabled && strings.TrimSpace(cfg.EmbeddingModelID) == modelID
}

// tenantChatHistoryConfig 读取租户聊天历史配置；读取失败按未配置处理。
func (s *modelService) tenantChatHistoryConfig(
	ctx context.Context, tenantID uint64,
) *types.ChatHistoryConfig {
	if s.tenantService == nil {
		return nil
	}
	tenant, err := s.tenantService.GetTenantByID(ctx, tenantID)
	if err != nil || tenant == nil {
		return nil
	}
	return tenant.ChatHistoryConfig
}

// appendBindingOnce 追加去重后的绑定，保持调用方顺序稳定。
func appendBindingOnce(
	bindings []types.ModelUsageBinding, binding types.ModelUsageBinding,
) []types.ModelUsageBinding {
	for _, existing := range bindings {
		if existing == binding {
			return bindings
		}
	}
	return append(bindings, binding)
}

// resolveReassignmentTarget 按“工作空间默认 → 部署默认”的顺序解析目标模型。
// 目标必须 active、支持该类型，且不能是被删除的模型本身。
func resolveReassignmentTarget(
	models []*types.Model, settings *types.DefaultSettings,
	binding types.ModelUsageBinding, deletedID string,
) (*types.Model, string) {
	modelType, ok := types.ModelUsageBindingModelType(binding)
	if !ok {
		return nil, ""
	}
	for _, candidateID := range workspaceTargetIDs(settings, binding) {
		if candidateID == "" || candidateID == deletedID {
			continue
		}
		for _, model := range models {
			if model != nil && model.ID == candidateID && usableReassignmentTarget(model, modelType) {
				return model, types.ModelReassignmentTargetWorkspaceDefault
			}
		}
	}
	for _, model := range models {
		if model == nil || model.ID == deletedID || !model.IsDefault {
			continue
		}
		if usableReassignmentTarget(model, modelType) {
			return model, types.ModelReassignmentTargetDeploymentDefault
		}
	}
	return nil, ""
}

// workspaceTargetIDs 返回该绑定的工作空间默认槽位候选（按优先级）。
func workspaceTargetIDs(
	settings *types.DefaultSettings, binding types.ModelUsageBinding,
) []string {
	if settings == nil {
		return nil
	}
	switch binding {
	case types.ModelUsageBindingEmbeddingModel:
		return []string{strings.TrimSpace(settings.EmbeddingModelID)}
	case types.ModelUsageBindingChatModel:
		return []string{strings.TrimSpace(settings.ChatModelID)}
	case types.ModelUsageBindingSummaryModel, types.ModelUsageBindingWikiSynthesisModel,
		types.ModelUsageBindingAutoTagModel, types.ModelUsageBindingQueryUnderstandModel,
		types.ModelUsageBindingFollowUpModel, types.ModelUsageBindingExtractModel:
		// 后台轻量任务优先使用 Utility 模型，未配置时回落到对话默认。
		return []string{
			strings.TrimSpace(settings.UtilityModelID),
			strings.TrimSpace(settings.ChatModelID),
		}
	case types.ModelUsageBindingRerankModel:
		return []string{strings.TrimSpace(settings.RerankModelID)}
	case types.ModelUsageBindingVLMModel, types.ModelUsageBindingImageProcessingModel:
		return []string{strings.TrimSpace(settings.VLMModelID)}
	case types.ModelUsageBindingASRModel:
		return []string{strings.TrimSpace(settings.ASRModelID)}
	default:
		return nil
	}
}

func usableReassignmentTarget(model *types.Model, modelType types.ModelType) bool {
	return model.Status == types.ModelStatusActive && model.SupportsType(modelType)
}

// DeleteModelWithReassignment 在一个事务内改写引用、清空默认槽位并删除模型。
func (s *modelService) DeleteModelWithReassignment(
	ctx context.Context, id string,
) (*types.ModelReassignmentPlan, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	plan, err := s.GetModelReassignmentPlan(ctx, id)
	if err != nil {
		return nil, err
	}
	if !plan.CanDelete {
		return nil, apperrors.NewBadRequestError(
			formatModelReassignmentBlockMessage(plan),
		).WithDetails(plan)
	}
	if s.db == nil {
		return nil, apperrors.NewInternalServerError("database is not configured")
	}

	targets := make(map[types.ModelUsageBinding]string, len(plan.Items))
	for _, item := range plan.Items {
		targets[item.Binding] = item.TargetModelID
	}
	memoryBindings := plan.LongTermMemory.Bindings

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 事务内重新确认引用仍然存在，避免 plan 过期后漏改。
		kbCounts, err := s.kbRepo.CountModelBindings(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		agentCounts, err := s.agentRepo.CountModelBindings(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		docCount, err := s.kbRepo.CountKnowledgeBasesWithDocumentsByEmbeddingModel(
			ctx, tx, tenantID, id,
		)
		if err != nil {
			return err
		}
		docModelCount, err := s.kbRepo.CountKnowledgeDocumentsByEmbeddingModel(
			ctx, tx, tenantID, id,
		)
		if err != nil {
			return err
		}
		sessionCount, err := s.countSessionModelBindings(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		if docCount > 0 || docModelCount > 0 {
			blocked := *plan
			blocked.CanDelete = false
			blocked.Blockers = append([]types.ModelReassignmentBlocker{}, plan.Blockers...)
			if docCount > 0 {
				blocked.Blockers = append(blocked.Blockers, types.ModelReassignmentBlocker{
					Code:               types.ModelReassignmentBlockerEmbeddingHasDocuments,
					Binding:            types.ModelUsageBindingEmbeddingModel,
					ModelType:          types.ModelTypeEmbedding,
					KnowledgeBaseCount: docCount,
				})
			}
			if docModelCount > 0 {
				blocked.Blockers = append(blocked.Blockers, types.ModelReassignmentBlocker{
					Code:          types.ModelReassignmentBlockerEmbeddingInDocuments,
					Binding:       types.ModelUsageBindingEmbeddingModel,
					ModelType:     types.ModelTypeEmbedding,
					DocumentCount: docModelCount,
				})
			}
			return apperrors.NewBadRequestError(
				formatModelReassignmentBlockMessage(&blocked),
			).WithDetails(&blocked)
		}

		// 事务内复查长期记忆与聊天历史，避免缓存过期导致漏改写/漏阻塞。
		var tenantRow types.Tenant
		if err := tx.WithContext(ctx).
			Select("memory_config", "chat_history_config").
			Where("id = ?", tenantID).
			First(&tenantRow).Error; err != nil {
			return err
		}
		if cfg := tenantRow.ChatHistoryConfig; cfg != nil &&
			cfg.Enabled && strings.TrimSpace(cfg.EmbeddingModelID) == id {
			blocked := *plan
			blocked.CanDelete = false
			blocked.Blockers = append(
				append([]types.ModelReassignmentBlocker{}, plan.Blockers...),
				types.ModelReassignmentBlocker{
					Code:      types.ModelReassignmentBlockerChatHistoryEmbedding,
					Binding:   types.ModelUsageBindingEmbeddingModel,
					ModelType: types.ModelTypeEmbedding,
				},
			)
			return apperrors.NewBadRequestError(
				formatModelReassignmentBlockMessage(&blocked),
			).WithDetails(&blocked)
		}
		if tenantRow.MemoryConfig != nil {
			if strings.TrimSpace(tenantRow.MemoryConfig.EmbeddingModelID) == id {
				if _, ok := targets[types.ModelUsageBindingEmbeddingModel]; !ok {
					return apperrors.NewBadRequestError(
						"model references changed during deletion; please retry",
					).WithDetails(plan)
				}
				memoryBindings = appendBindingOnce(
					memoryBindings, types.ModelUsageBindingEmbeddingModel,
				)
			}
			if strings.TrimSpace(tenantRow.MemoryConfig.ExtractModelID) == id {
				if _, ok := targets[types.ModelUsageBindingExtractModel]; !ok {
					return apperrors.NewBadRequestError(
						"model references changed during deletion; please retry",
					).WithDetails(plan)
				}
				memoryBindings = appendBindingOnce(
					memoryBindings, types.ModelUsageBindingExtractModel,
				)
			}
		}

		for binding, targetID := range targets {
			modelType, ok := types.ModelUsageBindingModelType(binding)
			if !ok {
				continue
			}
			var target types.Model
			if err := tx.WithContext(ctx).
				Where("id = ?", targetID).
				Where("(tenant_id = ? OR is_builtin = true)", tenantID).
				First(&target).Error; err != nil {
				return err
			}
			if !usableReassignmentTarget(&target, modelType) {
				return apperrors.NewBadRequestError(
					"reassignment target model is no longer usable",
				).WithDetails(plan)
			}
		}

		if err := s.tenantRepo.ClearDefaultModelSlots(ctx, tx, tenantID, id); err != nil {
			return err
		}
		if len(kbCounts) > 0 {
			if err := s.kbRepo.ReassignModelReferences(ctx, tx, tenantID, id, targets); err != nil {
				return err
			}
		}
		if len(agentCounts) > 0 {
			if err := s.agentRepo.ReassignModelReferences(ctx, tx, tenantID, id, targets); err != nil {
				return err
			}
		}
		if len(memoryBindings) > 0 {
			if err := s.tenantRepo.ReassignMemoryModelReferences(
				ctx, tx, tenantID, id, targets,
			); err != nil {
				return err
			}
		}
		// 会话记忆不阻塞删除：有默认对话模型时改写，否则清空选择。
		if sessionCount > 0 && s.sessionRepo != nil {
			if err := s.sessionRepo.ReassignLastRequestModel(
				ctx, tx, tenantID, id, plan.SessionTargetModelID,
			); err != nil {
				return err
			}
		}

		// 提交前复查，防止并发保存把旧模型 ID 写回。
		kbAfter, err := s.kbRepo.CountModelBindings(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		agentAfter, err := s.agentRepo.CountModelBindings(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		sessionAfter, err := s.countSessionModelBindings(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		if len(kbAfter) > 0 || len(agentAfter) > 0 || sessionAfter > 0 {
			return apperrors.NewBadRequestError(
				"model references changed during deletion; please retry",
			).WithDetails(plan)
		}

		result := tx.WithContext(ctx).
			Where("id = ? AND tenant_id = ?", id, tenantID).
			Delete(&types.Model{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrModelNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	logger.Infof(ctx, "Model deleted with reassignment: %s", id)
	return plan, nil
}

func formatModelReassignmentBlockMessage(plan *types.ModelReassignmentPlan) string {
	parts := make([]string, 0, len(plan.Blockers))
	for _, blocker := range plan.Blockers {
		switch blocker.Code {
		case types.ModelReassignmentBlockerEmbeddingHasDocuments:
			parts = append(parts, fmt.Sprintf(
				"%d knowledge base(s) already contain documents and use this embedding model",
				blocker.KnowledgeBaseCount,
			))
		case types.ModelReassignmentBlockerEmbeddingInDocuments:
			parts = append(parts, fmt.Sprintf(
				"%d document(s) still store vectors from this embedding model",
				blocker.DocumentCount,
			))
		case types.ModelReassignmentBlockerChatHistoryEmbedding:
			parts = append(parts,
				"chat history index uses this embedding model and requires re-indexing")
		case types.ModelReassignmentBlockerNoDefaultModel:
			parts = append(parts, fmt.Sprintf(
				"no usable default %s model for binding %s",
				blocker.ModelType, blocker.Binding,
			))
		default:
			parts = append(parts, blocker.Code)
		}
	}
	return "model cannot be deleted: " + strings.Join(parts, "; ")
}

// GetEmbeddingModel retrieves and initializes an embedding model instance
// Takes a model ID and returns an Embedder interface implementation
func (s *modelService) GetEmbeddingModel(ctx context.Context, modelId string) (embedding.Embedder, error) {
	// Get the model details
	model, err := s.GetModelByID(ctx, modelId)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id": modelId,
		})
		return nil, err
	}

	logger.Infof(ctx, "Getting embedding model: %s, source: %s", model.Name, model.Source)

	appID, appSecret := s.resolveWeKnoraCloudCredentials(ctx, &model.Parameters)

	embedder, err := embedding.NewEmbedder(embedding.ConfigFromModel(model, appID, appSecret), s.pooler, s.ollamaService)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   model.ID,
			"model_name": model.Name,
		})
		return nil, err
	}

	logger.Info(ctx, "Embedding model initialized successfully")
	return embedder, nil
}

// GetEmbeddingModelForTenant retrieves and initializes an embedding model for a specific tenant
// This is used for cross-tenant knowledge base sharing where the embedding model from
// the source tenant must be used to ensure vector compatibility
func (s *modelService) GetEmbeddingModelForTenant(ctx context.Context, modelId string, tenantID uint64) (embedding.Embedder, error) {
	// Check if model ID is empty
	if modelId == "" {
		logger.Error(ctx, "Model ID is empty")
		return nil, errors.New("model ID cannot be empty")
	}

	// Fetch model from repository using the specified tenant ID
	model, err := s.repo.GetByID(ctx, tenantID, modelId)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":  modelId,
			"tenant_id": tenantID,
		})
		return nil, err
	}

	if model == nil {
		logger.Error(ctx, "Model not found for specified tenant")
		return nil, ErrModelNotFound
	}

	if model.Status != types.ModelStatusActive {
		logger.Errorf(ctx, "Model is not active, status: %s", model.Status)
		return nil, errors.New("model is not active")
	}

	logger.Infof(ctx, "Getting cross-tenant embedding model: %s, source: %s, tenant: %d", model.Name, model.Source, tenantID)

	appID, appSecret := s.resolveWeKnoraCloudCredentials(ctx, &model.Parameters)

	embedder, err := embedding.NewEmbedder(embedding.ConfigFromModel(model, appID, appSecret), s.pooler, s.ollamaService)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   model.ID,
			"model_name": model.Name,
			"tenant_id":  tenantID,
		})
		return nil, err
	}

	logger.Info(ctx, "Cross-tenant embedding model initialized successfully")
	return embedder, nil
}

// GetRerankModel retrieves and initializes a reranking model instance
// Takes a model ID and returns a Reranker interface implementation
func (s *modelService) GetRerankModel(ctx context.Context, modelId string) (rerank.Reranker, error) {
	// Get the model details
	model, err := s.GetModelByID(ctx, modelId)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id": modelId,
		})
		return nil, err
	}

	logger.Infof(ctx, "Getting rerank model: %s, source: %s", model.Name, model.Source)

	appID, appSecret := s.resolveWeKnoraCloudCredentials(ctx, &model.Parameters)

	reranker, err := rerank.NewReranker(rerank.ConfigFromModel(model, appID, appSecret))
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   model.ID,
			"model_name": model.Name,
		})
		return nil, err
	}

	logger.Info(ctx, "Rerank model initialized successfully")
	return reranker, nil
}

// GetChatModel retrieves and initializes a chat model instance
// Takes a model ID and returns a Chat interface implementation
func (s *modelService) GetChatModel(ctx context.Context, modelId string) (chat.Chat, error) {
	// Check if model ID is empty
	if modelId == "" {
		logger.Error(ctx, "Model ID is empty")
		return nil, errors.New("model ID cannot be empty")
	}

	tenantID := types.MustTenantIDFromContext(ctx)

	// Get the model directly from repository to avoid status checks
	model, err := s.repo.GetByID(ctx, tenantID, modelId)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":  modelId,
			"tenant_id": tenantID,
		})
		return nil, err
	}

	if model == nil {
		logger.Error(ctx, "Chat model not found")
		return nil, ErrModelNotFound
	}

	logger.Infof(ctx, "Getting chat model: %s, source: %s", model.Name, model.Source)

	appID, appSecret := s.resolveWeKnoraCloudCredentials(ctx, &model.Parameters)

	chatModel, err := chat.NewChat(chat.ConfigFromModel(model, appID, appSecret), s.ollamaService)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   model.ID,
			"model_name": model.Name,
		})
		return nil, err
	}

	return chatModel, nil
}

// GetVLMModel retrieves and initializes a vision language model instance.
func (s *modelService) GetVLMModel(ctx context.Context, modelId string) (vlm.VLM, error) {
	if modelId == "" {
		return nil, errors.New("model ID cannot be empty")
	}

	tenantID := types.MustTenantIDFromContext(ctx)

	model, err := s.repo.GetByID(ctx, tenantID, modelId)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":  modelId,
			"tenant_id": tenantID,
		})
		return nil, err
	}

	if model == nil {
		return nil, ErrModelNotFound
	}

	logger.Infof(ctx, "Getting VLM model: %s, source: %s", model.Name, model.Source)

	appID, appSecret := s.resolveWeKnoraCloudCredentials(ctx, &model.Parameters)

	vlmModel, err := vlm.NewVLM(vlm.ConfigFromModel(model, appID, appSecret), s.ollamaService)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   model.ID,
			"model_name": model.Name,
		})
		return nil, err
	}

	return vlmModel, nil
}

// GetASRModel retrieves and initializes an automatic speech recognition model instance.
func (s *modelService) GetASRModel(ctx context.Context, modelId string) (asr.ASR, error) {
	if modelId == "" {
		return nil, errors.New("model ID cannot be empty")
	}

	tenantID := types.MustTenantIDFromContext(ctx)

	model, err := s.repo.GetByID(ctx, tenantID, modelId)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":  modelId,
			"tenant_id": tenantID,
		})
		return nil, err
	}

	if model == nil {
		return nil, ErrModelNotFound
	}

	logger.Infof(ctx, "Getting ASR model: %s, source: %s", model.Name, model.Source)

	sttModel, err := asr.NewASR(asr.ConfigFromModel(model))
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"model_id":   model.ID,
			"model_name": model.Name,
		})
		return nil, err
	}

	return sttModel, nil
}

func formatModelInUseMessage(
	kbCount, agentCount, docCount int64, memory, chatHistory bool,
) string {
	var parts []string
	if kbCount > 0 {
		parts = append(parts, fmt.Sprintf("%d knowledge base(s)", kbCount))
	}
	if agentCount > 0 {
		parts = append(parts, fmt.Sprintf("%d agent(s)", agentCount))
	}
	if docCount > 0 {
		parts = append(parts, fmt.Sprintf("%d document(s)", docCount))
	}
	if memory {
		parts = append(parts, "long-term memory")
	}
	if chatHistory {
		parts = append(parts, "chat history index")
	}
	joined := strings.Join(parts, " and ")
	return fmt.Sprintf(
		"model is used by %s; reconfigure or remove those references before deleting",
		joined,
	)
}
