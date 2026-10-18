package service

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestNormalizeMeetingArtifact(t *testing.T) {
	// 历史行（迁移前数据）：kind/status 为空 → 兜底 solution/ready
	legacy := &types.MeetingArtifact{ID: "a1", SolutionMessageID: "m1", Version: 1}
	out := types.NormalizeMeetingArtifact(legacy)
	assert.Equal(t, types.MeetingArtifactKindSolution, out.Kind)
	assert.Equal(t, types.MeetingArtifactStatusReady, out.Status)

	// 新行原样保留
	fresh := &types.MeetingArtifact{
		Kind: types.MeetingArtifactKindGenerated, KindType: types.MeetingArtifactTypeDocx,
		Status: types.MeetingArtifactStatusGenerating,
	}
	out2 := types.NormalizeMeetingArtifact(fresh)
	assert.Equal(t, types.MeetingArtifactKindGenerated, out2.Kind)
	assert.Equal(t, types.MeetingArtifactStatusGenerating, out2.Status)
}

func TestValidMeetingArtifactType(t *testing.T) {
	for _, k := range []string{
		types.MeetingArtifactTypeReportMarkdown, types.MeetingArtifactTypeMinutes,
		types.MeetingArtifactTypeTodoList, types.MeetingArtifactTypeDocx, types.MeetingArtifactTypeXlsx,
		types.MeetingArtifactTypePptx, types.MeetingArtifactTypeHtmlReport, types.MeetingArtifactTypeChartImage,
	} {
		assert.True(t, types.ValidMeetingArtifactType(k), k)
	}
	for _, bad := range []string{"", "pdf", "docx ", "Solution"} {
		assert.False(t, types.ValidMeetingArtifactType(bad), bad)
	}
}
