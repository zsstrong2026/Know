import assert from 'node:assert/strict'
import { test } from 'node:test'

import { isBackableWorkbenchPath, resolveWorkbenchBack } from './back-target.ts'

test('文件夹列表进入编辑页：返回时按历史回到该文件夹', () => {
  assert.equal(isBackableWorkbenchPath('/platform/workbench/folders/f1'), true)
  assert.deepEqual(
    resolveWorkbenchBack({ previous: '/platform/workbench/folders/f1', folderId: 'f1' }),
    { kind: 'history' },
  )
})

test('带搜索/排序 query 的列表也保留（整段 URL 回退）', () => {
  assert.equal(
    isBackableWorkbenchPath('/platform/workbench/folders/f1?q=周报&sort=title'),
    true,
  )
})

test('上一页是另一个编辑页时不回退，避免两个文档间来回弹', () => {
  assert.equal(isBackableWorkbenchPath('/platform/workbench/items/other'), false)
  assert.deepEqual(
    resolveWorkbenchBack({ previous: '/platform/workbench/items/other', folderId: 'f2' }),
    { kind: 'folder', folderId: 'f2' },
  )
})

test('没有站内历史时按条目所属文件夹兜底', () => {
  assert.deepEqual(resolveWorkbenchBack({ previous: null, folderId: 'f3' }), {
    kind: 'folder',
    folderId: 'f3',
  })
  // 站外来源（直接粘贴链接、外部跳入）同样不算可回退历史
  assert.deepEqual(resolveWorkbenchBack({ previous: 'https://example.com/x', folderId: 'f3' }), {
    kind: 'folder',
    folderId: 'f3',
  })
})

test('既无历史也不在任何文件夹时回到最近', () => {
  assert.deepEqual(resolveWorkbenchBack({ previous: '/platform/knowledge-bases', folderId: null }), {
    kind: 'recent',
  })
  assert.deepEqual(resolveWorkbenchBack({ previous: undefined, folderId: null }), {
    kind: 'recent',
  })
})
