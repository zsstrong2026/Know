// 导出层脱敏设置：只影响复制 / 导出 / 存为文档，不改变页面与追问使用的原文。
package insight

import (
	"context"
	"strings"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
)

// GetPrivacy 取当前用户的脱敏设置；没有保存过时返回空词表。
func (s *Service) GetPrivacy(ctx context.Context) (*types.InsightPrivacy, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	stored, err := s.repo.GetPrivacy(ctx, tenantID, ownerID)
	if err != nil {
		return nil, err
	}
	// 未保存过时字段是 nil，直接序列化会变成 null；统一成空数组，前端可直接遍历。
	if stored == nil {
		stored = &types.InsightPrivacy{}
	}
	normalized := normalizePrivacy(stored)
	return &normalized, nil
}

// UpdatePrivacy 清洗并覆盖保存脱敏设置。
func (s *Service) UpdatePrivacy(
	ctx context.Context, input *types.InsightPrivacy,
) (*types.InsightPrivacy, error) {
	tenantID, ownerID, err := callerScope(ctx)
	if err != nil {
		return nil, err
	}
	if input == nil {
		return nil, apperrors.NewValidationError("privacy settings are required")
	}
	privacy := normalizePrivacy(input)
	if err := s.repo.SavePrivacy(ctx, tenantID, ownerID, &privacy); err != nil {
		return nil, err
	}
	return &privacy, nil
}

// normalizePrivacy 去空、去重、限长限量；顺序保持用户输入顺序。
func normalizePrivacy(input *types.InsightPrivacy) types.InsightPrivacy {
	return types.InsightPrivacy{
		Persons:            normalizeTerms(input.Persons),
		Organizations:      normalizeTerms(input.Organizations),
		Projects:           normalizeTerms(input.Projects),
		HideEvidenceQuotes: input.HideEvidenceQuotes,
	}
}

// normalizeTerms 返回非 nil 切片，保证 API 输出 [] 而不是 null。
func normalizeTerms(terms []string) []string {
	out := make([]string, 0, len(terms))
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		trimmed := strings.TrimSpace(term)
		if trimmed == "" {
			continue
		}
		runes := []rune(trimmed)
		if len(runes) > types.InsightPrivacyMaxTermRunes {
			trimmed = string(runes[:types.InsightPrivacyMaxTermRunes])
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
		if len(out) >= types.InsightPrivacyMaxTerms {
			break
		}
	}
	return out
}
