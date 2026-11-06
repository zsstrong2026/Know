package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkbenchTagRequest_Validate(t *testing.T) {
	req := &WorkbenchTagRequest{Name: "   "}
	req.Normalize()
	assert.ErrorIs(t, req.Validate(), ErrWorkbenchTagNameEmpty)

	req = &WorkbenchTagRequest{Name: strings.Repeat("标", WorkbenchTagNameMaxRunes+1)}
	req.Normalize()
	err := req.Validate()
	require.Error(t, err)
	var lengthErr *WorkbenchAILengthError
	require.ErrorAs(t, err, &lengthErr)
	assert.Equal(t, "name", lengthErr.Field)

	// 未指定颜色时落默认色；大小写与空白都归一
	req = &WorkbenchTagRequest{Name: " 紧急 ", Color: " BLUE "}
	req.Normalize()
	assert.Equal(t, "紧急", req.Name)
	assert.Equal(t, "blue", req.Color)
	require.NoError(t, req.Validate())

	// 白名单外的颜色直接拒
	bad := &WorkbenchTagRequest{Name: "x", Color: "#ff0000"}
	bad.Normalize()
	assert.ErrorIs(t, bad.Validate(), ErrWorkbenchTagColor)
}

func TestNormalizeItemTags(t *testing.T) {
	// 去空白、去重、保序
	assert.Equal(t, []string{"a", "b"}, NormalizeItemTags([]string{" a ", "b", "a", "  "}))
	assert.Empty(t, NormalizeItemTags(nil))

	// 超过上限截断：防止一次请求把条目挂满
	ids := make([]string, 0, WorkbenchTagsPerItemLimit+5)
	for index := 0; index < WorkbenchTagsPerItemLimit+5; index++ {
		ids = append(ids, "tag-"+string(rune('a'+index%26))+string(rune('a'+index/26)))
	}
	assert.Len(t, NormalizeItemTags(ids), WorkbenchTagsPerItemLimit)
}

func TestIsWorkbenchTagColor(t *testing.T) {
	assert.True(t, IsWorkbenchTagColor(WorkbenchTagColorDefault))
	assert.True(t, IsWorkbenchTagColor("purple"))
	assert.False(t, IsWorkbenchTagColor(""))
	assert.False(t, IsWorkbenchTagColor("rgb(0,0,0)"))
}
