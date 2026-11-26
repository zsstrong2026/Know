package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// modelUsageBindingSQL 描述一个模型绑定在数据库里的位置。
// JSONKey 为空表示标量列；否则为点号分隔的 JSON 字段路径（可多级）。
type modelUsageBindingSQL struct {
	Binding types.ModelUsageBinding
	Column  string
	JSONKey string
}

// knowledgeBaseModelBindingSQL 与 scopeKnowledgeBasesByModelID 的命中条件保持一致。
var knowledgeBaseModelBindingSQL = []modelUsageBindingSQL{
	{types.ModelUsageBindingEmbeddingModel, "embedding_model_id", ""},
	{types.ModelUsageBindingSummaryModel, "summary_model_id", ""},
	{types.ModelUsageBindingImageProcessingModel, "image_processing_config", "model_id"},
	{types.ModelUsageBindingVLMModel, "vlm_config", "model_id"},
	{types.ModelUsageBindingASRModel, "asr_config", "model_id"},
	{types.ModelUsageBindingWikiSynthesisModel, "wiki_config", "synthesis_model_id"},
	{types.ModelUsageBindingAutoTagModel, "auto_tag_config", "model_id"},
}

// customAgentModelBindingSQL 与 scopeCustomAgentsByModelID 的命中条件保持一致。
var customAgentModelBindingSQL = []modelUsageBindingSQL{
	{types.ModelUsageBindingChatModel, "config", "model_id"},
	{types.ModelUsageBindingRerankModel, "config", "rerank_model_id"},
	{types.ModelUsageBindingVLMModel, "config", "vlm_model_id"},
	{types.ModelUsageBindingASRModel, "config", "asr_model_id"},
	{types.ModelUsageBindingQueryUnderstandModel, "config", "query_understand_model_id"},
	{types.ModelUsageBindingFollowUpModel, "config", "question_suggestions.follow_ups.model_id"},
}

// postgresJSONTextExpr 生成 PG 的 JSON 文本取值表达式，例如
// config->'question_suggestions'->'follow_ups'->>'model_id'。
func postgresJSONTextExpr(column, key string) string {
	parts := strings.Split(key, ".")
	expr := column
	for i, part := range parts {
		if i == len(parts)-1 {
			expr += fmt.Sprintf("->>'%s'", part)
			continue
		}
		expr += fmt.Sprintf("->'%s'", part)
	}
	return expr
}

// modelBindingHitExpr 返回 `<绑定位置> = ?` 的方言化匹配表达式。
func modelBindingHitExpr(db *gorm.DB, def modelUsageBindingSQL) string {
	if def.JSONKey == "" {
		return def.Column + " = ?"
	}
	if db.Dialector.Name() == "postgres" {
		return postgresJSONTextExpr(def.Column, def.JSONKey) + " = ?"
	}
	return fmt.Sprintf("json_extract(%s, '$.%s') = ?", def.Column, def.JSONKey)
}

// countModelBindings 用一条聚合查询统计每个绑定的命中数量。
// table 必须是带 deleted_at 的表（knowledge_bases / custom_agents）。
// 这里用 Raw 而不是 Select，避免 GORM 对聚合 Select 的占位符重复展开。
func countModelBindings(
	ctx context.Context, db *gorm.DB, table string,
	tenantID uint64, modelID string, defs []modelUsageBindingSQL,
) (map[types.ModelUsageBinding]int64, error) {
	selects := make([]string, 0, len(defs))
	args := make([]interface{}, 0, len(defs)+1)
	for i, def := range defs {
		// hitExpr 本身已包含 `= ?`，这里不能重复追加。
		selects = append(selects, fmt.Sprintf(
			"COALESCE(SUM(CASE WHEN %s THEN 1 ELSE 0 END), 0) AS c%d",
			modelBindingHitExpr(db, def), i,
		))
		args = append(args, modelID)
	}
	args = append(args, tenantID)

	values := make([]int64, len(defs))
	dest := make([]interface{}, len(defs))
	for i := range values {
		dest[i] = &values[i]
	}
	query := fmt.Sprintf(
		"SELECT %s FROM %s WHERE tenant_id = ? AND deleted_at IS NULL",
		strings.Join(selects, ", "), table,
	)
	row := db.WithContext(ctx).Raw(query, args...).Row()
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}

	counts := make(map[types.ModelUsageBinding]int64, len(defs))
	for i, def := range defs {
		if values[i] > 0 {
			counts[def.Binding] = values[i]
		}
	}
	return counts, nil
}

// modelBindingJSONSetExpr 生成只替换 JSON 字段的方言化表达式。
func modelBindingJSONSetExpr(db *gorm.DB, column, key, target string) clause.Expr {
	if db.Dialector.Name() == "postgres" {
		path := "{" + strings.Join(strings.Split(key, "."), ",") + "}"
		return gorm.Expr(fmt.Sprintf(
			"jsonb_set(%s::jsonb, '%s', to_jsonb(?::text))::json", column, path,
		), target)
	}
	return gorm.Expr(fmt.Sprintf("json_set(%s, '$.%s', ?)", column, key), target)
}

// modelBindingReassignExpr 生成“命中旧模型才替换”的 CASE 表达式，
// 这样同一条 UPDATE 可以只改写真正引用旧模型的列。
func modelBindingReassignExpr(
	db *gorm.DB, def modelUsageBindingSQL, modelID, target string,
) clause.Expr {
	if def.JSONKey == "" {
		return gorm.Expr(fmt.Sprintf(
			"CASE WHEN %s = ? THEN ? ELSE %s END", def.Column, def.Column,
		), modelID, target)
	}
	return modelBindingJSONSetCaseExpr(db, def.Column, def.JSONKey, modelID, target)
}

func modelBindingJSONSetCaseExpr(
	db *gorm.DB, column, key, modelID, target string,
) clause.Expr {
	if db.Dialector.Name() == "postgres" {
		path := "{" + strings.Join(strings.Split(key, "."), ",") + "}"
		// THEN/ELSE 统一转成 jsonb，避免 json 与 jsonb 的 CASE 类型不匹配；
		// 赋值回原列时由 PG 的 assignment cast 处理。
		return gorm.Expr(fmt.Sprintf(
			"CASE WHEN %s = ? THEN jsonb_set(%s::jsonb, '%s', to_jsonb(?::text)) ELSE %s::jsonb END",
			postgresJSONTextExpr(column, key), column, path, column,
		), modelID, target)
	}
	return gorm.Expr(fmt.Sprintf(
		"CASE WHEN %s THEN json_set(%s, '$.%s', ?) ELSE %s END",
		modelBindingHitExpr(db, modelUsageBindingSQL{Column: column, JSONKey: key}),
		column, key, column,
	), modelID, target)
}

func knowledgeBaseModelUsageBindings(kb *types.KnowledgeBase, modelID string) []types.ModelUsageBinding {
	bindings := make([]types.ModelUsageBinding, 0, 8)
	if kb.EmbeddingModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingEmbeddingModel)
	}
	if kb.SummaryModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingSummaryModel)
	}
	if kb.QuestionGenerationConfig != nil && kb.QuestionGenerationConfig.ModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingQuestionModel)
	}
	if kb.ImageProcessingConfig.ModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingImageProcessingModel)
	}
	if kb.VLMConfig.ModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingVLMModel)
	}
	if kb.ASRConfig.ModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingASRModel)
	}
	if kb.WikiConfig != nil && kb.WikiConfig.SynthesisModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingWikiSynthesisModel)
	}
	if kb.AutoTagConfig != nil && kb.AutoTagConfig.ModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingAutoTagModel)
	}
	return bindings
}

func customAgentModelUsageBindings(agent *types.CustomAgent, modelID string) []types.ModelUsageBinding {
	bindings := make([]types.ModelUsageBinding, 0, 6)
	if agent.Config.ModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingChatModel)
	}
	if agent.Config.RerankModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingRerankModel)
	}
	if agent.Config.VLMModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingVLMModel)
	}
	if agent.Config.ASRModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingASRModel)
	}
	if agent.Config.QueryUnderstandModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingQueryUnderstandModel)
	}
	if agent.Config.QuestionSuggestions != nil &&
		agent.Config.QuestionSuggestions.FollowUps.ModelID == modelID {
		bindings = append(bindings, types.ModelUsageBindingFollowUpModel)
	}
	return bindings
}

// scopeKnowledgeBasesByModelID filters knowledge_bases rows that reference
// modelID in any model-binding field.
func scopeKnowledgeBasesByModelID(db *gorm.DB, modelID string) *gorm.DB {
	if db.Dialector.Name() == "postgres" {
		return db.Where(
			"embedding_model_id = ? OR summary_model_id = ? OR "+
				"image_processing_config->>'model_id' = ? OR "+
				"vlm_config->>'model_id' = ? OR "+
				"asr_config->>'model_id' = ? OR "+
				"wiki_config->>'synthesis_model_id' = ? OR "+
				"auto_tag_config->>'model_id' = ? OR "+
				"question_generation_config->>'model_id' = ?",
			modelID, modelID, modelID, modelID, modelID, modelID, modelID, modelID,
		)
	}
	return db.Where(
		"embedding_model_id = ? OR summary_model_id = ? OR "+
			"json_extract(image_processing_config, '$.model_id') = ? OR "+
			"json_extract(vlm_config, '$.model_id') = ? OR "+
			"json_extract(asr_config, '$.model_id') = ? OR "+
			"json_extract(wiki_config, '$.synthesis_model_id') = ? OR "+
			"json_extract(auto_tag_config, '$.model_id') = ? OR "+
			"json_extract(question_generation_config, '$.model_id') = ?",
		modelID, modelID, modelID, modelID, modelID, modelID, modelID, modelID,
	)
}

// scopeCustomAgentsByModelID filters custom_agents rows whose config JSON
// references modelID in any model-binding field.
func scopeCustomAgentsByModelID(db *gorm.DB, modelID string) *gorm.DB {
	if db.Dialector.Name() == "postgres" {
		return db.Where(
			"config->>'model_id' = ? OR config->>'rerank_model_id' = ? OR "+
				"config->>'vlm_model_id' = ? OR config->>'asr_model_id' = ? OR "+
				"config->>'query_understand_model_id' = ? OR "+
				"config->'question_suggestions'->'follow_ups'->>'model_id' = ?",
			modelID, modelID, modelID, modelID, modelID, modelID,
		)
	}
	return db.Where(
		"json_extract(config, '$.model_id') = ? OR "+
			"json_extract(config, '$.rerank_model_id') = ? OR "+
			"json_extract(config, '$.vlm_model_id') = ? OR "+
			"json_extract(config, '$.asr_model_id') = ? OR "+
			"json_extract(config, '$.query_understand_model_id') = ? OR "+
			"json_extract(config, '$.question_suggestions.follow_ups.model_id') = ?",
		modelID, modelID, modelID, modelID, modelID, modelID,
	)
}

func scopeCustomAgentsBySandboxConfigID(db *gorm.DB, configID string) *gorm.DB {
	if db.Dialector.Name() == "postgres" {
		return db.Where("config->>'sandbox_config_id' = ?", configID)
	}
	return db.Where("json_extract(config, '$.sandbox_config_id') = ?", configID)
}
