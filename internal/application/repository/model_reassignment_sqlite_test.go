package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupReassignDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	if len(models) > 0 {
		require.NoError(t, db.AutoMigrate(models...))
	}
	return db
}

// TestKnowledgeBaseReassignModelReferences_SQLite 同时验证：
// 1) 改写是全量 WHERE 更新，不经过 50 条上限的列表查询；
// 2) 标量列与 JSON 列都能命中；
// 3) 其他租户的数据不受影响。
func TestKnowledgeBaseReassignModelReferences_SQLite(t *testing.T) {
	db := setupKBTestDB(t)
	repo := NewKnowledgeBaseRepository(db)
	ctx := context.Background()

	for i := 0; i < 55; i++ {
		kb := &types.KnowledgeBase{
			ID:                    fmt.Sprintf("kb-%02d", i),
			Name:                  "kb",
			TenantID:              1,
			EmbeddingModelID:      "old",
			SummaryModelID:        "old",
			ImageProcessingConfig: types.ImageProcessingConfig{ModelID: "old"},
			VLMConfig:             types.VLMConfig{ModelID: "old"},
			ASRConfig:             types.ASRConfig{ModelID: "old"},
			WikiConfig:            &types.WikiConfig{SynthesisModelID: "old"},
			AutoTagConfig:         &types.AutoTagConfig{ModelID: "old"},
		}
		require.NoError(t, db.Create(kb).Error)
	}
	other := &types.KnowledgeBase{
		ID: "kb-other", Name: "other", TenantID: 2,
		EmbeddingModelID: "old", SummaryModelID: "old",
	}
	require.NoError(t, db.Create(other).Error)

	counts, err := repo.CountModelBindings(ctx, nil, 1, "old")
	require.NoError(t, err)
	assert.Equal(t, int64(55), counts[types.ModelUsageBindingEmbeddingModel])
	assert.Equal(t, int64(55), counts[types.ModelUsageBindingAutoTagModel])

	targets := map[types.ModelUsageBinding]string{
		types.ModelUsageBindingEmbeddingModel:       "new-embed",
		types.ModelUsageBindingSummaryModel:         "new-chat",
		types.ModelUsageBindingImageProcessingModel: "new-vlm",
		types.ModelUsageBindingVLMModel:             "new-vlm",
		types.ModelUsageBindingASRModel:             "new-asr",
		types.ModelUsageBindingWikiSynthesisModel:   "new-chat",
		types.ModelUsageBindingAutoTagModel:         "new-chat",
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return repo.ReassignModelReferences(ctx, tx, 1, "old", targets)
	}))

	var got types.KnowledgeBase
	require.NoError(t, db.First(&got, "id = ?", "kb-00").Error)
	assert.Equal(t, "new-embed", got.EmbeddingModelID)
	assert.Equal(t, "new-chat", got.SummaryModelID)

	// JSON 配置的 Scan 只接受 []byte，SQLite 返回 string 时会被零值化，
	// 因此这里直接断言原始 JSON 内容。
	var rawImage, rawVLM, rawASR, rawWiki, rawAutoTag string
	require.NoError(t, db.Raw(`SELECT image_processing_config, vlm_config, asr_config, wiki_config, auto_tag_config
		FROM knowledge_bases WHERE id = ?`, "kb-00").
		Row().Scan(&rawImage, &rawVLM, &rawASR, &rawWiki, &rawAutoTag))
	assert.Contains(t, rawImage, `"model_id":"new-vlm"`)
	assert.Contains(t, rawVLM, `"model_id":"new-vlm"`)
	assert.Contains(t, rawASR, `"model_id":"new-asr"`)
	assert.Contains(t, rawWiki, `"synthesis_model_id":"new-chat"`)
	assert.Contains(t, rawAutoTag, `"model_id":"new-chat"`)

	var untouched types.KnowledgeBase
	require.NoError(t, db.First(&untouched, "id = ?", "kb-other").Error)
	assert.Equal(t, "old", untouched.EmbeddingModelID)

	var remaining int64
	require.NoError(t, db.Model(&types.KnowledgeBase{}).
		Where("tenant_id = ? AND embedding_model_id = ?", 1, "old").
		Count(&remaining).Error)
	assert.Zero(t, remaining)
}

// TestCountKnowledgeBasesWithDocumentsByEmbeddingModel_SQLite 验证文档阻塞
// 统计口径：只算非工作知识库，且必须存在未删除文档。
func TestCountKnowledgeBasesWithDocumentsByEmbeddingModel_SQLite(t *testing.T) {
	db := setupKBTestDB(t)
	require.NoError(t, db.Exec(`
		CREATE TABLE knowledges (
			id VARCHAR(36) PRIMARY KEY,
			knowledge_base_id VARCHAR(36),
			tenant_id INTEGER,
			deleted_at DATETIME
		)`).Error)
	repo := NewKnowledgeBaseRepository(db)
	ctx := context.Background()

	withDocs := &types.KnowledgeBase{ID: "kb-1", Name: "docs", TenantID: 1, EmbeddingModelID: "old"}
	work := &types.KnowledgeBase{
		ID: "kb-work", Name: "work", TenantID: 1, EmbeddingModelID: "old",
		IsSystem: true, SystemKey: types.KBSystemKeyWork,
	}
	withoutDocs := &types.KnowledgeBase{ID: "kb-2", Name: "empty", TenantID: 1, EmbeddingModelID: "old"}
	for _, kb := range []*types.KnowledgeBase{withDocs, work, withoutDocs} {
		require.NoError(t, db.Create(kb).Error)
	}
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, knowledge_base_id, tenant_id) VALUES (?, ?, ?)`,
		"k-1", "kb-1", 1,
	).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, knowledge_base_id, tenant_id) VALUES (?, ?, ?)`,
		"k-work", "kb-work", 1,
	).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, knowledge_base_id, tenant_id) VALUES (?, ?, ?)`,
		"k-deleted", "kb-2", 1,
	).Error)
	require.NoError(t, db.Exec(`UPDATE knowledges SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, "k-deleted").Error)

	count, err := repo.CountKnowledgeBasesWithDocumentsByEmbeddingModel(ctx, nil, 1, "old")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestSessionLastRequestModelReassignment_SQLite(t *testing.T) {
	db := setupReassignDB(t, &types.Session{})
	repo := NewSessionRepository(db)
	ctx := context.Background()

	created := make([]*types.Session, 0, 3)
	for _, modelID := range []string{"old", "old", "keep"} {
		session := &types.Session{
			TenantID: 1,
			LastRequestState: &types.SessionLastRequestState{
				ModelID: modelID,
			},
		}
		// BeforeCreate 会覆盖 ID，测试需保留写入后的真实主键。
		require.NoError(t, db.Create(session).Error)
		created = append(created, session)
	}
	targetID := created[0].ID

	count, err := repo.CountSessionsByLastRequestModel(ctx, nil, 1, "old")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return repo.ReassignLastRequestModel(ctx, tx, 1, "old", "new-chat")
	}))

	var raw string
	require.NoError(t, db.Raw(
		`SELECT agent_config FROM sessions WHERE id = ?`, targetID,
	).Row().Scan(&raw))
	assert.Contains(t, raw, `"model_id":"new-chat"`)

	// 空目标表示清空选择。
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return repo.ReassignLastRequestModel(ctx, tx, 1, "new-chat", "")
	}))
	require.NoError(t, db.Raw(
		`SELECT agent_config FROM sessions WHERE id = ?`, targetID,
	).Row().Scan(&raw))
	assert.Contains(t, raw, `"model_id":""`)
}

// TestCountKnowledgeDocumentsByEmbeddingModel_SQLite 验证文档级 embedding
// 引用的统计口径：排除工作知识库与软删除文档。
func TestCountKnowledgeDocumentsByEmbeddingModel_SQLite(t *testing.T) {
	db := setupKBTestDB(t)
	require.NoError(t, db.Exec(`
		CREATE TABLE knowledges (
			id VARCHAR(36) PRIMARY KEY,
			knowledge_base_id VARCHAR(36),
			tenant_id INTEGER,
			embedding_model_id VARCHAR(64),
			deleted_at DATETIME
		)`).Error)
	repo := NewKnowledgeBaseRepository(db)
	ctx := context.Background()

	normal := &types.KnowledgeBase{ID: "kb-1", Name: "docs", TenantID: 1, EmbeddingModelID: "old"}
	work := &types.KnowledgeBase{
		ID: "kb-work", Name: "work", TenantID: 1, EmbeddingModelID: "old",
		IsSystem: true, SystemKey: types.KBSystemKeyWork,
	}
	for _, kb := range []*types.KnowledgeBase{normal, work} {
		require.NoError(t, db.Create(kb).Error)
	}
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, knowledge_base_id, tenant_id, embedding_model_id) VALUES (?, ?, ?, ?)`,
		"k-1", "kb-1", 1, "old",
	).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, knowledge_base_id, tenant_id, embedding_model_id) VALUES (?, ?, ?, ?)`,
		"k-work", "kb-work", 1, "old",
	).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, knowledge_base_id, tenant_id, embedding_model_id) VALUES (?, ?, ?, ?)`,
		"k-deleted", "kb-1", 1, "old",
	).Error)
	require.NoError(t, db.Exec(
		`UPDATE knowledges SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, "k-deleted",
	).Error)

	count, err := repo.CountKnowledgeDocumentsByEmbeddingModel(ctx, nil, 1, "old")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestCustomAgentReassignModelReferences_SQLite(t *testing.T) {
	db := setupReassignDB(t, &types.CustomAgent{})
	repo := NewCustomAgentRepository(db)
	ctx := context.Background()

	agent := &types.CustomAgent{
		ID: "agent-1", TenantID: 1, Name: "agent",
		Config: types.CustomAgentConfig{
			ModelID:                "old",
			RerankModelID:          "old",
			VLMModelID:             "old",
			ASRModelID:             "old",
			QueryUnderstandModelID: "old",
			QuestionSuggestions: &types.QuestionSuggestionConfig{
				FollowUps: types.FollowUpSuggestionConfig{ModelID: "old"},
			},
		},
	}
	require.NoError(t, db.Create(agent).Error)

	counts, err := repo.CountModelBindings(ctx, nil, 1, "old")
	require.NoError(t, err)
	assert.Equal(t, int64(1), counts[types.ModelUsageBindingFollowUpModel])

	targets := map[types.ModelUsageBinding]string{
		types.ModelUsageBindingChatModel:            "new-chat",
		types.ModelUsageBindingRerankModel:          "new-rerank",
		types.ModelUsageBindingVLMModel:             "new-vlm",
		types.ModelUsageBindingASRModel:             "new-asr",
		types.ModelUsageBindingQueryUnderstandModel: "new-chat",
		types.ModelUsageBindingFollowUpModel:        "new-chat",
	}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return repo.ReassignModelReferences(ctx, tx, 1, "old", targets)
	}))

	var rawConfig string
	require.NoError(t, db.Raw(
		`SELECT config FROM custom_agents WHERE id = ?`, "agent-1",
	).Row().Scan(&rawConfig))
	assert.Contains(t, rawConfig, `"model_id":"new-chat"`)
	assert.Contains(t, rawConfig, `"rerank_model_id":"new-rerank"`)
	assert.Contains(t, rawConfig, `"vlm_model_id":"new-vlm"`)
	assert.Contains(t, rawConfig, `"asr_model_id":"new-asr"`)
	assert.Contains(t, rawConfig, `"query_understand_model_id":"new-chat"`)
	// 顶层 model_id 与 question_suggestions.follow_ups.model_id 都应改成新模型。
	assert.GreaterOrEqual(t, strings.Count(rawConfig, `"model_id":"new-chat"`), 2)
}

func TestTenantDefaultSlotsAndMemoryReassignment_SQLite(t *testing.T) {
	db := setupReassignDB(t, &types.Tenant{})
	repo := NewTenantRepository(db)
	ctx := context.Background()

	tenant := &types.Tenant{
		ID:   1,
		Name: "tenant",
		DefaultSettings: &types.DefaultSettings{
			ChatModelID:      "old",
			UtilityModelID:   "keep",
			EmbeddingModelID: "old",
		},
		MemoryConfig: &types.MemoryConfig{
			Enabled:          true,
			EmbeddingModelID: "old",
			ExtractModelID:   "old",
		},
	}
	require.NoError(t, db.Create(tenant).Error)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if err := repo.ClearDefaultModelSlots(ctx, tx, 1, "old"); err != nil {
			return err
		}
		return repo.ReassignMemoryModelReferences(ctx, tx, 1, "old", map[types.ModelUsageBinding]string{
			types.ModelUsageBindingEmbeddingModel: "new-embed",
			types.ModelUsageBindingExtractModel:   "new-chat",
		})
	}))

	var got types.Tenant
	require.NoError(t, db.First(&got, "id = ?", 1).Error)
	require.NotNil(t, got.DefaultSettings)
	assert.Empty(t, got.DefaultSettings.ChatModelID)
	assert.Equal(t, "keep", got.DefaultSettings.UtilityModelID)
	assert.Empty(t, got.DefaultSettings.EmbeddingModelID)
	require.NotNil(t, got.MemoryConfig)
	assert.Equal(t, "new-embed", got.MemoryConfig.EmbeddingModelID)
	assert.Equal(t, "new-chat", got.MemoryConfig.ExtractModelID)
}
