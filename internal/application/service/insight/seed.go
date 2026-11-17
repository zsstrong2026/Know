package insight

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// builtinThemeSeed 是内置主题的静态定义。ID 固定，便于幂等种入与自我洞悉门槛统计。
type builtinThemeSeed struct {
	ID           string
	Name         string
	Description  string
	Sensitive    bool
	Glyph        string
	Instructions string
	Dimensions   []types.InsightDimension
}

// builtinThemes 是 6 个内置主题：思维模式、价值观、思维陷阱、价值澄清、MBTI、主要矛盾。
var builtinThemes = []builtinThemeSeed{
	{
		ID:          "insight-builtin-thinking",
		Name:        "思维模式",
		Description: "从归档文档里看你如何摄取信息、如何推理、在哪一层抽象上思考",
		Glyph:       "i-spark",
		Instructions: "只根据原文中出现的推理过程、问题拆解与结论下判断。" +
			"区分“作者这样写”与“你据此推断”，推断必须标注 evidence_type=inference。" +
			"不要用性格标签替代证据。",
		Dimensions: []types.InsightDimension{
			{Name: "信息摄取", Criteria: "更偏广度扫描还是单点深挖，是否主动寻找反例", Scale: "0-100"},
			{Name: "推理方式", Criteria: "更依赖归纳类比还是演绎拆解，论证是否有链条", Scale: "0-100"},
			{Name: "抽象层级", Criteria: "停留在具体操作还是能上升到原则与模型", Scale: "0-100"},
			{Name: "决策速度", Criteria: "在信息不足时倾向于快速定调还是反复验证", Scale: "0-100"},
		},
	},
	{
		ID:          "insight-builtin-values",
		Name:        "价值观",
		Description: "识别你在成就、安全、自由、关系之间的真实排序与代价承受",
		Sensitive:   true,
		Glyph:       "i-star",
		Instructions: "价值排序只看“实际选择与代价”，不看口号式表达。" +
			"优先引用涉及取舍、放弃、投入成本的段落；没有取舍证据的维度留空。" +
			"不要给出道德评判。",
		Dimensions: []types.InsightDimension{
			{Name: "成就", Criteria: "对结果、影响力与可衡量进展的重视程度", Scale: "0-100"},
			{Name: "安全", Criteria: "对确定性、稳定与风险兜底的重视程度", Scale: "0-100"},
			{Name: "自由", Criteria: "对自主权、选择空间与不受约束的重视程度", Scale: "0-100"},
			{Name: "关系", Criteria: "对亲密、归属与他人评价的重视程度", Scale: "0-100"},
		},
	},
	{
		ID:          "insight-builtin-traps",
		Name:        "思维陷阱",
		Description: "找出反复出现的认知偏差：过度概括、二元对立、确认偏误、灾难化",
		Glyph:       "i-info",
		Instructions: "先定位偏差的原句，再说明它如何影响判断。" +
			"同一句话不要重复计入多个陷阱；证据不足时宁可少报。" +
			"提醒语气要克制，不做人格诊断。",
		Dimensions: []types.InsightDimension{
			{Name: "过度概括", Criteria: "用“总是/从不/所有人”等绝对化表述替代具体事实", Scale: "0-100"},
			{Name: "二元对立", Criteria: "把连续光谱压缩成非黑即白的选择", Scale: "0-100"},
			{Name: "确认偏误", Criteria: "只收集支持既有结论的信息，忽略反例", Scale: "0-100"},
			{Name: "灾难化", Criteria: "把小概率负面结果推演成必然结局", Scale: "0-100"},
		},
	},
	{
		ID:          "insight-builtin-clarify",
		Name:        "价值澄清",
		Description: "把模糊的“我想要”拆成可排序、可付代价、可长期坚持的选择",
		Sensitive:   true,
		Glyph:       "i-layers",
		Instructions: "围绕真实取舍澄清价值：你愿意为之放弃什么、能坚持多久。" +
			"引用必须来自原文中的选择或计划；不要把建议写成结论。" +
			"遇到相互冲突的价值时，指出冲突而不是替用户裁决。",
		Dimensions: []types.InsightDimension{
			{Name: "价值排序", Criteria: "多个价值冲突时的实际优先级是否清晰", Scale: "0-100"},
			{Name: "代价承受", Criteria: "是否明确愿意为所选价值付出什么成本", Scale: "0-100"},
			{Name: "长期一致性", Criteria: "选择在不同时间、不同场景下是否保持一致", Scale: "0-100"},
			{Name: "取舍边界", Criteria: "能否说清楚“不做什么”与底线在哪里", Scale: "0-100"},
		},
	},
	{
		ID:          "insight-builtin-mbti",
		Name:        "MBTI 倾向分析",
		Description: "从行为证据估计 E/I、S/N、T/F、J/P 四组倾向，只作参考不作定论",
		Sensitive:   true,
		Glyph:       "i-table",
		Instructions: "这是倾向估计而不是类型判定：只描述证据支持的方向与强度。" +
			"每组维度都必须给出原文依据；证据不足时明确说“倾向不明显”。" +
			"不要输出四字母类型标签，也不要用类型解释一切。",
		Dimensions: []types.InsightDimension{
			{Name: "能量来源", Criteria: "独处恢复还是与人互动恢复，偏 E 还是偏 I", Scale: "0-100"},
			{Name: "信息获取", Criteria: "关注具体事实还是模式可能性，偏 S 还是偏 N", Scale: "0-100"},
			{Name: "决策方式", Criteria: "优先逻辑一致性还是关系影响，偏 T 还是偏 F", Scale: "0-100"},
			{Name: "生活取向", Criteria: "偏好计划收束还是开放探索，偏 J 还是偏 P", Scale: "0-100"},
		},
	},
	{
		ID:          "insight-builtin-conflict",
		Name:        "主要矛盾",
		Description: "定位当前阶段最消耗你的那组矛盾，以及它在文档里的反复出现方式",
		Glyph:       "i-clock",
		Instructions: "主要矛盾必须是“两个都重要、但当下互相挤压”的目标或角色。" +
			"用反复出现的场景证明它不是一次性情绪；区分结构性矛盾与暂时困难。" +
			"结论要指向可调整的杠杆，而不是泛泛的“要平衡”。",
		Dimensions: []types.InsightDimension{
			{Name: "目标与资源", Criteria: "想做的事与时间、精力、能力之间的缺口", Scale: "0-100"},
			{Name: "角色与期待", Criteria: "不同角色要求之间的冲突与自我期待落差", Scale: "0-100"},
			{Name: "短期与长期", Criteria: "当下收益与长期方向的拉扯程度", Scale: "0-100"},
			{Name: "价值与代价", Criteria: "坚持某种价值所付出的现实代价", Scale: "0-100"},
		},
	},
}

// seedBuiltinThemes 幂等种入内置主题：已存在的跳过，并发冲突时回读确认。
func (s *Service) seedBuiltinThemes(ctx context.Context, tenantID uint64, ownerID string) error {
	existing, err := s.repo.ListThemes(ctx, tenantID, ownerID)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(existing))
	for _, theme := range existing {
		seen[theme.ID] = struct{}{}
	}
	now := time.Now()
	for _, seed := range builtinThemes {
		if _, ok := seen[seed.ID]; ok {
			continue
		}
		theme := &types.InsightTheme{
			ID:           seed.ID,
			Name:         seed.Name,
			Description:  seed.Description,
			Builtin:      true,
			Sensitive:    seed.Sensitive,
			Glyph:        seed.Glyph,
			Instructions: seed.Instructions,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := theme.SetDimensions(seed.Dimensions); err != nil {
			return err
		}
		if err := s.repo.CreateTheme(ctx, tenantID, ownerID, theme); err != nil {
			// 并发首次请求可能同时种入；只要回读得到就视为成功。
			if _, getErr := s.repo.GetTheme(ctx, tenantID, ownerID, seed.ID); getErr != nil {
				return err
			}
		}
	}
	return nil
}
