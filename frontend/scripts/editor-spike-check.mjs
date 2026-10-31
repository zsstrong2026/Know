// P0-3 编辑器 spike 的自动化断言脚本。
//
// 依赖：Node 22+ 内置 WebSocket（无需 ws / playwright）+ 本机 Edge/Chrome 的 CDP。
// 用法：
//   npm run dev                                     # 另开终端，起 vite（5173）
//   node scripts/editor-spike-check.mjs [--url=http://127.0.0.1:5173/editor-spike.html]
//
// 脚本自己拉起无头浏览器（--remote-debugging-port=0），跑完自动关闭。
import { spawn } from 'node:child_process'
import { existsSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import assert from 'node:assert/strict'

const BROWSER_CANDIDATES = [
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
  'C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe',
  '/usr/bin/google-chrome',
  '/usr/bin/chromium',
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
]

const cliArgs = Object.fromEntries(
  process.argv.slice(2).map(arg => {
    const [key, value = 'true'] = arg.replace(/^--/, '').split('=')
    return [key, value]
  })
)
const pageUrl = cliArgs.url ?? 'http://127.0.0.1:5173/editor-spike.html'
const browserPath = cliArgs.browser ?? BROWSER_CANDIDATES.find(candidate => existsSync(candidate))

if (!browserPath) {
  console.error('找不到 Chrome/Edge 可执行文件，请用 --browser=<path> 指定')
  process.exit(1)
}

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms))
const findings = []
const record = (title, payload) => {
  findings.push({ title, payload })
  console.log(`\n=== ${title} ===`)
  console.log(typeof payload === 'string' ? payload : JSON.stringify(payload, null, 2))
}

/**
 * 最小 CDP 客户端。
 * 扁平会话（flatten: true）下 sessionId 是消息顶层字段，必须单独处理，
 * 不能塞进 params，否则浏览器会忽略该命令。
 */
class CdpConnection {
  #ws
  #id = 0
  #pending = new Map()
  #listeners = new Map()

  constructor(ws) {
    this.#ws = ws
    ws.addEventListener('message', event => {
      const msg = JSON.parse(event.data)
      if (msg.id && this.#pending.has(msg.id)) {
        const { resolve, reject } = this.#pending.get(msg.id)
        this.#pending.delete(msg.id)
        if (msg.error) {
          reject(new Error(`${msg.error.message} ${msg.error.data ?? ''}`))
        } else {
          resolve(msg.result)
        }
        return
      }
      const key = msg.sessionId ? `${msg.sessionId}:${msg.method}` : msg.method
      this.#listeners.get(key)?.forEach(fn => fn(msg.params))
    })
  }

  static async connect(url) {
    const ws = new WebSocket(url)
    await new Promise((resolve, reject) => {
      ws.addEventListener('open', resolve, { once: true })
      ws.addEventListener('error', reject, { once: true })
    })
    return new CdpConnection(ws)
  }

  send(method, params = {}, sessionId) {
    const id = ++this.#id
    const message = { id, method, params }
    if (sessionId) message.sessionId = sessionId
    return new Promise((resolve, reject) => {
      this.#pending.set(id, { resolve, reject })
      this.#ws.send(JSON.stringify(message))
    })
  }

  on(method, fn, sessionId) {
    const key = sessionId ? `${sessionId}:${method}` : method
    const list = this.#listeners.get(key) ?? []
    list.push(fn)
    this.#listeners.set(key, list)
  }

  close() {
    this.#ws.close()
  }
}

/** 绑定到某个 page target 的会话，提供 send / evaluate / on */
class PageSession {
  constructor(connection, sessionId) {
    this.connection = connection
    this.sessionId = sessionId
  }

  send(method, params = {}) {
    return this.connection.send(method, params, this.sessionId)
  }

  on(method, fn) {
    this.connection.on(method, fn, this.sessionId)
  }

  /** 在页面里执行表达式（自动 await），失败时带页面堆栈抛出 */
  async evaluate(expression) {
    const result = await this.send('Runtime.evaluate', {
      expression: `(async () => { return (${expression}) })()`,
      awaitPromise: true,
      returnByValue: true,
    })
    if (result.exceptionDetails) {
      const detail = result.exceptionDetails.exception?.description ?? result.exceptionDetails.text
      throw new Error(`页面异常：${detail}`)
    }
    return result.result.value
  }

  async waitFor(expression, label, timeout = 30000) {
    const deadline = Date.now() + timeout
    while (Date.now() < deadline) {
      if (await this.evaluate(expression)) return
      await sleep(120)
    }
    throw new Error(`等待超时：${label}`)
  }
}

async function launchBrowser() {
  const userDataDir = mkdtempSync(join(tmpdir(), 'editor-spike-'))
  const child = spawn(
    browserPath,
    [
      '--headless=new',
      '--remote-debugging-port=0',
      `--user-data-dir=${userDataDir}`,
      '--no-first-run',
      '--no-default-browser-check',
      '--disable-gpu',
      '--window-size=1400,1000',
      'about:blank',
    ],
    { stdio: ['ignore', 'ignore', 'pipe'] }
  )

  // 无头模式会把监听地址写到 stderr：DevTools listening on ws://127.0.0.1:<port>/devtools/browser/<id>
  const wsUrl = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('等待 DevTools 端口超时')), 20000)
    let buffer = ''
    child.stderr.on('data', chunk => {
      buffer += chunk.toString()
      const match = /DevTools listening on (ws:\/\/\S+)/.exec(buffer)
      if (match) {
        clearTimeout(timer)
        resolve(match[1])
      }
    })
    child.on('exit', code => {
      clearTimeout(timer)
      reject(new Error(`浏览器提前退出，code=${code}`))
    })
  })

  return { child, userDataDir, wsUrl }
}

async function main() {
  const { child, userDataDir, wsUrl } = await launchBrowser()
  let connection
  try {
    connection = await CdpConnection.connect(wsUrl)
    const { targetId } = await connection.send('Target.createTarget', { url: 'about:blank' })
    const { sessionId } = await connection.send('Target.attachToTarget', { targetId, flatten: true })
    const page = new PageSession(connection, sessionId)

    const runtimeErrors = []
    page.on('Runtime.consoleAPICalled', params => {
      if (params.type === 'error') {
        runtimeErrors.push(params.args.map(arg => arg.value ?? arg.description ?? '').join(' '))
      }
    })
    page.on('Runtime.exceptionThrown', params => {
      runtimeErrors.push(
        params.exceptionDetails?.exception?.description ?? params.exceptionDetails?.text ?? ''
      )
    })

    await page.send('Page.enable')
    await page.send('Runtime.enable')
    await page.send('Emulation.setDeviceMetricsOverride', {
      width: 1400,
      height: 1000,
      deviceScaleFactor: 1,
      mobile: false,
    })
    await page.send('Page.navigate', { url: pageUrl })
    await page.waitFor('typeof window.__spike === "object"', '__spike 暴露')
    await page.waitFor('window.__spike.summary().rowCount > 0', '数据库注入完成')

    // ---------------- 1. 数据库 block + 100 行 ----------------
    const initial = await page.evaluate('window.__spike.summary()')
    assert.equal(initial.seedRowCount, 100, 'seed 生成 100 行')
    assert.equal(initial.rowCount, 100, 'database children = 100')
    assert.equal(initial.views.table, 1, 'table 视图容器已渲染')
    record('1. 数据库 block 初始状态', initial)

    const addIds = await page.evaluate('window.__spike.addRows(5)')
    assert.equal(addIds.length, 5, '新增 5 行返回 5 个 id')
    assert.equal(await page.evaluate('window.__spike.rowCount()'), 105, '新增后 105 行')

    const delResult = await page.evaluate(`(() => {
      const ids = [window.__spike.rowIdAt(0), window.__spike.rowIdAt(2), window.__spike.rowIdAt(4)]
      window.__spike.deleteRows(ids)
      return { deleted: ids.length, rows: window.__spike.rowCount() }
    })()`)
    assert.equal(delResult.rows, 102, '删除 3 行后 102 行')

    const titleResult = await page.evaluate(`(() => {
      const rowId = window.__spike.rowIdAt(0)
      window.__spike.setRowTitle(rowId, 'Row 001 已改名')
      const model = window.__blocksuiteSpike.store.getModelById(rowId)
      return { rowId, text: model.text.toString() }
    })()`)
    assert.ok(titleResult.text.includes('已改名'), '标题列改写生效')
    record('1b. 增 / 删 / 改行', {
      新增行数: addIds.length,
      删除后行数: delResult.rows,
      标题改写: titleResult,
    })

    // ---------------- 2. 三视图切换 ----------------
    const viewRun = []
    for (const type of ['table', 'kanban', 'calendar']) {
      await page.evaluate(`window.__spike.clickView('${type}')`)
      await sleep(500)
      const evidence = await page.evaluate('window.__spike.viewEvidence()')
      const rows = await page.evaluate('window.__spike.rowCount()')
      viewRun.push({ type, rows, evidence })
      assert.equal(rows, 102, `${type} 切换后行数不变`)
      assert.equal(evidence[type], 1, `${type} 视图容器渲染 1 个`)
      const selected = evidence.buttons.find(button => button.selected)?.name ?? ''
      assert.ok(selected.toLowerCase().includes(type), `${type} 按钮为选中态，实际=${selected}`)
    }
    record('2. 三视图切换', viewRun)

    // ---------------- 3. Calendar 拖拽改期 ----------------
    const beforeIso = await page.evaluate('window.__spike.rowDateAt(0)')
    const targetDay = await page.evaluate(
      `(() => { const d = new Date(window.__spike.baseDay()); d.setDate(d.getDate() + 12); return window.__spike.startOfLocalDay(d.getTime()) })()`
    )
    await page.evaluate(`window.__spike.clickView('calendar')`)
    await sleep(600)
    const dragResult = await page.evaluate(`window.__spike.dragCalendarEntry(0, ${targetDay})`)
    await sleep(600)
    const afterIso = await page.evaluate('window.__spike.rowDateAt(0)')
    const expectedIso = await page.evaluate(`window.__spike.formatLocalDate(${targetDay})`)
    assert.equal(afterIso, expectedIso, `calendar 拖拽后日期应为 ${expectedIso}，实际 ${afterIso}`)
    record('3. Calendar 拖拽改期（合成 DragEvent，走上游 DnD 链路）', {
      拖拽: dragResult,
      目标日期: expectedIso,
      改期前: beforeIso,
      改期后: afterIso,
    })

    // 快照重挂载后日期仍在
    const snapAfterDrag = await page.evaluate('window.__spike.snapshotBase64()')
    const firstRowId = await page.evaluate('window.__spike.rowIdAt(0)')
    await page.evaluate(`window.__spike.mount(${JSON.stringify(snapAfterDrag)})`)
    await page.waitFor('window.__spike.summary().rowCount > 0', '快照重挂载完成')
    const dateAfterRemount = await page.evaluate(
      `window.__spike.rowDate(window.__blocksuiteSpike.store.root.children.find(c => c.flavour === 'affine:note').children.find(c => c.flavour === 'affine:database').children[0].id)`
    )
    assert.equal(dateAfterRemount, expectedIso, '重挂载后日期保持')
    assert.equal(await page.evaluate('window.__spike.rowIdAt(0)'), firstRowId, '重挂载后行 id 一致')
    record('3b. 快照重挂载后日期', {
      期望: expectedIso,
      实际: dateAfterRemount,
      快照字节: snapAfterDrag.length ? Math.floor((snapAfterDrag.length * 3) / 4) : 0,
    })

    // ---------------- 4. 图片资产 ----------------
    await page.waitFor('window.__spike.imageEvidence().naturalWidth > 0', '图片渲染', 20000)
    const imageEvidence = await page.evaluate('window.__spike.imageEvidence()')
    assert.ok(imageEvidence.found, '重挂载后存在 <img>')
    assert.ok(imageEvidence.naturalWidth > 0, '重挂载后 naturalWidth > 0')
    record('4. 图片资产（重挂载后由 AssetResolver 重新解析）', imageEvidence)

    // ---------------- 5. 2 秒自动保存 + 恢复 ----------------
    await page.evaluate('localStorage.removeItem("spike:autosave:spike-page")')
    const typed = await page.evaluate('window.__spike.typeIntoFirstParagraph(" [autosave-marker]")')
    const typedAt = Date.now()
    await sleep(900)
    const midStatus = await page.evaluate('window.__spike.storageRecord()')
    assert.equal(midStatus, null, '输入后 0.9s 尚未落盘（2s 防抖）')
    await sleep(1800)
    const storage = await page.evaluate('window.__spike.storageRecord()')
    assert.ok(storage, '约 2s 后已写入 localStorage')
    record('5. 自动保存（2 秒防抖）', {
      输入后文本: typed,
      '0.9s 时': midStatus,
      '2.7s 后': storage,
      写入耗时毫秒: Date.now() - typedAt,
    })

    const textBeforeRestore = await page.evaluate(
      `(() => { const note = window.__blocksuiteSpike.store.root.children.find(c => c.flavour === 'affine:note'); return note.children[0].text.toString() })()`
    )
    const savedSnapshot = await page.evaluate(
      'JSON.parse(localStorage.getItem("spike:autosave:spike-page")).base64'
    )
    await page.evaluate(`window.__spike.mount(${JSON.stringify(savedSnapshot)})`)
    await page.waitFor('window.__spike.summary().rowCount > 0', '自动保存快照重挂载')
    const textAfterRestore = await page.evaluate(
      `(() => { const note = window.__blocksuiteSpike.store.root.children.find(c => c.flavour === 'affine:note'); return note.children[0].text.toString() })()`
    )
    assert.equal(textAfterRestore, textBeforeRestore, '重挂载后文本一致')
    record('5b. 刷新恢复（同一快照重挂载）', {
      恢复前: textBeforeRestore,
      恢复后: textAfterRestore,
      快照字节: Math.floor((savedSnapshot.length * 3) / 4),
    })

    // ---------------- 6. 8MB 边界 ----------------
    const oversize = await page.evaluate('window.__spike.testOversizeRejection()')
    assert.equal(oversize.rejected, true, '超 8MB 快照被拒绝')
    assert.equal(oversize.wroteToStorage, false, '超限时没有写入存储（未静默截断）')
    record('6. 8MB 边界', oversize)

    // ---------------- 7. P0-2 回归：空白文档仍可输入 ----------------
    const blank = await page.evaluate('window.__spike.blankDocCheck()')
    assert.equal(blank.paragraphCount, 1, '空白文档只有 1 个初始 paragraph')
    assert.equal(blank.text, '空白页可输入', '空白文档可写入文本')
    record('7. P0-2 回归（空白文档可输入）', blank)

    // ---------------- 8. 20 元素编辑 + 快照恢复 ----------------
    const rich = await page.evaluate('window.__spike.richDocRoundTrip()')
    assert.ok(rich.before.count >= 20, `block 数量应 ≥ 20，实际 ${rich.before.count}`)
    const requiredFlavours = {
      'affine:paragraph': 6, // h1/h2/h3 + quote + 2 段落
      'affine:list': 3, // bulleted / numbered / todo
      'affine:code': 1,
      'affine:divider': 1,
      'affine:image': 1,
      'affine:database': 1,
    }
    for (const [flavour, min] of Object.entries(requiredFlavours)) {
      assert.ok(
        (rich.before.flavours[flavour] ?? 0) >= min,
        `${flavour} 至少 ${min} 个，实际 ${rich.before.flavours[flavour] ?? 0}`
      )
    }
    assert.equal(rich.before.databaseRows, 5, 'database 含 5 行')
    assert.equal(rich.identical, true, '快照重挂载后清单完全一致')
    assert.equal(rich.after.count, rich.before.count, '恢复后 block 数量一致')
    assert.deepEqual(rich.after.flavours, rich.before.flavours, '恢复后各 flavour 数量一致')
    assert.deepEqual(rich.after.texts, rich.before.texts, '恢复后关键文本一致')
    assert.equal(rich.after.databaseRows, rich.before.databaseRows, '恢复后数据库行数一致')
    assert.deepEqual(rich.after.duplicateIds, [], '恢复后没有重复 block id')
    assert.equal(rich.restoreApplyUpdateCalls, 1, '恢复路径恰好 1 次 applyUpdate')
    record('8. 20 元素编辑 + 快照恢复', {
      block数量: rich.before.count,
      flavour分布: rich.before.flavours,
      数据库行数: rich.before.databaseRows,
      快照字节: rich.snapshotBytes,
      恢复后快照字节: rich.restoredSnapshotBytes,
      恢复时applyUpdate次数: rich.restoreApplyUpdateCalls,
      清单完全一致: rich.identical,
    })

    // ---------------- 9. 双根切换（同一 store，不重放快照） ----------------
    const dual = await page.evaluate('window.__spike.rootSwitchCheck(5)')
    const { pageBefore, edgelessInventory, pageAfter } = dual

    // 9a. 切到 edgeless 后 DOM 只保留 edgeless root，内容不变
    assert.equal(dual.dom.edgeless.pageRoots, 0, 'edgeless 下没有残留 page root')
    assert.equal(dual.dom.edgeless.edgelessRoots, 1, 'edgeless root 渲染 1 个')
    assert.equal(edgelessInventory.count, pageBefore.count, '切换后 block 数量不变')
    assert.deepEqual(edgelessInventory.texts, pageBefore.texts, '切换后文本一致')
    assert.equal(edgelessInventory.rootId, pageBefore.rootId, 'store.root id 未变')

    // 9b. 在 edgeless 新增 block 后切回 page，新增 block 仍在且无重复
    assert.equal(pageAfter.count, pageBefore.count + 1, 'page 下多出 edgeless 新增的 1 个 block')
    assert.ok(
      pageAfter.texts.includes('edgeless 下新增的段落'),
      'page 下能看到 edgeless 新增的文本'
    )
    assert.deepEqual(pageAfter.duplicateIds, [], 'page 树里没有重复 block id')
    assert.deepEqual(dual.dom.pageAfter.duplicateBlockElements, [], 'DOM 里没有重复 block 元素')
    assert.equal(dual.dom.pageAfter.pageRoots, 1, 'page root 渲染 1 个')
    assert.equal(dual.dom.pageAfter.edgelessRoots, 0, 'page 下没有残留 edgeless root')

    // 9c. 切换过程 0 次 applyUpdate，只是重建视图
    assert.equal(dual.applyUpdate.before, 0, '首次挂载（无快照）applyUpdate = 0')
    assert.equal(dual.applyUpdate.after, 0, '5 次切换后 applyUpdate 仍为 0')
    assert.equal(dual.viewMounts.after - dual.viewMounts.before, dual.modeSwitches, '每次切换重建 1 次视图')
    assert.equal(dual.viewUnmounts, dual.modeSwitches, '每次切换卸载 1 次视图')
    // 9c'. 切换复用同一 store / 同一 Y.Doc 实例（不是重建 doc）
    assert.equal(dual.identity.sameStore, true, '切换后仍是同一个 store 实例')
    assert.equal(dual.identity.sameSpaceDoc, true, '切换后仍是同一个 Y.Doc 实例')
    assert.equal(dual.identity.sameRootId, true, '切换后 store.root id 未变')

    // 9d. 连续切换不累积 block、快照字节稳定、无重复
    // 允许极小浮动（≤64 字节）：edgeless 首次渲染会写回 note 的几何/视口相关属性，
    // 属于一次性归一化，不是每次切换都增长。
    const roundBytes = dual.rounds.map(r => r.bytes)
    for (const round of dual.rounds) {
      assert.equal(round.blocks, pageAfter.count, `第 ${round.round} 次切换后 block 数不增长`)
      assert.equal(round.dupes, 0, `第 ${round.round} 次切换后无重复 id`)
    }
    const byteSpread = Math.max(...roundBytes, dual.bytes.pageAfter) - Math.min(...roundBytes, dual.bytes.pageAfter)
    assert.ok(byteSpread <= 64, `切换期间快照字节浮动应 ≤64，实际 ${byteSpread}`)

    record('9. 双根切换（同一 store / 同一 Y.Doc）', {
      切换次数: dual.modeSwitches,
      applyUpdate次数: dual.applyUpdate,
      视图挂载: dual.viewMounts,
      视图卸载: dual.viewUnmounts,
      'page→edgeless 后': {
        block数: edgelessInventory.count,
        文本一致: JSON.stringify(edgelessInventory.texts) === JSON.stringify(pageBefore.texts),
        rootId未变: edgelessInventory.rootId === pageBefore.rootId,
      },
      'edgeless 新增→page 后': {
        新增blockId: dual.addedBlockId,
        block数: pageAfter.count,
        新增文本在: pageAfter.texts.includes('edgeless 下新增的段落'),
        重复id: pageAfter.duplicateIds,
        DOM重复元素: dual.dom.pageAfter.duplicateBlockElements,
      },
      快照字节: dual.bytes,
       同一实例: dual.identity,
       首次切换的props变化: dual.firstSwitchPropDiff,
       连续切换: dual.rounds,
    })

    const fatalErrors = runtimeErrors.filter(
      text => text && !/ResizeObserver|favicon|Failed to load resource/i.test(text)
    )
    record('运行期未捕获错误', fatalErrors.length ? fatalErrors : '（无）')

    console.log('\n全部断言通过')
    return findings
  } finally {
    connection?.close()
    child.kill()
    await sleep(300)
    rmSync(userDataDir, { recursive: true, force: true })
  }
}

main().catch(error => {
  console.error('\n断言失败：', error)
  process.exit(1)
})
