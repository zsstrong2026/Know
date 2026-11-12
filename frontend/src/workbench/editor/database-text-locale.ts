/**
 * 数据库视图里上游硬编码文案的本地化（P3-1 体验补齐）。
 *
 * 背景：`<affine-database>` 的视图 Tab、行按钮、搜索框占位、日历空态，以及
 * 「视图设置」弹层与列头右键菜单里的菜单项（View settings / Type / Property type /
 * Sort Ascending / Duplicate view…）都是上游写死的英文字符串，没有 i18n 入口，
 * 也不随界面语言变化。
 *
 * 做法：只在**已知的静态文案容器**里做精确文本替换。
 * 不用整树扫描，是因为行标题与单元格值也是文本节点，整树替换会把用户写的
 * 「New Record」这种内容也改掉。替换后的文本不再命中字典，不会重复替换。
 *
 * 文案统一走 i18n：`TEXT_KEYS` 只登记「英文原文 → 文案 key」，
 * 具体语言由当前 locale 决定，六种界面语言都能生效（早期版本只有简繁两套硬编码）。
 */
import i18n from '@/i18n'

/** 允许替换文本的容器；命中任一祖先即处理。 */
const SAFE_SELECTORS = [
  'component-overflow',
  '.affine-database-toolbar',
  '.calendar-toolbar',
  '.calendar-empty-month-hint',
  '.calendar-new-row',
  '.data-view-table-group-add-row',
  // 视图设置 / 筛选 / 排序 / 列头右键等弹层挂在 body 上（不在数据库宿主内），单独放行。
  'affine-menu',
  // 单/多选列的标签选择弹层：同样是 body 上的独立浮层。
  'affine-multi-tag-select',
]

const SAFE_SELECTOR = SAFE_SELECTORS.join(',')

/** body 上需要处理的浮层容器（比宿主范围更严，避免碰正文）。 */
const BODY_SAFE_SELECTOR = 'affine-menu, affine-multi-tag-select'

/**
 * 英文原文 → i18n key。
 *
 * 覆盖三块：工具行（新建行/搜索/今天）、日历空态、视图设置弹层。
 * 上游文案一旦变化，这里查不到就保持原文，不会误伤其它内容。
 */
const TEXT_KEYS: Record<string, string> = {
  // 工具行与日历
  'New Record': 'workbench.database.toolbar.newRow',
  'New row': 'workbench.database.toolbar.newRow',
  'Search...': 'workbench.database.toolbar.searchPlaceholder',
  Today: 'workbench.database.menu.today',
  'Nothing here yet': 'workbench.database.menu.calendarEmpty',
  "Add a row to any date, it'll appear here on the calendar.": 'workbench.database.menu.calendarEmptyHint',
  'Add a date property': 'workbench.database.menu.addDateProperty',
  'Select a date property': 'workbench.database.menu.selectDateProperty',

  // 视图设置弹层（标题与页面）
  'View settings': 'workbench.database.menu.viewSettings',
  'View name': 'workbench.database.menu.viewName',
  Properties: 'workbench.database.menu.properties',
  Filter: 'workbench.database.menu.filter',
  Sort: 'workbench.database.menu.sort',
  Group: 'workbench.database.menu.group',
  'Group by': 'workbench.database.menu.groupBy',
  Layout: 'workbench.database.menu.layout',
  'Table View': 'workbench.database.menu.table',
  'Kanban View': 'workbench.database.menu.kanban',
  'Calendar View': 'workbench.database.menu.calendar',
  'Duplicate view': 'workbench.database.menu.duplicateView',
  'Delete view': 'workbench.database.menu.deleteView',

  // 视图设置弹层（分组 / 排序 / 通用动作）
  Add: 'workbench.database.menu.add',
  'Add sort': 'workbench.database.menu.addSort',
  Delete: 'workbench.database.menu.delete',
  'No Results': 'workbench.database.menu.noResults',
  'Remove Grouping': 'workbench.database.menu.removeGrouping',
  'Remove grouping': 'workbench.database.menu.removeGrouping',
  'Hide empty groups': 'workbench.database.menu.hideEmptyGroups',
  'Oldest first': 'workbench.database.menu.oldestFirst',
  'Newest first': 'workbench.database.menu.newestFirst',
  'Date by': 'workbench.database.menu.dateBy',
  'Start week on': 'workbench.database.menu.startWeekOn',

  // 列头菜单（截图里「Type / Property type / Hide In View / Sort Ascending…」那一列）
  Type: 'workbench.database.menu.columnType',
  'Property type': 'workbench.database.menu.propertyType',
  'Property name': 'workbench.database.menu.propertyName',
  'Hide In View': 'workbench.database.menu.hideInView',
  'Sort Ascending': 'workbench.database.menu.sortAscending',
  'Sort Descending': 'workbench.database.menu.sortDescending',
  'Insert Left Column': 'workbench.database.menu.insertLeftColumn',
  'Insert Right Column': 'workbench.database.menu.insertRightColumn',
  'Move Left': 'workbench.database.menu.moveLeft',
  'Move Right': 'workbench.database.menu.moveRight',
  Duplicate: 'workbench.database.menu.duplicate',
  'Number Format': 'workbench.database.menu.numberFormat',
  'Edit View': 'workbench.database.menu.editView',
  'Expand Row': 'workbench.database.menu.expandRow',
  'Expand Card': 'workbench.database.menu.expandCard',
  'Delete Row': 'workbench.database.menu.deleteRow',
  'Delete Rows': 'workbench.database.menu.deleteRows',
  'Delete Card': 'workbench.database.menu.deleteCard',
  'Delete Cards': 'workbench.database.menu.deleteCards',
  'Insert Before': 'workbench.database.menu.insertBefore',
  'Insert After': 'workbench.database.menu.insertAfter',
  'Move To': 'workbench.database.menu.moveTo',
  Ungroup: 'workbench.database.menu.ungroup',
  'Create date property': 'workbench.database.menu.addDateProperty',

  // 列类型名（column-type 子菜单与右键菜单里的「类型」后缀）
  Checkbox: 'workbench.database.menu.typeCheckbox',
  Date: 'workbench.database.menu.typeDate',
  'Multi-select': 'workbench.database.menu.typeMultiSelect',
  Number: 'workbench.database.menu.typeNumber',
  Progress: 'workbench.database.menu.typeProgress',
  Select: 'workbench.database.menu.typeSelect',
  Link: 'workbench.database.menu.typeLink',
  Text: 'workbench.database.menu.typeText',
  'Plain-Text': 'workbench.database.menu.typeText',
  Title: 'workbench.database.menu.typeTitle',
  'Created Time': 'workbench.database.menu.typeCreatedTime',
  'Created time': 'workbench.database.menu.typeCreatedTime',
  'Create Time': 'workbench.database.menu.typeCreatedTime',

  // 标签选择弹层
  'Type here...': 'workbench.database.menu.typeHere',
  'Type here... ': 'workbench.database.menu.typeHere',
  'Select tag or create one': 'workbench.database.menu.selectTagOrCreate',
  Create: 'workbench.database.menu.create',

  // 输入框 placeholder（走属性替换，不在文本节点里；View name / Property name 已在上方登记）
  'Type a value...': 'workbench.database.menu.typeValue',

  // 筛选运算符（视图设置 → 筛选 的下拉）
  Contains: 'workbench.database.menu.filterContains',
  'Does no contains': 'workbench.database.menu.filterNotContains',
  'Does not contains': 'workbench.database.menu.filterNotContains',
  'Starts with': 'workbench.database.menu.filterStartsWith',
  'Ends with': 'workbench.database.menu.filterEndsWith',
  Is: 'workbench.database.menu.filterIs',
  'Is not': 'workbench.database.menu.filterIsNot',
  'Is one of': 'workbench.database.menu.filterIsOneOf',
  'Is not one of': 'workbench.database.menu.filterIsNotOneOf',
  Before: 'workbench.database.menu.filterBefore',
  After: 'workbench.database.menu.filterAfter',
  'Is checked': 'workbench.database.menu.filterIsChecked',
  'Is unchecked': 'workbench.database.menu.filterIsUnchecked',
  'Contains one of': 'workbench.database.menu.filterContainsOneOf',
  'Is empty': 'workbench.database.menu.filterIsEmpty',
  'Is not empty': 'workbench.database.menu.filterIsNotEmpty',
}

/** 带数字的溢出入口：`3 More` / `2 shown` / `1 active`。 */
const MORE_PATTERN = /^(\d+)\s+More$/
const SHOWN_PATTERN = /^(\d+)\s+shown$/
const ACTIVE_PATTERN = /^(\d+)\s+active$/

/** 生成一份「英文原文 → 当前语言文案」字典；无 key 时返回 null（空转）。 */
function buildDictionary(): Record<string, string> {
  const t = i18n.global.t
  const dict: Record<string, string> = {}
  for (const [source, key] of Object.entries(TEXT_KEYS)) {
    const translated = t(key)
    // vue-i18n 找不到 key 时原样返回 key，这种值写回去只会更糟，直接跳过。
    if (translated && translated !== key) dict[source] = translated
  }
  return dict
}

/** 整句匹配 + 带数字的模式匹配；都不命中返回 null。 */
function translate(raw: string, dict: Record<string, string>): string | null {
  const text = raw.trim()
  if (!text) return null
  const direct = dict[text]
  if (direct) return direct
  const t = i18n.global.t
  const more = MORE_PATTERN.exec(text)
  if (more) return t('workbench.database.menu.more', { count: Number(more[1]) })
  const shown = SHOWN_PATTERN.exec(text)
  if (shown) return t('workbench.database.menu.shown', { count: Number(shown[1]) })
  const active = ACTIVE_PATTERN.exec(text)
  if (active) return t('workbench.database.menu.active', { count: Number(active[1]) })
  return null
}

/** 只替换文本节点本身，保留其父元素的样式与事件。 */
function walk(root: Node, dict: Record<string, string>): void {
  if (root.nodeType === Node.TEXT_NODE) {
    const next = translate(root.textContent ?? '', dict)
    if (next) (root as Text).data = next
    return
  }
  root.childNodes.forEach((child) => walk(child, dict))
}

/** 搜索框的文案在 placeholder 属性上，文本替换覆盖不到。 */
function translatePlaceholders(root: Element, dict: Record<string, string>): void {
  const inputs = root.matches('input[placeholder]')
    ? [root as HTMLInputElement]
    : Array.from(root.querySelectorAll<HTMLInputElement>('input[placeholder]'))
  for (const input of inputs) {
    const next = translate(input.placeholder, dict)
    if (next && next !== input.placeholder) input.placeholder = next
  }
}

/**
 * 就地处理一个节点。
 *
 * 分支顺序很关键：
 *   1. 元素本身在安全容器里 → 整棵子树翻译；
 *   2. 元素本身不在、但子树里有安全容器 → 只翻译那些容器（首次全量扫描走这里）；
 *   3. 文本节点 → 看父元素是否在安全容器内。
 * 只做第 3 步会让首次 `apply(host)` 完全空转（host 自己不在任何安全容器里），
 * 上游已经渲染完的文案就永远不会被替换。
 */
function apply(node: Node, dict: Record<string, string>): void {
  if (node.nodeType === Node.ELEMENT_NODE) {
    const el = node as Element
    if (el.closest(SAFE_SELECTOR)) {
      walk(el, dict)
      translatePlaceholders(el, dict)
      return
    }
    el.querySelectorAll(SAFE_SELECTOR).forEach((safe) => {
      walk(safe, dict)
      translatePlaceholders(safe, dict)
    })
    return
  }
  if (node.parentElement?.closest(SAFE_SELECTOR)) walk(node, dict)
}

/**
 * 在数据库宿主上安装文案本地化。
 *
 * 除宿主编译器内部，还额外观察 `document.body`：视图设置、列头右键、标签选择
 * 这些弹层都由上游直接 append 到 body，不在宿主子树里，只观察宿主会漏掉整个菜单。
 * body 上的观察只处理 `affine-menu` / `affine-multi-tag-select` 子树，不会碰正文与聊天区。
 *
 * 返回卸载函数。
 */
export function installDatabaseTextLocale(host: HTMLElement): () => void {
  const dict = buildDictionary()
  if (Object.keys(dict).length === 0) return () => {}

  const observer = new MutationObserver((records) => {
    for (const record of records) {
      record.addedNodes.forEach((node) => apply(node, dict))
      // 上游会在已存在节点里改文案（搜索框展开/收起、月份切换），需要跟随
      if (record.type === 'characterData') apply(record.target, dict)
      if (record.type === 'attributes') translatePlaceholders(record.target as Element, dict)
    }
  })
  observer.observe(host, {
    childList: true,
    subtree: true,
    characterData: true,
    attributes: true,
    attributeFilter: ['placeholder'],
  })
  apply(host, dict)

  // 弹层：只关心 body 上安全浮层的出现与内部重渲染。
  const menuObserver = new MutationObserver((records) => {
    for (const record of records) {
      if (record.type === 'characterData') {
        const text = record.target
        if (text.parentElement?.closest(BODY_SAFE_SELECTOR)) walk(text, dict)
        continue
      }
      record.addedNodes.forEach((node) => applyMenu(node, dict))
    }
  })
  menuObserver.observe(document.body, {
    childList: true,
    subtree: true,
    characterData: true,
    attributes: true,
    attributeFilter: ['placeholder'],
  })

  return () => {
    observer.disconnect()
    menuObserver.disconnect()
  }
}

/** body 子树里只处理新出现的安全浮层（及其内部节点）。 */
function applyMenu(node: Node, dict: Record<string, string>): void {
  if (node.nodeType !== Node.ELEMENT_NODE) return
  const el = node as Element
  if (el.closest(BODY_SAFE_SELECTOR)) {
    walk(el, dict)
    translatePlaceholders(el, dict)
    return
  }
  el.querySelectorAll(BODY_SAFE_SELECTOR).forEach((safe) => {
    walk(safe, dict)
    translatePlaceholders(safe, dict)
  })
}
