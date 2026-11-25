package engine

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/sop/executor"
	"github.com/Tencent/WeKnora/internal/types"
)

// blockingProvider 卡在 Execute 里，直到测试放行，用来观察「节点正在跑」这一刻的事件。
type blockingProvider struct {
	kind  string
	start chan struct{}
	hold  chan struct{}
}

func (b *blockingProvider) Kind() string { return b.kind }

func (b *blockingProvider) Prepare(context.Context, *types.SopNode, string) error { return nil }

func (b *blockingProvider) Execute(
	ctx context.Context, tb executor.TaskBook, _ executor.ExecOptions,
) (executor.ExecResult, error) {
	select {
	case b.start <- struct{}{}:
	default:
	}
	<-b.hold
	return executor.ExecResult{Output: "ok", GateResult: types.SopGatePass}, nil
}

func (b *blockingProvider) Cancel(context.Context) error { return nil }

// 节点进入 running 时也要广播一次状态：否则前端卡片要等到完成/失败才有事件，
// 「正在执行」这段时间看不到任何反馈。
func TestNodeRunningPublishesStatusEvent(t *testing.T) {
	e, _, _ := newTestEngine(t)
	var mu sync.Mutex
	var statuses []string
	e.deps.EventSink = func(_ string, ev executor.StreamEvent) {
		if ev.Type != executor.StreamEventNodeStatus {
			return
		}
		var nr types.SopNodeRun
		if err := json.Unmarshal(ev.Payload, &nr); err != nil {
			return
		}
		mu.Lock()
		statuses = append(statuses, nr.Status)
		mu.Unlock()
	}

	prov := &blockingProvider{kind: "platform_agent", start: make(chan struct{}, 1), hold: make(chan struct{})}
	e.deps.Registry.Register(prov)
	ctx := context.Background()
	def := types.SopDefinition{Nodes: []types.SopNode{{
		ID: "n1", Name: "实现", Type: "agent",
		Executor: types.SopNodeExecutor{Kind: "platform_agent", AgentID: "a"},
	}}}
	run := startRun(t, e, def)

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.Advance(ctx, run.ID)
	}()
	select {
	case <-prov.start:
	case <-time.After(10 * time.Second):
		t.Fatal("执行器没有被调用")
	}

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, s := range statuses {
			if s == types.SopNodeRunning {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond, "进入 running 必须广播状态事件，实际：%v", statuses)

	close(prov.hold)
	<-done

	mu.Lock()
	defer mu.Unlock()
	require.Contains(t, statuses, types.SopNodeSucceeded, "完成时仍要广播")
}
