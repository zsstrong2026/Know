<template>
  <!-- Page AI 五组动作菜单（§9.15.2）：role=menu + 内联子菜单 + 键盘导航 -->
  <div class="pai-menu" role="menu" :aria-label="t('workbench.pageAi.menuLabel')" @keydown="onKeydown">
    <div class="pai-body">
      <template v-for="group in PAGE_AI_GROUPS" :key="group.id">
        <div class="pai-group" role="presentation">{{ t(group.labelKey) }}</div>
        <template v-for="action in actionsByGroup(group.id)" :key="action.id">
          <button
            class="pai-item"
            :class="{ 'is-disabled': isDisabled(action) }"
            type="button"
            role="menuitem"
            :aria-haspopup="action.submenu ? 'menu' : undefined"
            :aria-expanded="action.submenu ? openSub === action.submenu : undefined"
            :aria-disabled="isDisabled(action) || undefined"
            :disabled="isDisabled(action)"
            @click="onSelect(action)"
            @mouseenter="onHover"
          >
            <span class="pai-label">{{ labelOf(action) }}</span>
            <t-icon v-if="action.submenu" name="chevron-right" size="12px" class="pai-chev" />
          </button>

          <!-- 子菜单内联展开：不弹浮层，避免被编辑器容器裁切 -->
          <div v-if="action.submenu && openSub === action.submenu" class="pai-sub" role="menu">
            <button
              v-for="item in submenuItems(action.submenu)"
              :key="item.id"
              class="pai-sub-item"
              type="button"
              role="menuitem"
              @click="emitSelect(action, item.id)"
            >
              {{ item.label }}
            </button>
          </div>
        </template>
      </template>
    </div>
    <div class="pai-foot">
      <t-icon name="info-circle" size="11px" />
      <span>{{ t('workbench.pageAi.state.codeBlockHint') }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * Page AI 动作菜单。
 *
 * 只负责渲染、子菜单展开与键盘导航；请求、预览与写入全部交给父组件，
 * 这样菜单可以在浮动工具条与侧栏里复用同一份预设表。
 */
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  PAGE_AI_GROUPS,
  actionsByGroup,
  submenuItems,
  toneName,
  translateLangName,
  type PageAIAction,
  type TranslateLang,
} from '@/workbench/ai/page-actions'

const props = defineProps<{
  /** 代码块内只允许「解释选中」 */
  codeOnly?: boolean
  /** 上次选择的翻译语言，用于「翻译为 · X」的快捷展示 */
  lastLang?: TranslateLang
}>()

const emit = defineEmits<{
  /** 选中主项；submenuId 只在子菜单项上有值 */
  select: [action: PageAIAction, submenuId?: string]
  /** 关闭菜单（Esc） */
  close: []
}>()

const { t } = useI18n()
/** 当前展开的子菜单；同一时刻只展开一个（§9.15.1「菜单与斜杠互斥」的同类约束）。 */
const openSub = ref<'translate' | 'tone' | ''>('')

/** 代码块内除解释类之外一律禁用（§9.15.1）。 */
function isDisabled(action: PageAIAction): boolean {
  if (action.id === 'other.chat') return false
  return Boolean(props.codeOnly) && action.id !== 'review.explain'
}

/** 主项文案：子菜单主项带上次选择（如「翻译为 · 英语」）。 */
function labelOf(action: PageAIAction): string {
  if (action.submenu === 'translate') {
    return t(action.labelKey, { value: translateLangName(props.lastLang ?? 'en') })
  }
  if (action.submenu === 'tone') {
    return t(action.labelKey, { value: toneName('professional') })
  }
  return t(action.labelKey)
}

function onSelect(action: PageAIAction) {
  if (isDisabled(action)) return
  if (action.submenu) {
    openSub.value = openSub.value === action.submenu ? '' : action.submenu
    return
  }
  emitSelect(action)
}

function emitSelect(action: PageAIAction, submenuId?: string) {
  openSub.value = ''
  emit('select', action, submenuId)
}

function onHover() {
  // 鼠标移到别的项时收起子菜单，避免两个列表同时开着。
  if (openSub.value) openSub.value = ''
}

/** 上下键在可点项之间移动；Esc 先收子菜单，再交给父组件关菜单。 */
function onKeydown(event: KeyboardEvent) {
  const menu = event.currentTarget as HTMLElement
  const items = Array.from(menu.querySelectorAll<HTMLButtonElement>('button:not(:disabled)'))
  if (items.length === 0) return
  const index = items.findIndex((item) => item === document.activeElement)

  switch (event.key) {
    case 'ArrowDown':
      event.preventDefault()
      items[(index + 1) % items.length]?.focus()
      break
    case 'ArrowUp':
      event.preventDefault()
      items[(index - 1 + items.length) % items.length]?.focus()
      break
    case 'ArrowRight': {
      // 右/回车展开带子菜单的项（§9.15.1 无障碍要求）。
      const current = items[index]
      if (current?.getAttribute('aria-haspopup') === 'menu') {
        event.preventDefault()
        current.click()
      }
      break
    }
    case 'ArrowLeft':
      if (openSub.value) {
        event.preventDefault()
        openSub.value = ''
      }
      break
    case 'Escape':
      event.preventDefault()
      event.stopPropagation()
      if (openSub.value) {
        openSub.value = ''
        return
      }
      emit('close')
      break
    default:
      break
  }
}
</script>

<style lang="less" scoped>
.pai-menu {
  width: 240px;
  max-height: 60vh;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-2);
  overflow: hidden;
}

.pai-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--app-space-1);
}

.pai-group {
  padding: var(--app-space-2) var(--app-space-2) var(--app-space-1);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.pai-item {
  width: 100%;
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-1) var(--app-space-2);
  border-radius: var(--app-radius-xs);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
  text-align: left;

  &:hover:not(:disabled),
  &:focus-visible {
    background: var(--td-brand-color-1);
    color: var(--td-brand-color);
  }

  &:disabled {
    color: var(--td-text-color-disabled);
  }
}

.pai-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pai-chev {
  color: var(--td-text-color-placeholder);
}

.pai-sub {
  display: flex;
  flex-direction: column;
  padding: 0 var(--app-space-1) var(--app-space-1) var(--app-space-5);
}

.pai-sub-item {
  padding: var(--app-space-1) var(--app-space-2);
  border-radius: var(--app-radius-xs);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  text-align: left;

  &:hover,
  &:focus-visible {
    background: var(--td-brand-color-1);
    color: var(--td-brand-color);
  }
}

.pai-foot {
  display: flex;
  align-items: flex-start;
  gap: var(--app-space-1);
  padding: var(--app-space-2);
  border-top: 1px solid var(--td-component-stroke);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}
</style>
