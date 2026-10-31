package errors

import (
	"fmt"
	"net/http"
	"time"
)

// ErrorCode defines the error code type
type ErrorCode int

// System error codes
const (
	// Common error codes (1000-1999)
	ErrBadRequest         ErrorCode = 1000
	ErrUnauthorized       ErrorCode = 1001
	ErrForbidden          ErrorCode = 1002
	ErrNotFound           ErrorCode = 1003
	ErrMethodNotAllowed   ErrorCode = 1004
	ErrConflict           ErrorCode = 1005
	ErrTooManyRequests    ErrorCode = 1006
	ErrInternalServer     ErrorCode = 1007
	ErrServiceUnavailable ErrorCode = 1008
	ErrTimeout            ErrorCode = 1009
	ErrValidation         ErrorCode = 1010

	// Tenant related error codes (2000-2099)
	ErrTenantNotFound         ErrorCode = 2000
	ErrTenantAlreadyExists    ErrorCode = 2001
	ErrTenantInactive         ErrorCode = 2002
	ErrTenantNameRequired     ErrorCode = 2003
	ErrTenantInvalidStatus    ErrorCode = 2004
	ErrTenantCreationDisabled ErrorCode = 2005

	// Agent related error codes (2100-2199)
	ErrAgentMissingThinkingModel ErrorCode = 2100
	ErrAgentMissingAllowedTools  ErrorCode = 2101
	ErrAgentInvalidMaxIterations ErrorCode = 2102
	ErrAgentInvalidTemperature   ErrorCode = 2103

	// VectorStore binding related error codes (2200-2299).
	// Both map to HTTP 400 with a generic message; the typed code lets
	// clients distinguish "wrong UUID / cross-tenant" from "store exists
	// but is currently unavailable" without parsing the message text.
	ErrVectorStoreBindingInvalid ErrorCode = 2200
	ErrVectorStoreUnavailable    ErrorCode = 2201

	// Model lifecycle errors (2300-2399).
	// The dedicated code lets clients render structured usage details without
	// parsing the backwards-compatible English error message.
	ErrModelInUse ErrorCode = 2300

	// Workbench errors (2400-2499). 错误码是对外稳定契约，客户端按 code 分支，
	// 新增语义时请追加新值，不要复用已有 code。
	ErrWorkbenchNotFound        ErrorCode = 2400 // 404：条目不存在或不属于当前用户
	ErrWorkbenchVersionConflict ErrorCode = 2401 // 409：内容快照版本冲突
	ErrWorkbenchTooLarge        ErrorCode = 2402 // 413：内容超出体积上限
	ErrWorkbenchArchiveBusy     ErrorCode = 2404 // 409：同一归档分片正被另一请求处理
	ErrWorkbenchArchiveStale    ErrorCode = 2405 // 409：归档基线版本已过期
	ErrWorkbenchNoModel         ErrorCode = 2408 // 409：租户没有可用的聊天模型
	ErrWorkbenchDecodeFailed    ErrorCode = 2409 // 422：快照无法解码 / schema 版本不支持
	ErrWorkbenchStreamBroken    ErrorCode = 2410 // 502：模型流式回答中途中断
	ErrWorkbenchInvalidField    ErrorCode = 2411 // 400：日历/形态等字段非法（details.field 指明字段）
	ErrWorkbenchSystemKB        ErrorCode = 2412 // 400：系统知识库不允许普通用户操作

	// Add more error codes here
)

// AppError defines the application error structure
type AppError struct {
	Code     ErrorCode `json:"code"`
	Message  string    `json:"message"`
	Details  any       `json:"details,omitempty"`
	HTTPCode int       `json:"-"`
}

// Error implements the error interface
func (e *AppError) Error() string {
	return fmt.Sprintf("error code: %d, error message: %s", e.Code, e.Message)
}

// WithDetails adds error details
func (e *AppError) WithDetails(details any) *AppError {
	e.Details = details
	return e
}

// NewBadRequestError creates a bad request error
func NewBadRequestError(message string) *AppError {
	return &AppError{
		Code:     ErrBadRequest,
		Message:  message,
		HTTPCode: http.StatusBadRequest,
	}
}

// NewModelInUseError creates the typed HTTP 400 returned when active tenant
// resources still reference a model.
func NewModelInUseError(message string, details any) *AppError {
	return &AppError{
		Code:     ErrModelInUse,
		Message:  message,
		Details:  details,
		HTTPCode: http.StatusBadRequest,
	}
}

// NewUnauthorizedError creates an unauthorized error
func NewUnauthorizedError(message string) *AppError {
	return &AppError{
		Code:     ErrUnauthorized,
		Message:  message,
		HTTPCode: http.StatusUnauthorized,
	}
}

// NewForbiddenError creates a forbidden error
func NewForbiddenError(message string) *AppError {
	return &AppError{
		Code:     ErrForbidden,
		Message:  message,
		HTTPCode: http.StatusForbidden,
	}
}

// NewNotFoundError creates a not found error
func NewNotFoundError(message string) *AppError {
	return &AppError{
		Code:     ErrNotFound,
		Message:  message,
		HTTPCode: http.StatusNotFound,
	}
}

// NewConflictError creates a conflict error
func NewConflictError(message string) *AppError {
	return &AppError{
		Code:     ErrConflict,
		Message:  message,
		HTTPCode: http.StatusConflict,
	}
}

// NewTooManyRequestsError creates a 429 error, used by quota-style
// guards (e.g. per-user self-service tenant creation cap).
func NewTooManyRequestsError(message string) *AppError {
	if message == "" {
		message = "too many requests"
	}
	return &AppError{
		Code:     ErrTooManyRequests,
		Message:  message,
		HTTPCode: http.StatusTooManyRequests,
	}
}

// NewInternalServerError creates an internal server error
func NewInternalServerError(message string) *AppError {
	if message == "" {
		message = "服务器内部错误"
	}
	return &AppError{
		Code:     ErrInternalServer,
		Message:  message,
		HTTPCode: http.StatusInternalServerError,
	}
}

// NewServiceUnavailableError creates a service unavailable (503)
// error. Used for transient failures where the caller can retry.
func NewServiceUnavailableError(message string) *AppError {
	if message == "" {
		message = "服务暂时不可用"
	}
	return &AppError{
		Code:     ErrServiceUnavailable,
		Message:  message,
		HTTPCode: http.StatusServiceUnavailable,
	}
}

// NewValidationError creates a validation error
func NewValidationError(message string) *AppError {
	return &AppError{
		Code:     ErrValidation,
		Message:  message,
		HTTPCode: http.StatusBadRequest,
	}
}

// Tenant related errors
func NewTenantNotFoundError() *AppError {
	return &AppError{
		Code:     ErrTenantNotFound,
		Message:  "空间不存在",
		HTTPCode: http.StatusNotFound,
	}
}

// NewTenantAlreadyExistsError creates a tenant already exists error
func NewTenantAlreadyExistsError() *AppError {
	return &AppError{
		Code:     ErrTenantAlreadyExists,
		Message:  "空间已存在",
		HTTPCode: http.StatusConflict,
	}
}

// NewTenantInactiveError creates a tenant inactive error
func NewTenantInactiveError() *AppError {
	return &AppError{
		Code:     ErrTenantInactive,
		Message:  "空间已停用",
		HTTPCode: http.StatusForbidden,
	}
}

// NewTenantCreationDisabledError reports a deployment-policy denial for
// ordinary self-service tenant creation.
func NewTenantCreationDisabledError() *AppError {
	return &AppError{
		Code:     ErrTenantCreationDisabled,
		Message:  "self-service workspace creation is disabled; join a workspace by invitation",
		HTTPCode: http.StatusForbidden,
	}
}

// Agent related errors
func NewAgentMissingThinkingModelError() *AppError {
	return &AppError{
		Code:     ErrAgentMissingThinkingModel,
		Message:  "启用Agent模式前，请先选择思考模型",
		HTTPCode: http.StatusBadRequest,
	}
}

func NewAgentMissingAllowedToolsError() *AppError {
	return &AppError{
		Code:     ErrAgentMissingAllowedTools,
		Message:  "至少需要选择一个允许的工具",
		HTTPCode: http.StatusBadRequest,
	}
}

func NewAgentInvalidMaxIterationsError() *AppError {
	return &AppError{
		Code:     ErrAgentInvalidMaxIterations,
		Message:  "最大迭代次数须为正整数，或 -1 表示不限制",
		HTTPCode: http.StatusBadRequest,
	}
}

func NewAgentInvalidTemperatureError() *AppError {
	return &AppError{
		Code:     ErrAgentInvalidTemperature,
		Message:  "温度参数必须在0-2之间",
		HTTPCode: http.StatusBadRequest,
	}
}

// NewVectorStoreBindingInvalidError signals that a knowledge base create
// request referenced a vector store that does not exist under the caller's
// tenant (or carried a malformed UUID). The user-facing message is
// intentionally generic to avoid enumeration oracles — see the structured
// log at the call site for the tenant/store pair.
func NewVectorStoreBindingInvalidError(message string) *AppError {
	if message == "" {
		message = "vector store not found"
	}
	return &AppError{
		Code:     ErrVectorStoreBindingInvalid,
		Message:  message,
		HTTPCode: http.StatusBadRequest,
	}
}

// NewVectorStoreUnavailableError signals that the requested vector store
// row exists in the database but is not currently wired into the in-memory
// engine registry (factory failure on CreateStore, dynamic config error,
// or stale state after a connection-config rotation).
func NewVectorStoreUnavailableError(message string) *AppError {
	if message == "" {
		message = "vector store is currently unavailable"
	}
	return &AppError{
		Code:     ErrVectorStoreUnavailable,
		Message:  message,
		HTTPCode: http.StatusBadRequest,
	}
}

// Workbench 相关错误构造器。handler 不手拼响应体，统一交给 ErrorHandler 渲染。

// NewWorkbenchNotFoundError 生成 2400/404。不存在与越权共用同一个错误，
// 避免通过 code 差异探测他人的条目是否存在。
func NewWorkbenchNotFoundError(message string) *AppError {
	if message == "" {
		message = "workbench item not found"
	}
	return &AppError{
		Code:     ErrWorkbenchNotFound,
		Message:  message,
		HTTPCode: http.StatusNotFound,
	}
}

// NewWorkbenchVersionConflict 生成 2401/409，details 携带当前版本与更新时间，
// 供前端做“重载后重试”，而不是让用户手工刷新页面。
func NewWorkbenchVersionConflict(currentVersion int, updatedAt time.Time) *AppError {
	details := map[string]any{"current_version": currentVersion}
	if !updatedAt.IsZero() {
		details["updated_at"] = updatedAt.UTC()
	}
	return &AppError{
		Code:     ErrWorkbenchVersionConflict,
		Message:  "Workbench content has been updated by another tab",
		Details:  details,
		HTTPCode: http.StatusConflict,
	}
}

// NewWorkbenchTooLarge 生成 2402/413。details 只放数值，不放正文。
func NewWorkbenchTooLarge(limit, actual int64) *AppError {
	return &AppError{
		Code:     ErrWorkbenchTooLarge,
		Message:  "workbench content exceeds the size limit",
		Details:  map[string]any{"limit": limit, "actual": actual},
		HTTPCode: http.StatusRequestEntityTooLarge,
	}
}

// NewWorkbenchArchiveInProgress 生成 2404/409：同一 part 已有 pending 行，
// 说明另一个标签页/请求正在归档它，调用方应稍后重试而不是并发写知识。
func NewWorkbenchArchiveInProgress(retryAfter time.Duration) *AppError {
	ms := int64(retryAfter / time.Millisecond)
	if ms <= 0 {
		ms = 1000
	}
	return &AppError{
		Code:     ErrWorkbenchArchiveBusy,
		Message:  "workbench archive is being processed by another request",
		Details:  map[string]any{"retry_after_ms": ms},
		HTTPCode: http.StatusConflict,
	}
}

// NewWorkbenchArchiveVersionStale 生成 2405/409：归档请求带的 base_version
// 落后于源内容，details 带当前版本供前端提示“内容已更新，请重新归档”。
func NewWorkbenchArchiveVersionStale(currentVersion int) *AppError {
	return &AppError{
		Code:     ErrWorkbenchArchiveStale,
		Message:  "workbench archive source is newer than the request base version",
		Details:  map[string]any{"current_version": currentVersion},
		HTTPCode: http.StatusConflict,
	}
}

// NewWorkbenchNoModel 生成 2408/409：租户没有可用的聊天模型。
// 前端据此展示“去配置模型”入口，而不是反复重试同一个必然失败的请求。
func NewWorkbenchNoModel() *AppError {
	return &AppError{
		Code:     ErrWorkbenchNoModel,
		Message:  "no chat model available for workbench AI",
		Details:  map[string]any{"reason": "no_chat_model"},
		HTTPCode: http.StatusConflict,
	}
}

// NewWorkbenchStreamBroken 生成 2410/502：流已经开始，但上游 provider 中途断开。
// 前端保留已生成的部分文本供重试，不自动写入。
func NewWorkbenchStreamBroken(message string) *AppError {
	if message == "" {
		message = "model stream interrupted"
	}
	return &AppError{
		Code:     ErrWorkbenchStreamBroken,
		Message:  message,
		HTTPCode: http.StatusBadGateway,
	}
}

// NewWorkbenchSchemaUnsupported 生成 2409/422，用于快照解码失败或 schema 版本过高。
func NewWorkbenchSchemaUnsupported(supported int) *AppError {
	return &AppError{
		Code:     ErrWorkbenchDecodeFailed,
		Message:  "workbench snapshot cannot be decoded or its schema version is unsupported",
		Details:  map[string]any{"supported_schema_version": supported},
		HTTPCode: http.StatusUnprocessableEntity,
	}
}

// NewWorkbenchInvalidField 生成 2411/400，用于 journal_date / current_mode 等
// 单字段语义非法。details.field 让前端能定位到具体输入框，而不是笼统报错。
func NewWorkbenchInvalidField(field, expected string) *AppError {
	return &AppError{
		Code:     ErrWorkbenchInvalidField,
		Message:  fmt.Sprintf("invalid workbench field: %s", field),
		Details:  map[string]any{"field": field, "expected": expected},
		HTTPCode: http.StatusBadRequest,
	}
}

// NewWorkbenchSystemKBError 生成 2412/400：系统知识库（工作知识库）不接受
// 普通用户的删除/改名/移动；只有内部清理流程能处理它。
func NewWorkbenchSystemKBError(message string) *AppError {
	if message == "" {
		message = "system knowledge base cannot be modified"
	}
	return &AppError{
		Code:     ErrWorkbenchSystemKB,
		Message:  message,
		Details:  map[string]any{"reason": "system_knowledge_base"},
		HTTPCode: http.StatusBadRequest,
	}
}

// IsAppError checks if the error is an AppError type
func IsAppError(err error) (*AppError, bool) {
	appErr, ok := err.(*AppError)
	return appErr, ok
}
