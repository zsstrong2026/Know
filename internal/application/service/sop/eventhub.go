// eventhub.go 是运行事件的内存广播：执行器的流事件与节点状态变更都经它推给
// 订阅该运行的 SSE 连接。
//
// 三条口径：
//   - 丢事件不丢运行：订阅者缓冲满（前端卡住/网络慢）时直接丢弃这条事件，
//     Publish 绝不阻塞推进协程——一条慢连接不该拖住整条流程；
//   - 上限兜底：同一运行最多 16 个订阅，超出即拒绝（防浏览器重连泄漏连接）；
//   - 单进程：多副本部署时事件只在本进程可见（SSE 连接与执行落在同一副本由负载
//     均衡决定；跨副本直播不在本任务范围）。
package sop

import (
	"sync"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
)

// eventHubMaxSubscribers 是单个运行允许的并发订阅数。
const eventHubMaxSubscribers = 16

// eventHubSubscriberBuffer 是每个订阅的缓冲长度（一次工具调用可能连续吐几十行）。
const eventHubSubscriberBuffer = 64

// EventHub 运行事件广播器（进程内）。
type EventHub struct {
	mu   sync.Mutex
	subs map[string][]chan executor.StreamEvent
}

// NewEventHub 建空广播器。
func NewEventHub() *EventHub { return &EventHub{subs: map[string][]chan executor.StreamEvent{}} }

// Subscribe 订阅某运行的事件；返回通道与退订函数。
//
// 订阅数达到上限时返回一个立即关闭的空通道（调用方会读到 EOF 并结束流），
// 不返回 nil——nil 通道会让 select 永久阻塞。退订函数可重复调用。
func (h *EventHub) Subscribe(runID string) (<-chan executor.StreamEvent, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[runID]; !ok {
		h.subs[runID] = nil
	}
	if len(h.subs[runID]) >= eventHubMaxSubscribers {
		closed := make(chan executor.StreamEvent)
		close(closed)
		return closed, func() {}
	}
	ch := make(chan executor.StreamEvent, eventHubSubscriberBuffer)
	h.subs[runID] = append(h.subs[runID], ch)
	var once sync.Once
	return ch, func() {
		once.Do(func() { h.remove(runID, ch) })
	}
}

// remove 摘掉一个订阅并关闭通道（关闭后调用方的 range 立刻结束）。
func (h *EventHub) remove(runID string, ch chan executor.StreamEvent) {
	h.mu.Lock()
	list := h.subs[runID]
	out := list[:0]
	for _, sub := range list {
		if sub != ch {
			out = append(out, sub)
		}
	}
	if len(out) == 0 {
		delete(h.subs, runID)
	} else {
		h.subs[runID] = out
	}
	h.mu.Unlock()
	close(ch)
}

// Publish 广播一条事件：订阅者缓冲满就丢弃这条（不阻塞执行）。
func (h *EventHub) Publish(runID string, ev executor.StreamEvent) {
	if h == nil || runID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs[runID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// SubscriberCount 返回某运行当前的订阅数（测试与运维观测用）。
func (h *EventHub) SubscriberCount(runID string) int {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[runID])
}
