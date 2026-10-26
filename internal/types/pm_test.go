package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

// 需求状态机：合法迁移必须放行，非法迁移必须拒绝
func TestRequirementStatusTransitions(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{RequirementStatusPendingAnalysis, RequirementStatusConfirmed, true},
		{RequirementStatusPendingAnalysis, RequirementStatusRejected, true},
		{RequirementStatusConfirmed, RequirementStatusScheduled, true},
		{RequirementStatusScheduled, RequirementStatusDeveloping, true},
		{RequirementStatusDeveloping, RequirementStatusLaunched, true},
		// 终止态重新激活
		{RequirementStatusRejected, RequirementStatusPendingAnalysis, true},
		{RequirementStatusClosed, RequirementStatusPendingAnalysis, true},
		// 非法：不能从待分析直接上线
		{RequirementStatusPendingAnalysis, RequirementStatusLaunched, false},
		// 非法：已上线不能回到待分析（必须经 closed）
		{RequirementStatusLaunched, RequirementStatusPendingAnalysis, false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, CanTransitRequirement(c.from, c.to), "%s -> %s", c.from, c.to)
	}
}

// 版本状态机：定稿是分水岭，定稿后不允许回评审中
func TestVersionStatusTransitions(t *testing.T) {
	assert.True(t, CanTransitVersion(VersionStatusPlanning, VersionStatusReviewing))
	assert.True(t, CanTransitVersion(VersionStatusReviewing, VersionStatusFinalized))
	assert.True(t, CanTransitVersion(VersionStatusFinalized, VersionStatusDeveloping))
	assert.True(t, CanTransitVersion(VersionStatusDeveloping, VersionStatusLaunched))
	assert.True(t, CanTransitVersion(VersionStatusShelved, VersionStatusPlanning))
	assert.False(t, CanTransitVersion(VersionStatusFinalized, VersionStatusReviewing))
	assert.False(t, CanTransitVersion(VersionStatusLaunched, VersionStatusDeveloping))
}

// 枚举校验：非法值必须被拒绝，避免脏数据入库
func TestPMEnumValidation(t *testing.T) {
	assert.True(t, IsValidRequirementPriority("P0"))
	assert.False(t, IsValidRequirementPriority("P9"))
	assert.True(t, IsValidRequirementSource("customer"))
	assert.False(t, IsValidRequirementSource("unknown"))
	assert.True(t, IsValidVersionConclusion("conditional"))
	assert.False(t, IsValidVersionConclusion("passed"))
}

// 版本号生成：定稿时按 主.次.修订 递增
func TestNextVersionNo(t *testing.T) {
	assert.Equal(t, "1.0.0", NextVersionNo("", 0))
	assert.Equal(t, "1.1.0", NextVersionNo("1.0.0", 1))
	assert.Equal(t, "1.2.0", NextVersionNo("1.1.0", 1))
	assert.Equal(t, "1.2.1", NextVersionNo("1.2.0", 2))
}

// JSON 字段：types.JSON 必须能双向序列化并原样回读
func TestPMJSONFieldRoundTrip(t *testing.T) {
	raw := json.RawMessage(`{"value":"用户留存提升","risk":["依赖埋点"]}`)
	f := JSON(raw)
	b, err := f.MarshalJSON()
	assert.NoError(t, err)
	assert.JSONEq(t, string(raw), string(b))
}
