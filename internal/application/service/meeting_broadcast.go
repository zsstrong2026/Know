package service

import "sync"

// MeetingEvent 会议室 SSE 事件（事件名与设计文档一致）
type MeetingEvent struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// SSE 事件名常量
const (
	MeetingEventMessageCreated       = "message.created"
	MeetingEventMessageReviewUpdated = "message.review_updated"
	MeetingEventTurnStarted          = "discussion.turn_started"
	MeetingEventTurnEnded            = "discussion.turn_ended"
	MeetingEventGuardTriggered       = "guard.triggered"
	MeetingEventConsensusCreated     = "consensus.created"
	MeetingEventSolutionGenerating   = "solution.generating"
	MeetingEventSolutionCreated      = "solution.created"
	MeetingEventPing                 = "ping"
)

// MeetingBroadcaster 进程内按会议房间的发布/订阅（单副本部署前提，见设计风险章节）。
// 导出原因：handler 层 SSE 订阅需经类型断言拿到广播器（见 T1.6 engineBroadcaster）。
type MeetingBroadcaster struct {
	mu   sync.RWMutex
	subs map[string]map[chan MeetingEvent]struct{}
}

func NewMeetingBroadcaster() *MeetingBroadcaster {
	return &MeetingBroadcaster{subs: make(map[string]map[chan MeetingEvent]struct{})}
}

// Subscribe 订阅某房间事件，返回 channel 与取消函数
func (b *MeetingBroadcaster) Subscribe(roomID string) (chan MeetingEvent, func()) {
	ch := make(chan MeetingEvent, 64)
	b.mu.Lock()
	if b.subs[roomID] == nil {
		b.subs[roomID] = make(map[chan MeetingEvent]struct{})
	}
	b.subs[roomID][ch] = struct{}{}
	b.mu.Unlock()
	// 用 once 保证取消函数幂等：重复调用不会 double-close channel 造成 panic（对齐 context.CancelFunc 语义）
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs[roomID], ch)
			if len(b.subs[roomID]) == 0 {
				delete(b.subs, roomID)
			}
			b.mu.Unlock()
			close(ch)
		})
	}
}

// Publish 向房间内全部订阅者广播（非阻塞，慢消费者丢弃并记录由调用方负责）
func (b *MeetingBroadcaster) Publish(roomID string, evt MeetingEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[roomID] {
		select {
		case ch <- evt:
		default: // 慢消费者丢事件，前端有 ping 心跳与重连兜底
		}
	}
}
