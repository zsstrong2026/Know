package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestParseAgentEvalSuite(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newContext := func(rawQuery string) (*gin.Context, *httptest.ResponseRecorder) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/?"+rawQuery, nil)
		return c, recorder
	}

	// 缺省 → functional
	c, _ := newContext("")
	suite, ok := parseAgentEvalSuite(c)
	assert.True(t, ok)
	assert.Equal(t, "functional", suite)

	// 显式 functional
	c, _ = newContext("suite=functional")
	suite, ok = parseAgentEvalSuite(c)
	assert.True(t, ok)
	assert.Equal(t, "functional", suite)

	// security 合法
	c, _ = newContext("suite=security")
	suite, ok = parseAgentEvalSuite(c)
	assert.True(t, ok)
	assert.Equal(t, "security", suite)

	// 非法值 → 400 且 ok=false
	c, recorder := newContext("suite=evil")
	suite, ok = parseAgentEvalSuite(c)
	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "invalid eval suite")

	// 大小写不放开（白名单精确匹配）
	c, recorder = newContext("suite=Security")
	_, ok = parseAgentEvalSuite(c)
	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
}
