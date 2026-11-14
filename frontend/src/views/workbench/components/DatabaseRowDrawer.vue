<template>
  <!--
    行详情抽屉（对齐 AFFiNE-Know原型.html 的 .db-drawer）。

    上游的行详情是一套字段编辑器弹层，结构与本原型不同；这里用只读抽屉复刻原型的
    版式：头部「表格行详情」、标题、78px 标签列 + 值的属性行。数据全部来自
    `DatabaseToolbarController.rowDetail()`，与表格共用同一份 dataSource。
    底部提供删除入口，实际删除由父组件确认后执行。
  -->
  <aside v-if="open && detail" class="db-row-drawer" role="dialog" :aria-label="$t('workbench.database.toolbar.rowDetail')">
    <div class="drawer-head">
      <t-icon name="table" size="12px" />
      <span>{{ $t('workbench.database.toolbar.rowDetail') }}</span>
      <span class="spacer" />
      <button class="drawer-close" type="button" :aria-label="$t('workbench.database.toolbar.close')" @click="$emit('close')">
        <t-icon name="close" size="13px" />
      </button>
    </div>

    <div class="drawer-body">
      <div class="drawer-title">{{ detail.title || $t('workbench.item.untitled') }}</div>
      <div v-for="field in detail.fields" :key="field.columnId" class="prop-row">
        <span class="pr-label">{{ field.label }}</span>
        <span class="pr-val">
          <!-- 状态：按选项底色取原型的三组文字色 -->
          <span v-if="field.columnId === statusColumnId && field.value" class="pill" :class="`is-${toneOf(field.color)}`">
            {{ field.value }}
          </span>
          <!-- 优先级：圆点 + 文字 -->
          <span v-else-if="field.columnId === priorityColumnId && field.value" class="prio">
            <span class="p-dot" :style="{ backgroundColor: dotColor(field.color) }" />{{ field.value }}
          </span>
          <!-- 负责人：首字头像 -->
          <span v-else-if="field.columnId === ownerColumnId && field.value" class="owner">
            <span class="avatar">{{ field.value.slice(0, 1) }}</span>{{ field.value }}
          </span>
          <!-- 截止日：日期 + 相对天数 -->
          <span v-else-if="field.type === 'date' && field.value" class="date">
            {{ field.value }}<span class="rel">{{ relativeDays(field.value) }}</span>
          </span>
          <!-- 关联文档：次要信息 -->
          <span v-else-if="field.columnId === relationColumnId" class="muted">{{ field.value || '—' }}</span>
          <span v-else class="text">{{ field.value || '—' }}</span>
        </span>
      </div>
    </div>

    <!-- 底部删除入口：只读（含回收站）时不出现，避免给出无效操作 -->
    <div v-if="!readonly" class="drawer-foot">
      <button class="drawer-delete" type="button" @click="$emit('delete')">
        <t-icon name="delete" size="13px" />
        {{ $t('workbench.database.toolbar.deleteRow') }}
      </button>
    </div>
  </aside>
</template>

<script setup lang="ts">
/**
 * 行详情抽屉：展示为主，不复制数据库状态；删除动作抛给父组件统一处理。
 *
 * 列 id 用固定常量（与 database-seed.ts 保持一致），仅用于选择渲染方式
 * （胶囊/圆点/头像），未知列一律按文本渲染。
 */
import type { DatabaseRowDetail } from '@/workbench/editor/database-session'

defineProps<{
  open: boolean
  detail: DatabaseRowDetail | null
  /** 只读时隐藏删除入口（如回收站、无编辑权限） */
  readonly?: boolean
}>()

defineEmits<{ close: []; delete: [] }>()

const statusColumnId = 'wb-col-status'
const priorityColumnId = 'wb-col-priority'
const ownerColumnId = 'wb-col-owner'
const relationColumnId = 'wb-col-relation'

/** 选项底色的 token 名 → 状态语义（与表格装饰层同一套判定）。 */
function toneOf(color?: string): string {
  if (!color) return 'gray'
  if (color.includes('blue')) return 'doing'
  if (color.includes('green')) return 'done'
  return 'todo'
}

const PRIORITY_DOTS: Array<{ match: string; color: string }> = [
  { match: 'red', color: '#e34d59' },
  { match: 'orange', color: '#ed7b2f' },
  { match: 'gray', color: '#9aa29b' },
]

function dotColor(color?: string): string {
  const hit = PRIORITY_DOTS.find((item) => color?.includes(item.match))
  return hit?.color ?? '#9aa29b'
}

/** 相对今天的天数，对应原型的「3 天后」。 */
function relativeDays(value: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})/.exec(value)
  if (!match) return ''
  const target = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3])).getTime()
  const now = new Date()
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
  const days = Math.round((target - today) / 86400000)
  if (days === 0) return '今天'
  return days > 0 ? `${days} 天后` : `逾期 ${Math.abs(days)} 天`
}
</script>

<style lang="less" scoped>
/* 原型 .db-drawer：右侧 352px、左边线 + 轻投影、无遮罩。 */
.db-row-drawer {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  z-index: 2000;
  display: flex;
  flex-direction: column;
  width: 352px;
  max-width: 92vw;
  background: var(--td-bg-color-container);
  border-left: 1px solid var(--td-component-stroke);
  box-shadow: -8px 0 24px rgba(0, 0, 0, 0.05);
}

/* 原型 .drawer-head */
.drawer-head {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 10px 12px;
  border-bottom: 1px solid #f0f1ef;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}

.drawer-head .spacer {
  flex: 1;
}

.drawer-close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  padding: 0;
  border: 0;
  border-radius: 4px;
  background: none;
  color: var(--td-text-color-secondary);
  cursor: pointer;
}

.drawer-close:hover {
  background: var(--td-bg-color-container-hover);
}

/* 原型 .drawer-body */
.drawer-body {
  flex: 1;
  min-height: 0;
  padding: 14px 16px;
  overflow: auto;
}

/* 原型 .drawer-title */
.drawer-title {
  margin-bottom: 12px;
  color: var(--td-text-color-primary);
  font-size: 16px;
  font-weight: 600;
  line-height: 1.35;
}

/* 底部删除区：固定在抽屉底部，不跟随内容滚动 */
.drawer-foot {
  flex: none;
  padding: 10px 16px;
  border-top: 1px solid #f0f1ef;
}

.drawer-delete {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 5px;
  width: 100%;
  height: 30px;
  padding: 0;
  border: 1px solid #e5e6e4;
  border-radius: 6px;
  background: none;
  color: var(--td-error-color);
  font-size: 12px;
  cursor: pointer;
}

.drawer-delete:hover {
  border-color: #f0c9c6;
  background: #fdf3f2;
}

/* 原型 .prop-row：78px 固定标签列 + 值 */
.prop-row {
  display: grid;
  grid-template-columns: 78px minmax(0, 1fr);
  gap: 8px;
  align-items: center;
  min-height: 32px;
  border-bottom: 1px solid #f5f6f4;
  font-size: 12px;
}

.pr-label {
  display: flex;
  align-items: center;
  gap: 5px;
  color: var(--td-text-color-placeholder);
  font-size: 11.5px;
}

.pr-val {
  display: flex;
  align-items: center;
  gap: 5px;
  min-width: 0;
  color: var(--td-text-color-primary);
}

/* 状态胶囊：文字色按语义取原型色 */
.pill {
  display: inline-flex;
  align-items: center;
  height: 20px;
  padding: 0 8px;
  border-radius: 999px;
  font-size: 11px;
  white-space: nowrap;
}

.pill.is-todo {
  background: #f1f1ef;
  color: #777;
}

.pill.is-doing {
  background: #e9f2ff;
  color: #3b64a8;
}

.pill.is-done {
  background: #e9f8ec;
  color: #0b7a42;
}

.pill.is-gray {
  background: #eef0f5;
  color: var(--td-text-color-secondary);
}

/* 优先级：圆点 + 文字 */
.prio {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 11px;
}

.p-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
}

/* 负责人：首字头像 */
.owner {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 11.5px;
}

.avatar {
  display: grid;
  place-items: center;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  background: #d9f1e2;
  color: #0b5c36;
  font-size: 10px;
  font-weight: 600;
}

.date {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 11.5px;
  font-variant-numeric: tabular-nums;
}

.date .rel {
  color: var(--td-text-color-placeholder);
  font-size: 10px;
}

.muted {
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}

.text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
