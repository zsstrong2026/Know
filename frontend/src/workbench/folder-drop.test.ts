import assert from 'node:assert/strict'
import { test } from 'node:test'

import { isSelfOrDescendant, resolveFolderDrop, type FolderLike } from './folder-drop'

/**
 * 结构：
 *   a
 *     a1
 *       a1x
 *   b
 *   c
 */
const folders: FolderLike[] = [
  { id: 'a', parent_id: null },
  { id: 'a1', parent_id: 'a' },
  { id: 'a1x', parent_id: 'a1' },
  { id: 'b', parent_id: null },
  { id: 'c', parent_id: null },
]

test('resolveFolderDrop：落在行的上/下半区 = 排序到目标前/后', () => {
  // 把 c 拖到 b 之前：根层排除自己后是 [a,b] → 下标 1
  assert.deepEqual(resolveFolderDrop(folders, 'c', 'b', 'before'), { parentId: null, position: 1 })
  // 拖到 b 之后 → 下标 2
  assert.deepEqual(resolveFolderDrop(folders, 'c', 'b', 'after'), { parentId: null, position: 2 })
})

test('resolveFolderDrop：去掉自己之后才数下标，原地放下不会偏一位', () => {
  // b 在根层的位置是 1；排除自己后 [a,c]，插回 1 就是原位
  assert.deepEqual(resolveFolderDrop(folders, 'b', 'c', 'before'), { parentId: null, position: 1 })
  assert.deepEqual(resolveFolderDrop(folders, 'b', 'a', 'after'), { parentId: null, position: 1 })
})

test('resolveFolderDrop：行中间 = 放进这个文件夹，追加到末尾', () => {
  assert.deepEqual(resolveFolderDrop(folders, 'b', 'a', 'inside'), { parentId: 'a', position: 1 })
  // a 已经有 a1，放进去应该排在后
  assert.deepEqual(resolveFolderDrop(folders, 'c', 'a', 'inside'), { parentId: 'a', position: 1 })
})

test('resolveFolderDrop：跨层拖到别的分支的前后 = 换父级', () => {
  assert.deepEqual(resolveFolderDrop(folders, 'a1', 'b', 'before'), { parentId: null, position: 1 })
  assert.deepEqual(resolveFolderDrop(folders, 'a1', 'b', 'inside'), { parentId: 'b', position: 0 })
})

test('resolveFolderDrop：拖进自己的子孙被判无效', () => {
  assert.equal(resolveFolderDrop(folders, 'a', 'a1', 'inside'), null)
  assert.equal(resolveFolderDrop(folders, 'a', 'a1x', 'before'), null)
  // 反过来（子孙拖到祖先上）是合法的
  assert.deepEqual(resolveFolderDrop(folders, 'a1x', 'a1', 'after'), { parentId: 'a', position: 1 })
})

test('resolveFolderDrop：拖到自己或未知 id 返回 null', () => {
  assert.equal(resolveFolderDrop(folders, 'a', 'a', 'inside'), null)
  assert.equal(resolveFolderDrop(folders, 'a', 'ghost', 'inside'), null)
  assert.equal(resolveFolderDrop(folders, 'ghost', 'a', 'inside'), null)
  assert.equal(resolveFolderDrop([], 'a', 'b', 'inside'), null)
})

test('isSelfOrDescendant：含自身，且能兜住脏数据里的环', () => {
  assert.equal(isSelfOrDescendant(folders, 'a', 'a'), true)
  assert.equal(isSelfOrDescendant(folders, 'a', 'a1x'), true)
  assert.equal(isSelfOrDescendant(folders, 'a1', 'a'), false)
  // 环：x → y → x
  const cyclic: FolderLike[] = [
    { id: 'x', parent_id: 'y' },
    { id: 'y', parent_id: 'x' },
  ]
  assert.equal(isSelfOrDescendant(cyclic, 'z', 'x'), false, '遇环要停下来，不能死循环')
})
