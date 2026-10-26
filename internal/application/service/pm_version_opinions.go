package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// UpdateReviewOpinion 更新某轮某条评审意见的处理状态与说明（轮次、下标均从 1/0 起）。
// 历史轮次允许补充处理说明，但结论与轮次本身不可修改。
func (s *pmVersionService) UpdateReviewOpinion(
	ctx context.Context, tenantID uint64, id string, round int, index int,
	status string, note string,
) (*types.PMVersion, error) {
	if status != types.ReviewOpinionPending && status != types.ReviewOpinionResolved {
		return nil, fmt.Errorf("%w: %s", ErrVersionOpinionInvalid, status)
	}
	v, err := s.deps.Repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	rounds, err := decodeReviewRounds(v.ReviewOpinions)
	if err != nil {
		return nil, err
	}

	var target *reviewRoundView
	for i := range rounds {
		if rounds[i].Round == round {
			target = &rounds[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%w: round=%d", ErrVersionOpinionNotFound, round)
	}
	body := parseOpinionsBody(target.Opinions)
	if index < 0 || index >= len(body.Items) {
		return nil, fmt.Errorf("%w: index=%d", ErrVersionOpinionNotFound, index)
	}
	body.Items[index].Status = status
	body.Items[index].Note = strings.TrimSpace(note)

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	target.Opinions = raw
	all, err := json.Marshal(rounds)
	if err != nil {
		return nil, err
	}
	v.ReviewOpinions = types.JSON(all)
	if err := s.deps.Repo.Update(ctx, v); err != nil {
		return nil, err
	}
	s.decorate(ctx, v)
	return v, nil
}

// reviewRoundView 保留未知字段的轮次视图，避免回写时丢失历史数据
type reviewRoundView struct {
	Round      int             `json:"round"`
	Conclusion string          `json:"conclusion"`
	Opinions   json.RawMessage `json:"opinions"`
	Reviewers  string          `json:"reviewers,omitempty"`
	ReviewedAt json.RawMessage `json:"reviewed_at,omitempty"`
}

// decodeReviewRounds 解析版本评审历史；空值返回空切片
func decodeReviewRounds(raw types.JSON) ([]reviewRoundView, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var rounds []reviewRoundView
	if err := json.Unmarshal(raw, &rounds); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrVersionReviewInvalid, err)
	}
	return rounds, nil
}

// parseOpinionsBody 容错解析意见载体，兼容三种历史写法：
//  1. {items:[{text,status,note}], change_note}（当前标准）；
//  2. [{text|item, status}, ...] 纯数组（早期评审弹窗）；
//  3. "整段文字" 纯字符串。
func parseOpinionsBody(raw json.RawMessage) types.PMReviewOpinionsBody {
	body := types.PMReviewOpinionsBody{}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return body
	}
	if strings.HasPrefix(trimmed, "{") {
		if err := json.Unmarshal(raw, &body); err == nil {
			for i := range body.Items {
				body.Items[i].Status = normalizeOpinionStatus(body.Items[i].Status)
			}
			return body
		}
		return body
	}
	var items []struct {
		Text   string `json:"text"`
		Item   string `json:"item"` // 兼容早期字段名
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(raw, &items); err != nil {
			return body
		}
		for _, it := range items {
			text := strings.TrimSpace(it.Text)
			if text == "" {
				text = strings.TrimSpace(it.Item)
			}
			body.Items = append(body.Items, types.PMReviewOpinionItem{
				Text:   text,
				Status: normalizeOpinionStatus(it.Status),
				Note:   it.Note,
			})
		}
		return body
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil && strings.TrimSpace(text) != "" {
		body.Items = append(body.Items, types.PMReviewOpinionItem{
			Text:   strings.TrimSpace(text),
			Status: types.ReviewOpinionPending,
		})
	}
	return body
}

// normalizeOpinionStatus 统一历史状态写法，未知值按待处理
func normalizeOpinionStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case types.ReviewOpinionResolved, "done", "已解决":
		return types.ReviewOpinionResolved
	default:
		return types.ReviewOpinionPending
	}
}
