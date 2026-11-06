import assert from 'node:assert/strict'
import test from 'node:test'

import {
  IMPORT_ACCEPT,
  collectAttachments,
  createImportTasks,
  doneCount,
  failedCount,
  fileExtension,
  pendingCount,
  pickImportFiles,
  resetFailedTasks,
  runImportTasks,
  type ImportTask,
} from './import-material.ts'

/** 造一个最小 File；node 18+ 自带 File。 */
function file(name: string, size = 10, type = ''): File {
  return new File([new Uint8Array(size)], name, { type })
}

test('accept 列表与校验口径一致', () => {
  for (const ext of IMPORT_ACCEPT.split(',')) {
    const picked = pickImportFiles([file(`a${ext}`, 10)])
    assert.equal(picked.accepted.length, 1, `${ext} 应被接受`)
    assert.deepEqual(picked.rejected, [])
  }
})

test('扩展名不区分大小写', () => {
  assert.equal(fileExtension('报告.PDF'), '.pdf')
  assert.equal(fileExtension('noext'), '')
  assert.equal(pickImportFiles([file('文档.DOCX')]).accepted.length, 1)
})

test('空文件、超限、不支持格式分别拒绝且不影响其余文件', () => {
  const picked = pickImportFiles(
    [file('ok.md'), file('empty.pdf', 0), file('big.pdf', 20), file('x.exe', 5), file('note.md', 5)],
    10,
  )
  assert.deepEqual(picked.accepted.map((row) => row.name), ['ok.md', 'note.md'])
  assert.deepEqual(
    picked.rejected.map((row) => `${row.name}:${row.reason}`),
    ['empty.pdf:empty', 'big.pdf:too_large', 'x.exe:unsupported'],
  )
})

test('任务 key 同名文件不冲突', () => {
  const tasks = createImportTasks([file('a.md'), file('a.md')])
  assert.equal(new Set(tasks.map((row) => row.key)).size, 2)
  assert.deepEqual(tasks.map((row) => row.state), ['pending', 'pending'])
})

test('串行上传：成功与失败各自落状态，失败项可重试', async () => {
  const tasks = createImportTasks([file('a.md'), file('b.pdf'), file('c.md')])
  const uploaded: string[] = []
  const result = await runImportTasks(tasks, async (input) => {
    uploaded.push(input.name)
    if (input.name === 'b.pdf') throw new Error('boom')
    return { assetId: `asset-${input.name}` }
  }, () => {})
  // 串行：uploaded 顺序与选择顺序一致
  assert.deepEqual(uploaded, ['a.md', 'b.pdf', 'c.md'])
  assert.equal(doneCount(result), 2)
  assert.equal(failedCount(result), 1)
  assert.equal(pendingCount(result), 0)
  assert.equal(result[1].error, 'boom')

  const retried = resetFailedTasks(result)
  assert.equal(failedCount(retried), 0)
  assert.equal(pendingCount(retried), 1)
  // 成功项保留 assetId，重试不会重复上传它们
  assert.equal(retried[0].assetId, 'asset-a.md')
  assert.equal(retried[0].state, 'done')
})

test('重试只处理 pending，不重复上传已成功的文件', async () => {
  const done: ImportTask = { key: '0:a', file: file('a.md'), state: 'done', assetId: 'x' }
  const failed: ImportTask = { key: '1:b', file: file('b.md'), state: 'failed', error: 'e' }
  const calls: string[] = []
  const result = await runImportTasks([done, resetFailedTasks([failed])[0]], async (input) => {
    calls.push(input.name)
    return { assetId: 'y' }
  }, () => {})
  assert.deepEqual(calls, ['b.md'])
  assert.equal(doneCount(result), 2)
})

test('collectAttachments 只收成功项，并补齐 MIME 兜底', async () => {
  const tasks = createImportTasks([file('a.md'), file('b.pdf')])
  const result = await runImportTasks(tasks, async (input) => ({ assetId: `id-${input.name}` }), () => {})
  const attachments = collectAttachments(result)
  assert.deepEqual(attachments, [
    { assetId: 'id-a.md', name: 'a.md', size: 10, type: 'application/octet-stream' },
    { assetId: 'id-b.pdf', name: 'b.pdf', size: 10, type: 'application/octet-stream' },
  ])
})

test('onChange 在每项开始与结束时都回调', async () => {
  const tasks = createImportTasks([file('a.md')])
  const states: string[] = []
  await runImportTasks(tasks, async () => ({ assetId: 'id' }), (next) => {
    states.push(next[0].state)
  })
  assert.deepEqual(states, ['uploading', 'done'])
})
