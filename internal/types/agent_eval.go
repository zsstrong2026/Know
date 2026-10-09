package types

import (
	"database/sql/driver"
	"encoding/json"
	"time"
)

const (
	AgentEvalRunStatusRunning   = "running"
	AgentEvalRunStatusCompleted = "completed"
	AgentEvalRunStatusFailed    = "failed"

	AgentEvalCaseStatusPassed = "passed"
	AgentEvalCaseStatusFailed = "failed"

	AgentEvalTriggerManual = "manual"

	AgentEvalSuiteFunctional = "functional"
	AgentEvalSuiteSecurity   = "security"
)

// AgentEvalSessionMarker tags the sessions that agent smoke-test runs execute
// in. Each eval case drives a real agent transcript through a real session so
// the answer can be observed, but that transcript is infrastructure rather
// than a conversation, so the console list must never show it. This mirrors
// how skill maintenance sessions are classified (SkillMaintenanceSessionMarker):
// a description prefix, so no schema change is needed.
const AgentEvalSessionMarker = "agent_eval:"

type AgentEvalCredentials struct {
	Authorization string
	XAPIKey       string
	XTenantID     string
}

type AgentEvalAssertion struct {
	Name     string `json:"name"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Passed   bool   `json:"passed"`
}

type AgentEvalJudgeItem struct {
	Metric string  `json:"metric"`
	Score  float64 `json:"score,omitempty"`
	Reason string  `json:"reason,omitempty"`
	Error  string  `json:"error,omitempty"`
}

type AgentEvalToolCall struct {
	ToolName   string `json:"tool_name"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type AgentEvalAssertionList []AgentEvalAssertion

func (l AgentEvalAssertionList) Value() (driver.Value, error) {
	return json.Marshal(l)
}

func (l *AgentEvalAssertionList) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return nil
	}
	return json.Unmarshal(b, l)
}

type AgentEvalJudgeList []AgentEvalJudgeItem

func (l AgentEvalJudgeList) Value() (driver.Value, error) {
	return json.Marshal(l)
}

func (l *AgentEvalJudgeList) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return nil
	}
	return json.Unmarshal(b, l)
}

type AgentEvalToolCallList []AgentEvalToolCall

func (l AgentEvalToolCallList) Value() (driver.Value, error) {
	return json.Marshal(l)
}

func (l *AgentEvalToolCallList) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return nil
	}
	return json.Unmarshal(b, l)
}

type AgentEvalRun struct {
	ID         string     `yaml:"id" json:"run_id" gorm:"type:varchar(36);primaryKey"`
	TenantID   uint64     `yaml:"tenant_id" json:"tenant_id" gorm:"primaryKey"`
	AgentID    string     `yaml:"agent_id" json:"agent_id" gorm:"type:varchar(36)"`
	Status     string     `yaml:"status" json:"status" gorm:"type:varchar(16)"`
	Total      int        `yaml:"total" json:"total"`
	Passed     int        `yaml:"passed" json:"passed"`
	Failed     int        `yaml:"failed" json:"failed"`
	Error      string     `yaml:"error" json:"error,omitempty"`
	Trigger    string     `yaml:"trigger" json:"trigger" gorm:"column:eval_trigger;type:varchar(16)"`
	Suite      string     `yaml:"suite" json:"suite" gorm:"type:varchar(32)"`
	DurationMS int64      `yaml:"duration_ms" json:"duration_ms"`
	CreatedAt  time.Time  `yaml:"created_at" json:"created_at"`
	UpdatedAt  time.Time  `yaml:"updated_at" json:"updated_at"`
	FinishedAt *time.Time `yaml:"finished_at" json:"finished_at,omitempty"`
}

func (AgentEvalRun) TableName() string {
	return "agent_eval_runs"
}

type AgentEvalCaseResult struct {
	ID             string                 `yaml:"id" json:"id" gorm:"type:varchar(36);primaryKey"`
	RunID          string                 `yaml:"run_id" json:"run_id" gorm:"type:varchar(36);index"`
	TenantID       uint64                 `yaml:"tenant_id" json:"tenant_id"`
	CaseID         string                 `yaml:"case_id" json:"case_id" gorm:"type:varchar(32)"`
	CaseTitle      string                 `yaml:"case_title" json:"case_title" gorm:"type:varchar(255)"`
	Query          string                 `yaml:"query" json:"query" gorm:"type:text"`
	Status         string                 `yaml:"status" json:"status" gorm:"type:varchar(16)"`
	Assertions     AgentEvalAssertionList `yaml:"assertions" json:"assertions" gorm:"type:json"`
	Judge          AgentEvalJudgeList     `yaml:"judge" json:"judge" gorm:"type:json"`
	ToolCalls      AgentEvalToolCallList  `yaml:"tool_calls" json:"tool_calls" gorm:"type:json"`
	Answer         string                 `yaml:"answer" json:"answer" gorm:"type:text"`
	ReferenceCount int                    `yaml:"reference_count" json:"reference_count"`
	Error          string                 `yaml:"error" json:"error,omitempty"`
	DurationMS     int64                  `yaml:"duration_ms" json:"duration_ms"`
	CreatedAt      time.Time              `yaml:"created_at" json:"created_at"`
	UpdatedAt      time.Time              `yaml:"updated_at" json:"updated_at"`
}

func (AgentEvalCaseResult) TableName() string {
	return "agent_eval_case_results"
}
