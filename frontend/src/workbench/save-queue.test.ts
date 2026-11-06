import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createSaveQueue, type SaveQueueOptions } from './save-queue.ts'

/** 构造一个不依赖真实接口的队列；save 立即返回指定结果或抛出指定错误。 */
function makeQueue(overrides: Partial<SaveQueueOptions> & {
  saveImpl?: (payload: { base_version: number; force?: boolean }) => Promise<{ snapshot_version: number }>
} = {}) {
  const states: string[] = []
  const calls: Array<{ base_version: number; force?: boolean }> = []
  const saveImpl = overrides.saveImpl ?? (async () => ({ snapshot_version: 2 }))
  const queue = createSaveQueue({
    itemId: 'item-1',
    clientId: 'tab-a',
    baseVersion: 1,
    delayMs: 1,
    read: () => ({ ydoc: 'AAEC', textContent: 'hello', editorSchemaVersion: 1 }),
    onState: (state) => states.push(state),
    save: async (_id, payload) => {
      calls.push({ base_version: payload.base_version, force: payload.force })
      const result = await saveImpl({ base_version: payload.base_version, force: payload.force })
      return {
        snapshot_version: result.snapshot_version,
        editor_schema_version: 1,
        checkpointed: false,
        updated_at: '2026-10-30T00:00:00Z',
      }
    },
    ...overrides,
  })
  return { queue, states, calls }
}

/** 等到队列空闲（状态离开 pending/saving）。 */
async function settle(queue: ReturnType<typeof makeQueue>['queue']) {
  for (let i = 0; i < 50; i += 1) {
    if (queue.state() !== 'pending' && queue.state() !== 'saving') return
    await new Promise((resolve) => setTimeout(resolve, 5))
  }
}

test('保存成功推进基线版本，状态回到 saved', async () => {
  const { queue, calls, states } = makeQueue()
  queue.markDirty()
  await settle(queue)

  assert.equal(queue.state(), 'saved')
  assert.deepEqual(calls, [{ base_version: 1, force: false }])
  assert.ok(states.includes('pending'))
})

test('连续 markDirty 只写一次（防抖）', async () => {
  const { queue, calls } = makeQueue()
  queue.markDirty()
  queue.markDirty()
  queue.markDirty()
  await settle(queue)
  assert.equal(calls.length, 1, '同一个防抖窗口内只应写一次')
})

test('2401 冲突时不自动重试，状态变 conflict', async () => {
  const { queue, calls } = makeQueue({
    saveImpl: async () => {
      throw { error: { code: 2401, details: { current_version: 9 } } }
    },
  })
  queue.markDirty()
  await settle(queue)

  assert.equal(queue.state(), 'conflict')
  assert.equal(calls.length, 1, '冲突后不得自动重试')
})

test('冲突后 forceSave 以 force=true 重写并推进基线', async () => {
  let conflictOnce = true
  const { queue, calls } = makeQueue({
    saveImpl: async () => {
      if (conflictOnce) {
        conflictOnce = false
        throw { error: { code: 2401 } }
      }
      return { snapshot_version: 10 }
    },
  })
  queue.markDirty()
  await settle(queue)
  assert.equal(queue.state(), 'conflict')

  await queue.forceSave()
  assert.equal(queue.state(), 'saved')
  assert.deepEqual(calls.at(-1), { base_version: 1, force: true })
})

test('acceptRemote 丢弃待写改动，不发请求', async () => {
  const { queue, calls } = makeQueue()
  queue.markDirty()
  queue.acceptRemote()
  await settle(queue)
  assert.equal(calls.length, 0, '放弃本地改动后不应再写服务端')
  assert.equal(queue.state(), 'saved')
})

test('setBaseVersion 让下一次保存带上新基线', async () => {
  const { queue, calls } = makeQueue()
  queue.setBaseVersion(7)
  queue.markDirty()
  await settle(queue)
  assert.deepEqual(calls, [{ base_version: 7, force: false }])
})

test('其他错误只标记 error，不进入 conflict', async () => {
  const { queue } = makeQueue({
    saveImpl: async () => {
      throw { error: { code: 2402 } }
    },
  })
  queue.markDirty()
  await settle(queue)
  assert.equal(queue.state(), 'error')
})

test('dispose 后 markDirty 不再触发写请求', async () => {
  const { queue, calls } = makeQueue()
  queue.dispose()
  queue.markDirty()
  await settle(queue)
  assert.equal(calls.length, 0)
})

test('deferBlankCommit 时 flush 不写，编辑后才写', async () => {
  const { queue, calls } = makeQueue({ deferBlankCommit: true })
  // 挂载后没有任何编辑：离开页面也不应产生 version 1
  await queue.flush()
  assert.equal(calls.length, 0, '空文档延迟期间 flush 不能落盘')

  queue.markDirty()
  await settle(queue)
  assert.equal(calls.length, 1, '用户编辑后解除延迟')
})

test('未开启 deferBlankCommit 时行为不变', async () => {
  const { queue, calls } = makeQueue()
  queue.markDirty()
  await queue.flush()
  assert.equal(calls.length, 1)
})

test('lastSavedAt 只在真正落盘后推进', async () => {
  const { queue } = makeQueue()
  assert.equal(queue.lastSavedAt(), null)
  queue.markDirty()
  await settle(queue)
  const first = queue.lastSavedAt()
  assert.ok(typeof first === 'number' && first > 0)
  // 冲突不刷新时间：它并不代表内容已经写成功
  await queue.flush()
  assert.equal(queue.lastSavedAt(), first)
})
