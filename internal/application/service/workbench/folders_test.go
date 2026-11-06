package workbench

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// planFolderOrder 是拖拽排序的纯逻辑：先摘掉自己，再夹下标，最后插入。
// 拖拽最容易出错的就是「自己还在列表里」和「下标越界」，这里把两类边界都钉住。
func TestPlanFolderOrder(t *testing.T) {
	siblings := []string{"a", "b", "c"}

	t.Run("拖到中间", func(t *testing.T) {
		// 把 c 拖到 a 之后：排除自己后是 [a,b]，位置 1
		assert.Equal(t, []string{"a", "c", "b"}, planFolderOrder(siblings, "c", 1))
	})

	t.Run("拖到最前", func(t *testing.T) {
		assert.Equal(t, []string{"c", "a", "b"}, planFolderOrder(siblings, "c", 0))
	})

	t.Run("拖到最后（越界夹紧）", func(t *testing.T) {
		assert.Equal(t, []string{"a", "b", "c"}, planFolderOrder(siblings, "c", 99))
	})

	t.Run("负数下标表示追加到末尾", func(t *testing.T) {
		assert.Equal(t, []string{"a", "c", "b"}, planFolderOrder(siblings, "b", -1))
	})

	t.Run("原位拖回原位不产生重复", func(t *testing.T) {
		// b 在列表里的位置是 1；排除自己后插回 1 应该得到原顺序
		assert.Equal(t, []string{"a", "b", "c"}, planFolderOrder(siblings, "b", 1))
	})

	t.Run("跨层拖进空层", func(t *testing.T) {
		assert.Equal(t, []string{"b"}, planFolderOrder([]string{"b"}, "b", 5))
	})

	t.Run("目标层没有其它文件夹", func(t *testing.T) {
		assert.Equal(t, []string{"x"}, planFolderOrder(nil, "x", 0))
	})
}

func TestNormalizeFolderParent(t *testing.T) {
	assert.Nil(t, normalizeFolderParent(nil))
	empty := ""
	assert.Nil(t, normalizeFolderParent(&empty), "空串必须归一成根层级，不能写成空外键")
	spaces := "   "
	assert.Nil(t, normalizeFolderParent(&spaces))

	id := " folder-1 "
	got := normalizeFolderParent(&id)
	assert.NotNil(t, got)
	assert.Equal(t, "folder-1", *got, "两端的空白要去掉，否则查不到父级")
}

func TestSameFolderParent(t *testing.T) {
	a, b, other := "a", "a", "b"
	assert.True(t, sameFolderParent(nil, nil))
	assert.False(t, sameFolderParent(&a, nil))
	assert.False(t, sameFolderParent(nil, &a))
	assert.True(t, sameFolderParent(&a, &b), "值相同即视为同一父级")
	assert.False(t, sameFolderParent(&a, &other))
}
