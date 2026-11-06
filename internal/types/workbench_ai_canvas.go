package types

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// 画布 AI（P2-6）的请求与补丁契约。
//
// 与数据库 AI（§11.7）同构：服务端不解析 Y.Doc —— 画布上的元素快照由前端随
// 请求带上，模型只产出补丁，真正的写入由前端在一个 BlockSuite 事务里执行。
// 这样服务端既不需要反序列化快照，也不会持有第二份画布状态。
//
// 补丁是 `workbench.patch.v1` 的画布子集，只保留两个 op：
// create_frame（新建 Frame 并把元素收进去）与 move_element（平移元素）。
// 刻意不支持删除、改尺寸、改样式：整理画布不该有破坏性。
const (
	WorkbenchAICanvasPromptMaxRunes   = 1000
	WorkbenchAICanvasMaxElements      = 300
	WorkbenchAICanvasMaxFrames        = 30
	WorkbenchAICanvasMaxOps           = 60
	WorkbenchAICanvasMaxMembersPerOp  = 40
	WorkbenchAICanvasMaxTitleRunes    = 40
	WorkbenchAICanvasMaxTextRunes     = 200
	WorkbenchAICanvasCoordinateLimit  = 100000.0
	WorkbenchAICanvasMinFrameSize     = 80.0
	WorkbenchAICanvasMaxFrameSize     = 20000.0
	WorkbenchAICanvasOpCreateFrame    = "create_frame"
	WorkbenchAICanvasOpMoveElement    = "move_element"
	WorkbenchAICanvasElementKindNote  = "note"
	WorkbenchAICanvasElementKindFrame = "frame"
)

var (
	ErrWorkbenchAICanvasEmptyPrompt = errors.New("workbench canvas ai: prompt is required")
	ErrWorkbenchAICanvasBadPatch    = errors.New("workbench canvas ai: model output is not a valid patch")
)

// 补丁里的 id 只允许这个字符集；与数据库 AI 的 ID 约束一致。
var workbenchAICanvasIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// WorkbenchAICanvasElement 是前端带上来的一个画布元素快照。
//
// Kind 只区分模型需要知道的类别（note/frame/其它），不做等价于上游 flavour 的枚举，
// 避免上游加新元素类型时这里要跟着改。
type WorkbenchAICanvasElement struct {
	ID      string  `json:"id"`
	Kind    string  `json:"kind"`
	Text    string  `json:"text"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	W       float64 `json:"w"`
	H       float64 `json:"h"`
	FrameID string  `json:"frame_id"`
}

// WorkbenchAICanvasFrame 是已存在的 Frame 快照（标题 + 位置），用于让模型知道
// 哪些区域已经被占用，不要把新 Frame 摞在上面。
type WorkbenchAICanvasFrame struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
}

// WorkbenchAICanvasRequest 画布 AI 的生成请求。
type WorkbenchAICanvasRequest struct {
	ItemID  string `json:"item_id" binding:"required"`
	Prompt  string `json:"prompt" binding:"required"`
	AgentID string `json:"agent_id"`
	// Elements / Frames 是只读快照，不参与写入。
	Elements []WorkbenchAICanvasElement `json:"elements"`
	Frames   []WorkbenchAICanvasFrame   `json:"frames"`
	// Truncated 表示元素快照被截断，仅用于提示模型“这不是全部内容”。
	Truncated bool `json:"truncated"`
}

// WorkbenchAICanvasPatchOp 是一条画布补丁操作。
//
// 字段做成“宽进”：模型常常只给 members 里的标题而不是 id，前端会按标题回退匹配；
// 服务端只校验结构与数值边界。
type WorkbenchAICanvasPatchOp struct {
	Op string `json:"op"`

	// create_frame：新 Frame 的标题、位置尺寸与被收进去的元素
	Title     string   `json:"title,omitempty"`
	X         float64  `json:"x,omitempty"`
	Y         float64  `json:"y,omitempty"`
	W         float64  `json:"w,omitempty"`
	H         float64  `json:"h,omitempty"`
	MemberIDs []string `json:"member_ids,omitempty"`

	// move_element：目标元素与新位置
	ID string `json:"id,omitempty"`
}

// WorkbenchAICanvasPatch 模型输出的整包补丁。
type WorkbenchAICanvasPatch struct {
	Ops []WorkbenchAICanvasPatchOp `json:"ops"`
}

// WorkbenchAICanvasResult 返回给前端的补丁与模型信息。
type WorkbenchAICanvasResult struct {
	Patch   WorkbenchAICanvasPatch `json:"patch"`
	ModelID string                 `json:"model_id,omitempty"`
	AgentID string                 `json:"agent_id,omitempty"`
	Usage   *TokenUsage            `json:"usage,omitempty"`
}

// Normalize 收敛空白与截断，保证后续校验只看“清洗后”的值。
func (r *WorkbenchAICanvasRequest) Normalize() {
	r.Prompt = strings.TrimSpace(r.Prompt)
	if len(r.Elements) > WorkbenchAICanvasMaxElements {
		r.Elements = r.Elements[:WorkbenchAICanvasMaxElements]
		r.Truncated = true
	}
	if len(r.Frames) > WorkbenchAICanvasMaxFrames {
		r.Frames = r.Frames[:WorkbenchAICanvasMaxFrames]
		r.Truncated = true
	}
	for i := range r.Elements {
		r.Elements[i].Text = truncateRunes(r.Elements[i].Text, WorkbenchAICanvasMaxTextRunes)
	}
}

// Validate 校验请求本身的边界（不含补丁）。
func (r WorkbenchAICanvasRequest) Validate() error {
	if strings.TrimSpace(r.Prompt) == "" {
		return ErrWorkbenchAICanvasEmptyPrompt
	}
	// 与内联车道同口径：超长走 WorkbenchAILengthError → 2402，前端能提示“内容过长”。
	if actual := len([]rune(r.Prompt)); actual > WorkbenchAICanvasPromptMaxRunes {
		return &WorkbenchAILengthError{
			Field:  "prompt",
			Limit:  WorkbenchAICanvasPromptMaxRunes,
			Actual: actual,
		}
	}
	return nil
}

// Validate 校验补丁：op 白名单、数量上限、坐标与尺寸边界、id 字符集。
//
// 不校验“元素是否存在”——那是前端的责任（只有前端知道真实 store）。
// 服务端这一层的目标是：绝不让一个畸形补丁流到前端去写 store。
func (p WorkbenchAICanvasPatch) Validate() error {
	if len(p.Ops) == 0 {
		return nil
	}
	if len(p.Ops) > WorkbenchAICanvasMaxOps {
		return fmt.Errorf("%w: too many ops", ErrWorkbenchAICanvasBadPatch)
	}
	for index, op := range p.Ops {
		switch op.Op {
		case WorkbenchAICanvasOpCreateFrame:
			if len([]rune(op.Title)) > WorkbenchAICanvasMaxTitleRunes {
				return fmt.Errorf("%w: op %d title too long", ErrWorkbenchAICanvasBadPatch, index)
			}
			if op.W < WorkbenchAICanvasMinFrameSize || op.H < WorkbenchAICanvasMinFrameSize ||
				op.W > WorkbenchAICanvasMaxFrameSize || op.H > WorkbenchAICanvasMaxFrameSize {
				return fmt.Errorf("%w: op %d frame size out of range", ErrWorkbenchAICanvasBadPatch, index)
			}
			if !validCanvasCoordinate(op.X) || !validCanvasCoordinate(op.Y) {
				return fmt.Errorf("%w: op %d frame position out of range", ErrWorkbenchAICanvasBadPatch, index)
			}
			if len(op.MemberIDs) > WorkbenchAICanvasMaxMembersPerOp {
				return fmt.Errorf("%w: op %d too many members", ErrWorkbenchAICanvasBadPatch, index)
			}
			for _, id := range op.MemberIDs {
				if !workbenchAICanvasIDPattern.MatchString(id) {
					return fmt.Errorf("%w: op %d has invalid member id", ErrWorkbenchAICanvasBadPatch, index)
				}
			}
		case WorkbenchAICanvasOpMoveElement:
			if !workbenchAICanvasIDPattern.MatchString(op.ID) {
				return fmt.Errorf("%w: op %d has invalid element id", ErrWorkbenchAICanvasBadPatch, index)
			}
			if !validCanvasCoordinate(op.X) || !validCanvasCoordinate(op.Y) {
				return fmt.Errorf("%w: op %d move position out of range", ErrWorkbenchAICanvasBadPatch, index)
			}
		default:
			return fmt.Errorf("%w: unknown op %q", ErrWorkbenchAICanvasBadPatch, op.Op)
		}
	}
	return nil
}

func validCanvasCoordinate(value float64) bool {
	return value == value && value > -WorkbenchAICanvasCoordinateLimit && value < WorkbenchAICanvasCoordinateLimit
}
