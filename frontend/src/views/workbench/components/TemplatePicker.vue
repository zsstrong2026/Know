<template>
  <!-- 模板选择器（§9.16.5）：文档区内的模态浮层，不是新路由也不是新窗口 -->
  <div class="tpl-mask" @click.self="onClose">
    <div
      ref="dialogRef"
      class="tpl-dialog"
      role="dialog"
      aria-modal="true"
      :aria-label="t('workbench.templates.title')"
      @keydown="onKeydown"
    >
      <header class="tpl-head">
        <div>
          <div class="tpl-title">{{ t('workbench.templates.title') }}</div>
          <div class="tpl-sub">{{ t('workbench.templates.subtitle') }}</div>
        </div>
        <button class="tpl-close" type="button" :aria-label="t('workbench.templates.close')" @click="onClose">
          <t-icon name="close" size="16px" />
        </button>
      </header>

      <div class="tpl-toolbar">
        <t-input
          ref="searchRef"
          v-model="keyword"
          :placeholder="t('workbench.templates.searchPlaceholder')"
          clearable
          size="small"
          class="tpl-search"
        >
          <template #prefix-icon><t-icon name="search" size="15px" /></template>
        </t-input>
        <div class="tpl-tabs" role="tablist" :aria-label="t('workbench.templates.title')">
          <button
            v-for="tab in TEMPLATE_TABS"
            :key="tab"
            type="button"
            role="tab"
            :aria-selected="activeTab === tab"
            :class="{ 'is-active': activeTab === tab }"
            @click="activeTab = tab"
          >
            {{ t(`workbench.templates.tab.${tab}`) }}
          </button>
        </div>
      </div>

      <div class="tpl-body">
        <!-- 我的模板加载失败只影响该标签，内置模板仍可用 -->
        <div v-if="activeTab === 'mine' && mine.error" class="tpl-error">
          <span>{{ t('workbench.templates.mineLoadFailed') }}</span>
          <t-button size="small" variant="outline" @click="reloadMine">
            {{ t('workbench.templates.retry') }}
          </t-button>
        </div>
        <div v-else-if="activeTab === 'mine' && mine.loading" class="tpl-loading">
          <t-loading :loading="true" size="small" />
        </div>
        <div v-else-if="activeTab === 'mine' && mine.items.length === 0" class="tpl-empty">
          <p>{{ t('workbench.templates.mineEmpty') }}</p>
          <p class="tpl-empty-hint">{{ t('workbench.templates.mineEmptyHint') }}</p>
        </div>

        <div v-if="visibleCards.length > 0" class="tpl-grid">
          <TemplateCard
            v-for="card in visibleCards"
            :key="card.key"
            :card="card"
            :applying="applyingKey === card.key"
            :failed="failedKeys.has(card.key)"
            :disabled="applyingKey !== '' && applyingKey !== card.key"
            @apply="onApply(card)"
          />
        </div>
        <div v-else-if="keyword.trim() && !(activeTab === 'mine' && mine.items.length === 0)" class="tpl-empty">
          <p>{{ t('workbench.templates.noResult') }}</p>
          <t-button size="small" variant="text" @click="keyword = ''">
            {{ t('workbench.templates.clearSearch') }}
          </t-button>
        </div>
      </div>

      <footer class="tpl-foot">
        <span class="tpl-foot-note">{{ t('workbench.templates.footNote') }}</span>
        <span class="spacer" />
        <span class="tpl-foot-esc">{{ t('workbench.templates.escHint') }}</span>
        <t-button size="small" variant="outline" @click="onBlank">
          {{ t('workbench.templates.startBlank') }}
        </t-button>
      </footer>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 模板选择器。
 *
 * 三个不能动的约束（§9.16.2）：
 *   1. 只负责「选哪个模板」，套用由父组件用快照建 session；
 *   2. 打开期间不挂载编辑器，避免把模板合进已有 Y.Doc；
 *   3. 关闭 = 从空白文档开始，且保留工具栏的「使用模板」。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import TemplateCard from './TemplateCard.vue'
import { getWorkbenchTemplateContent, listWorkbenchTemplates } from '@/api/workbench'
import {
  TEMPLATE_TABS,
  builtinCardsFor,
  fetchMineTemplates,
  filterCards,
  loadCardSnapshot,
  mineCards,
  type MineTemplatesState,
  type TemplateCardItem,
  type TemplateTab,
} from '@/workbench/templates/picker'
import { TemplateApplyError, type PreparedTemplateSnapshot } from '@/workbench/templates/apply'

const emit = defineEmits<{
  /** 选中并成功取得快照；父组件据此建 session */
  apply: [snapshot: PreparedTemplateSnapshot, card: TemplateCardItem]
  /** 关闭选择器并从空白文档开始 */
  blank: []
}>()

const { t } = useI18n()

const dialogRef = ref<HTMLElement | null>(null)
const searchRef = ref<{ focus?: () => void } | null>(null)
const keyword = ref('')
const activeTab = ref<TemplateTab>('recommended')
const mine = ref<MineTemplatesState>({ loading: true, error: false, items: [] })
const applyingKey = ref('')
const failedKeys = ref(new Set<string>())

const visibleCards = computed<TemplateCardItem[]>(() => {
  const base =
    activeTab.value === 'mine' ? mineCards(mine.value.items) : builtinCardsFor(activeTab.value)
  return filterCards(base, keyword.value)
})

async function reloadMine() {
  mine.value = { loading: true, error: false, items: [] }
  mine.value = await fetchMineTemplates(listWorkbenchTemplates)
}

async function onApply(card: TemplateCardItem) {
  if (applyingKey.value) return
  applyingKey.value = card.key
  failedKeys.value.delete(card.key)
  try {
    const snapshot = await loadCardSnapshot(card, getWorkbenchTemplateContent)
    emit('apply', snapshot, card)
  } catch (err) {
    // 单个卡片失败只在卡上标红，不弹全局错误，也不阻止用户选别的模板。
    failedKeys.value.add(card.key)
    if (err instanceof TemplateApplyError) {
      failedKeys.value = new Set(failedKeys.value)
    }
  } finally {
    applyingKey.value = ''
  }
}

function onClose() {
  emit('blank')
}

function onBlank() {
  emit('blank')
}

/** Esc 关闭；焦点陷阱保证 Tab 不会跑到浮层外的编辑器上。 */
function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    onClose()
    return
  }
  if (event.key !== 'Tab' || !dialogRef.value) return
  const focusable = dialogRef.value.querySelectorAll<HTMLElement>(
    'button:not([disabled]), input, [tabindex]:not([tabindex="-1"])',
  )
  if (focusable.length === 0) return
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first.focus()
  }
}

onMounted(async () => {
  await nextTick()
  searchRef.value?.focus?.()
  void reloadMine()
})

onBeforeUnmount(() => {
  applyingKey.value = ''
})
</script>

<style lang="less" scoped>
.tpl-mask {
  position: absolute;
  inset: 0;
  z-index: 20;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--app-space-6);
  background: rgba(0, 0, 0, 0.35);
}

.tpl-dialog {
  display: flex;
  flex-direction: column;
  width: 920px;
  max-width: 100%;
  max-height: min(720px, calc(100% - 96px));
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-3);
  overflow: hidden;
}

.tpl-head {
  display: flex;
  align-items: flex-start;
  gap: var(--app-space-3);
  padding: var(--app-space-4) var(--app-space-5) var(--app-space-3);
  border-bottom: 1px solid var(--td-component-stroke);
}

.tpl-title {
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.tpl-sub {
  margin-top: 2px;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.tpl-close {
  margin-left: auto;
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  border-radius: var(--app-radius-xs);
  color: var(--td-text-color-placeholder);

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

.tpl-toolbar {
  display: flex;
  align-items: center;
  gap: var(--app-space-3);
  padding: var(--app-space-3) var(--app-space-5);
  border-bottom: 1px solid var(--td-component-stroke);
}

.tpl-search {
  width: 240px;
}

.tpl-tabs {
  display: flex;
  gap: var(--app-space-1);

  button {
    padding: var(--app-space-1) var(--app-space-2);
    border-radius: var(--app-radius-xs);
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);

    &:hover {
      background: var(--td-bg-color-container-hover);
    }

    &.is-active {
      background: var(--td-brand-color-1);
      color: var(--td-brand-color);
    }
  }
}

.tpl-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--app-space-4) var(--app-space-5);
}

/* 1440px 4 列、1200px 3 列；卡片最小 180px，不出现横向滚动 */
.tpl-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: var(--app-space-3);
}

.tpl-error,
.tpl-empty,
.tpl-loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-6) 0;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.tpl-empty p {
  margin: 0;
}

.tpl-empty-hint {
  font-size: var(--app-text-2xs);
}

.tpl-foot {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-3) var(--app-space-5);
  border-top: 1px solid var(--td-component-stroke);
}

.tpl-foot-note,
.tpl-foot-esc {
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.spacer {
  flex: 1;
}
</style>
