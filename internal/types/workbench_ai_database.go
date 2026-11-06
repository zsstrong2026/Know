package types

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// 数据库 AI（P3-5）的请求与补丁契约（实现方案 §11.7）。
//
// 服务端不解析 Y.Doc：列定义与行快照由前端随请求带上，模型只产出补丁，
// 真正的写入由前端在一个 BlockSuite 事务里执行。这样服务端既不持有
// 第二份数据库状态，也不需要读快照做反序列化。
//
// 补丁是 §11.6 `workbench.patch.v1` 的数据库子集，只保留三个 op：
// add_property（新建列）、add_row（新建行）、set_cell（写单元格）。

// 数据库 AI 的各项上限（§11.7）。
const (
	WorkbenchAIDatabasePromptMaxRunes   = 1000
	WorkbenchAIDatabaseMaxColumns       = 40
	WorkbenchAIDatabaseMaxRows          = 200
	WorkbenchAIDatabaseMaxOps           = 200
	WorkbenchAIDatabaseMaxCellRunes     = 500
	WorkbenchAIDatabaseMaxNameRunes     = 60
	WorkbenchAIDatabaseMaxOptionRunes   = 40
	WorkbenchAIDatabaseMaxOptionsPerCol = 50
)

// 数据库 AI 允许的三个 op。
const (
	WorkbenchAIDatabaseOpAddProperty = "add_property"
	WorkbenchAIDatabaseOpAddRow      = "add_row"
	WorkbenchAIDatabaseOpSetCell     = "set_cell"
)

// 允许新建的列类型（§11.7：不支持公式、关联、附件等）。
var workbenchAIDatabaseColumnTypes = []string{
	"text", "rich-text", "number", "date", "select", "multi-select", "checkbox",
}

var (
	ErrWorkbenchAIDatabaseEmptyPrompt = errors.New("workbench database ai: prompt is required")
	ErrWorkbenchAIDatabaseBadPatch    = errors.New("workbench database ai: model output is not a valid patch")
)

// 补丁里的 id 只允许这个字符集；与 §11.6 的 ID 约束一致。
var workbenchAIDatabaseIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// WorkbenchAIDatabaseColumn 是前端带上的列定义快照。
type WorkbenchAIDatabaseColumn struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// WorkbenchAIDatabaseRow 是前端带上的行快照；cells 的 key 是列 id。
type WorkbenchAIDatabaseRow struct {
	ID    string            `json:"id"`
	Title string            `json:"title"`
	Cells map[string]string `json:"cells"`
}

// WorkbenchAIDatabaseRequest 数据库 AI 的生成请求。
//
// 刻意不收 database_id：一个条目最多一个数据库块，归属由 item_id 决定。
type WorkbenchAIDatabaseRequest struct {
	ItemID  string `json:"item_id" binding:"required"`
	Prompt  string `json:"prompt" binding:"required"`
	AgentID string `json:"agent_id"`
	// Columns / Rows 是只读快照，不参与写入。
	Columns []WorkbenchAIDatabaseColumn `json:"columns"`
	Rows    []WorkbenchAIDatabaseRow    `json:"rows"`
	// Truncated 表示行快照被截断，仅用于提示模型“这不是全部数据”。
	Truncated bool `json:"truncated"`
}

// WorkbenchAIDatabasePatchOp 是一条补丁操作。
//
// 字段刻意做成“宽进”：模型可能用列名而不是列 id，也可能把行标题写在
// cells 里；前端在应用前统一解析成 id，服务端只校验结构与边界。
type WorkbenchAIDatabasePatchOp struct {
	Op string `json:"op"`

	// add_property
	Name    string   `json:"name,omitempty"`
	Type    string   `json:"type,omitempty"`
	Options []string `json:"options,omitempty"`

	// add_row
	Title string            `json:"title,omitempty"`
	Cells map[string]string `json:"cells,omitempty"`

	// set_cell
	RowID    string `json:"row_id,omitempty"`
	ColumnID string `json:"column_id,omitempty"`
	Column   string `json:"column,omitempty"`
	Value    string `json:"value,omitempty"`
}

// WorkbenchAIDatabasePatch 是模型产出的整包补丁；任一 op 非法则整包拒绝（§11.6）。
type WorkbenchAIDatabasePatch struct {
	Ops []WorkbenchAIDatabasePatchOp `json:"ops"`
}

// WorkbenchAIDatabaseResult 是生成结果：补丁 + 模型标识，供前端预览。
type WorkbenchAIDatabaseResult struct {
	Patch   WorkbenchAIDatabasePatch `json:"patch"`
	ModelID string                   `json:"model_id"`
	AgentID string                   `json:"agent_id,omitempty"`
	Usage   *TokenUsage              `json:"usage,omitempty"`
}

// Normalize 归一化请求：去空白并丢弃超过上限的行（快照只是参考材料）。
func (r *WorkbenchAIDatabaseRequest) Normalize() {
	if r == nil {
		return
	}
	r.ItemID = strings.TrimSpace(r.ItemID)
	r.Prompt = strings.TrimSpace(r.Prompt)
	r.AgentID = strings.TrimSpace(r.AgentID)
	if len(r.Columns) > WorkbenchAIDatabaseMaxColumns {
		r.Columns = r.Columns[:WorkbenchAIDatabaseMaxColumns]
	}
	if len(r.Rows) > WorkbenchAIDatabaseMaxRows {
		r.Rows = r.Rows[:WorkbenchAIDatabaseMaxRows]
		r.Truncated = true
	}
	for index := range r.Columns {
		r.Columns[index].ID = strings.TrimSpace(r.Columns[index].ID)
		r.Columns[index].Name = strings.TrimSpace(r.Columns[index].Name)
		r.Columns[index].Type = strings.TrimSpace(r.Columns[index].Type)
	}
	for index := range r.Rows {
		r.Rows[index].ID = strings.TrimSpace(r.Rows[index].ID)
		r.Rows[index].Title = strings.TrimSpace(r.Rows[index].Title)
	}
}

// Validate 校验提示词长度与快照规模；超限按 2402 处理。
func (r *WorkbenchAIDatabaseRequest) Validate() error {
	if r == nil || strings.TrimSpace(r.Prompt) == "" {
		return ErrWorkbenchAIDatabaseEmptyPrompt
	}
	if runeLen(r.Prompt) > WorkbenchAIDatabasePromptMaxRunes {
		return &WorkbenchAILengthError{
			Field: "prompt", Limit: WorkbenchAIDatabasePromptMaxRunes, Actual: runeLen(r.Prompt),
		}
	}
	return nil
}

// Validate 校验整包补丁：op 白名单、数量上限、文本长度。
//
// 存在性校验（列/行是否真的存在）交给前端，因为行快照可能被截断；
// 这里只拒绝“结构上不可能应用”的补丁，避免模型输出把前端炸掉。
func (p *WorkbenchAIDatabasePatch) Validate() error {
	if p == nil || len(p.Ops) == 0 {
		return fmt.Errorf("%w: patch has no ops", ErrWorkbenchAIDatabaseBadPatch)
	}
	if len(p.Ops) > WorkbenchAIDatabaseMaxOps {
		return fmt.Errorf("%w: too many ops (%d)", ErrWorkbenchAIDatabaseBadPatch, len(p.Ops))
	}
	newColumns, newRows := 0, 0
	seenColumns := make(map[string]struct{})
	for index, op := range p.Ops {
		if err := op.validate(index); err != nil {
			return err
		}
		switch op.Op {
		case WorkbenchAIDatabaseOpAddProperty:
			newColumns++
			key := strings.ToLower(strings.TrimSpace(op.Name))
			if _, duplicated := seenColumns[key]; duplicated {
				return fmt.Errorf("%w: duplicated column %q", ErrWorkbenchAIDatabaseBadPatch, op.Name)
			}
			seenColumns[key] = struct{}{}
		case WorkbenchAIDatabaseOpAddRow:
			newRows++
		}
	}
	if newColumns > WorkbenchAIDatabaseMaxColumns {
		return fmt.Errorf("%w: too many new columns (%d)", ErrWorkbenchAIDatabaseBadPatch, newColumns)
	}
	// 单次新建行数沿用 §11.7 的 100 行上限。
	if newRows > 100 {
		return fmt.Errorf("%w: too many new rows (%d)", ErrWorkbenchAIDatabaseBadPatch, newRows)
	}
	return nil
}

func (op WorkbenchAIDatabasePatchOp) validate(index int) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: ops[%d]: %s", ErrWorkbenchAIDatabaseBadPatch, index, fmt.Sprintf(format, args...))
	}
	switch op.Op {
	case WorkbenchAIDatabaseOpAddProperty:
		if strings.TrimSpace(op.Name) == "" {
			return fail("add_property requires name")
		}
		if runeLen(op.Name) > WorkbenchAIDatabaseMaxNameRunes {
			return fail("column name is too long")
		}
		if !isWorkbenchAIDatabaseColumnType(op.Type) {
			return fail("unsupported column type %q", op.Type)
		}
		if len(op.Options) > WorkbenchAIDatabaseMaxOptionsPerCol {
			return fail("too many options")
		}
		for _, option := range op.Options {
			if runeLen(option) > WorkbenchAIDatabaseMaxOptionRunes {
				return fail("option is too long")
			}
		}
		return nil
	case WorkbenchAIDatabaseOpAddRow:
		if runeLen(op.Title) > WorkbenchAIDatabaseMaxCellRunes {
			return fail("row title is too long")
		}
		if len(op.Cells) > WorkbenchAIDatabaseMaxColumns {
			return fail("too many cells")
		}
		for key, value := range op.Cells {
			if strings.TrimSpace(key) == "" {
				return fail("cell key is empty")
			}
			if runeLen(value) > WorkbenchAIDatabaseMaxCellRunes {
				return fail("cell %q value is too long", key)
			}
		}
		return nil
	case WorkbenchAIDatabaseOpSetCell:
		if !workbenchAIDatabaseIDPattern.MatchString(op.RowID) {
			return fail("set_cell requires a valid row_id")
		}
		column := strings.TrimSpace(op.ColumnID)
		if column == "" {
			column = strings.TrimSpace(op.Column)
		}
		if column == "" {
			return fail("set_cell requires column_id or column")
		}
		if runeLen(op.Value) > WorkbenchAIDatabaseMaxCellRunes {
			return fail("value is too long")
		}
		return nil
	default:
		return fail("unknown op %q", op.Op)
	}
}

func isWorkbenchAIDatabaseColumnType(name string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	for _, allowed := range workbenchAIDatabaseColumnTypes {
		if trimmed == allowed {
			return true
		}
	}
	return false
}
