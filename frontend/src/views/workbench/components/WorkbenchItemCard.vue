<template>
  <!-- 条目卡片：优先展示编辑器截图封面；没有封面时用 CSS 骨架兜底，避免空卡片 -->
  <article
    class="item-card"
    :class="{ 'is-trashed': trashed }"
    :data-item-id="item.id"
    tabindex="0"
    @click="emit('open', item)"
    @keydown.enter.prevent="emit('open', item)"
    @pointermove="onPointerMove"
    @pointerleave="onPointerReset"
  >
    <div ref="coverRef" class="card-cover" :class="`cover-${coverKind}`">
      <!-- 有真实缩略图时直接铺满封面；加载中/无图时继续用骨架，避免卡片空白 -->
      <img v-if="thumbUrl" class="cover-thumb" :src="thumbUrl" alt="" />
      <template v-else>
        <!-- 文档：一张纸的骨架，让空封面也能表达「有内容」 -->
        <div v-if="coverKind === 'page'" class="cover-paper" aria-hidden="true">
          <span class="ln ln-title" />
          <span v-for="width in docLineWidths" :key="width" class="ln" :style="{ width: `${width}%` }" />
        </div>

        <!-- 画布：点阵画布 + 节点连线 -->
        <div v-else-if="coverKind === 'canvas'" class="cover-board" aria-hidden="true">
          <svg class="board-art" viewBox="0 0 240 100" preserveAspectRatio="none">
            <path class="board-edge" d="M55 33 L110 54 M120 54 L168 29" />
            <rect class="board-node" x="16" y="19" width="62" height="28" rx="4" />
            <rect class="board-node" x="104" y="41" width="70" height="28" rx="4" />
            <rect class="board-node" x="163" y="13" width="60" height="34" rx="4" />
            <line class="board-line is-strong" x1="26" y1="30" x2="62" y2="30" />
            <line class="board-line" x1="26" y1="38" x2="52" y2="38" />
            <line class="board-line is-strong" x1="114" y1="52" x2="150" y2="52" />
            <line class="board-line" x1="114" y1="60" x2="140" y2="60" />
            <line class="board-line is-strong" x1="173" y1="25" x2="211" y2="25" />
            <line class="board-line" x1="173" y1="33" x2="199" y2="33" />
          </svg>
        </div>

        <!-- 数据表：表头 + 行列骨架 -->
        <div v-else class="cover-paper cover-table" aria-hidden="true">
          <div class="table-grid">
            <span
              v-for="cell in 12"
              :key="cell"
              class="table-cell"
              :class="{ 'is-head': cell <= 3 }"
            />
          </div>
        </div>
      </template>

      <button
        class="card-star"
        :class="{ 'is-on': item.is_favorite }"
        type="button"
        :aria-label="
          $t(item.is_favorite ? 'workbench.item.unfavorite' : 'workbench.item.favorite')
        "
        :aria-pressed="item.is_favorite"
        @click.stop="emit('favorite', item)"
      >
        <t-icon name="star" size="14px" />
      </button>
    </div>

    <div class="card-body">
      <!-- 标题前展示条目自选的 emoji 图标（与详情页、列表行同源：item.icon） -->
      <div class="card-title" :title="item.title">
        <span v-if="item.icon" class="card-title-icon" aria-hidden="true">{{ item.icon }}</span>
        <span class="card-title-text">{{ item.title }}</span>
      </div>
      <div class="card-desc">
        <t-icon :name="kindIcon" size="12px" />
        <span :title="describe">{{ describe }}</span>
      </div>

      <div class="card-foot">
        <!-- 卡片只展示归档状态；归档操作统一走「…」菜单，避免底部信息拥挤 -->
        <span class="card-badge">
          <ArchiveBadge
            :status="item.archive_status"
            :archived-version="item.archived_version"
            :readonly="trashed"
            :show-action="false"
          />
        </span>
        <span class="spacer" />
        <span class="card-time" :title="absoluteTime">{{ updatedLabel }}</span>
        <t-dropdown
          :options="menuOptions"
          trigger="click"
          placement="bottom-right"
          attach="body"
          @click="onMenu"
        >
          <button class="card-more" type="button" :aria-label="$t('workbench.item.open')" @click.stop>
            <t-icon name="ellipsis" size="14px" />
          </button>
        </t-dropdown>
      </div>
    </div>

    <!-- 金属质感叠层：只在悬停/聚焦时淡入，纯视觉，不拦截卡片内的点击 -->
    <span class="card-metal" aria-hidden="true" />
  </article>
</template>

<script setup lang="ts">
/**
 * 工作台条目卡片。
 *
 * 交互全部上抛给列表页（open / favorite / archive / menu）；
 * 卡片只按需加载封面缩略图（进入视口附近才请求，且有全局缓存），
 * 其余数据都来自列表响应，不额外请求。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import ArchiveBadge from './ArchiveBadge.vue'
import { formatRelativeTime, itemCaption } from '@/workbench/item-display'
import { cachedThumbnail, loadThumbnail } from '@/workbench/thumbnail-cache'
import {
  buildItemMenuOptions,
  resolveItemMenuAction,
  type WorkbenchItemMenuAction,
} from '@/workbench/item-menu'
import type { WorkbenchItem } from '@/types/workbench'

const props = withDefaults(
  defineProps<{
    item: WorkbenchItem
    /** 回收站里的条目只提供恢复/永久删除，不提供归档操作 */
    trashed?: boolean
  }>(),
  { trashed: false },
)

const emit = defineEmits<{
  open: [item: WorkbenchItem]
  favorite: [item: WorkbenchItem]
  /** 「…」菜单里的动作统一上抛，由列表页派发，卡片不自己 switch */
  menu: [item: WorkbenchItem, action: WorkbenchItemMenuAction]
}>()

const { t } = useI18n()

/** 封面造型：数据库是独立实体；文档与画布按当前查看模式区分，不当作两类条目 */
const coverKind = computed(() => {
  if (props.item.kind === 'database') return 'database'
  return (props.item.current_mode ?? 'edgeless') === 'page' ? 'page' : 'canvas'
})

const kindIcon = computed(() => {
  if (coverKind.value === 'database') return 'table'
  return coverKind.value === 'page' ? 'file' : 'image'
})

/** 文档骨架的正文行宽，固定值避免每次渲染抖动 */
const docLineWidths = [92, 84, 88, 56, 74]

const describe = computed(() => itemCaption(props.item, t))
const updatedLabel = computed(() => formatRelativeTime(props.item.updated_at))
const menuOptions = computed(() => buildItemMenuOptions(props.item, props.trashed, t))

/** 悬停提示用绝对时间，相对时间只负责扫读 */
const absoluteTime = computed(() => {
  const time = new Date(props.item.updated_at)
  return Number.isFinite(time.getTime()) ? time.toLocaleString() : ''
})

function onMenu(data: { value?: string | number }) {
  const action = resolveItemMenuAction(data.value)
  if (action) emit('menu', props.item, action)
}

// ---- 金属质感：悬停时高光与轻微倾斜跟随指针，离开即归位 ----
const reduceMotion =
  typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
let tiltFrame = 0

/** 指针坐标写成 CSS 变量；rAF 节流，避免高频 move 反复触发样式计算 */
function onPointerMove(event: PointerEvent) {
  if (event.pointerType !== 'mouse') return
  const target = event.currentTarget as HTMLElement
  const { clientX, clientY } = event
  if (tiltFrame) return
  tiltFrame = requestAnimationFrame(() => {
    tiltFrame = 0
    const rect = target.getBoundingClientRect()
    if (!rect.width || !rect.height) return
    const x = (clientX - rect.left) / rect.width
    const y = (clientY - rect.top) / rect.height
    target.style.setProperty('--metal-x', `${(x * 100).toFixed(2)}%`)
    target.style.setProperty('--metal-y', `${(y * 100).toFixed(2)}%`)
    if (reduceMotion) return
    // 幅度控制在 ±3deg，只做「面向指针」的轻微视差，不做夸张翻面
    target.style.setProperty('--metal-tilt-x', `${(-(y - 0.5) * 6).toFixed(2)}deg`)
    target.style.setProperty('--metal-tilt-y', `${((x - 0.5) * 6).toFixed(2)}deg`)
  })
}

function onPointerReset(event: PointerEvent) {
  if (event.pointerType !== 'mouse') return
  const target = event.currentTarget as HTMLElement
  cancelAnimationFrame(tiltFrame)
  tiltFrame = 0
  target.style.removeProperty('--metal-x')
  target.style.removeProperty('--metal-y')
  target.style.removeProperty('--metal-tilt-x')
  target.style.removeProperty('--metal-tilt-y')
}

// ---- 封面缩略图：进入视口附近才发起请求，避免一页 30 张同时抢带宽 ----
const coverRef = ref<HTMLElement | null>(null)
const thumbUrl = ref('')
/** 已发起请求的 key，防止 watch 与 observer 重复触发 */
let requestedKey = ''
let observer: IntersectionObserver | null = null

/** 版本随内容更新；key 变化即视为另一张图。 */
function thumbnailKey(): string {
  return props.item.thumbnail_version ? `${props.item.id}:${props.item.thumbnail_version}` : ''
}

function requestThumbnail() {
  const key = thumbnailKey()
  if (!key || requestedKey === key) return
  requestedKey = key
  const cached = cachedThumbnail(props.item)
  if (cached) {
    thumbUrl.value = cached
    return
  }
  void loadThumbnail(props.item).then((url) => {
    // 加载期间条目可能已被替换（同一路由切换），只接受当前 key 的结果。
    if (url && thumbnailKey() === key) thumbUrl.value = url
  })
}

function resetThumbnail() {
  observer?.disconnect()
  observer = null
  thumbUrl.value = ''
  requestedKey = ''

  const key = thumbnailKey()
  if (!key) return
  const cached = cachedThumbnail(props.item)
  if (cached) {
    requestedKey = key
    thumbUrl.value = cached
    return
  }
  if (typeof IntersectionObserver === 'undefined') {
    requestThumbnail()
    return
  }
  observer = new IntersectionObserver(
    (entries) => {
      if (!entries.some((entry) => entry.isIntersecting)) return
      observer?.disconnect()
      observer = null
      requestThumbnail()
    },
    // 提前 200px 预取，滚动到卡片时图片大概率已经就绪。
    { rootMargin: '200px' },
  )
  if (coverRef.value) observer.observe(coverRef.value)
}

onMounted(resetThumbnail)
watch(() => `${props.item.id}:${props.item.thumbnail_version}`, resetThumbnail)
onBeforeUnmount(() => {
  observer?.disconnect()
  cancelAnimationFrame(tiltFrame)
})
</script>

<style lang="less" scoped>
.item-card {
  /* 金属质感配色：亮部/暗部/边缘/扫光带，暗色主题下换成低亮度高光 */
  --metal-hi: rgba(255, 255, 255, 0.34);
  --metal-shade: rgba(20, 24, 32, 0.09);
  --metal-edge: rgba(255, 255, 255, 0.8);
  --metal-band: rgba(255, 255, 255, 0.55);

  position: relative;
  display: flex;
  flex-direction: column;
  min-width: 0;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
  overflow: hidden;
  cursor: pointer;
  /* 3D 透视只服务于悬停倾斜，未悬停时角度为 0，视觉与原来一致 */
  transform: perspective(1000px) rotateX(var(--metal-tilt-x, 0deg))
    rotateY(var(--metal-tilt-y, 0deg)) translateY(0);
  transition:
    border-color var(--app-motion-fast),
    box-shadow var(--app-motion-fast),
    transform var(--app-motion-fast);

  &:hover {
    border-color: var(--td-component-border);
    box-shadow: var(--td-shadow-1);
    transform: perspective(1000px) rotateX(var(--metal-tilt-x, 0deg))
      rotateY(var(--metal-tilt-y, 0deg)) translateY(-2px);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color);
    outline-offset: 1px;
  }
}

/* 暗色主题：白高光更收敛，靠细亮边表达金属边缘 */
:global(:root[theme-mode='dark']) .item-card {
  --metal-hi: rgba(255, 255, 255, 0.16);
  --metal-shade: rgba(255, 255, 255, 0.05);
  --metal-edge: rgba(255, 255, 255, 0.2);
  --metal-band: rgba(255, 255, 255, 0.16);
}

/* 金属叠层：默认完全透明，悬停/聚焦才淡入；不拦截卡片内任何点击 */
.card-metal {
  position: absolute;
  inset: 0;
  z-index: 1;
  border-radius: inherit;
  pointer-events: none;
  opacity: 0;
  overflow: hidden;
  transition: opacity var(--app-motion-fast);
  background-image:
    radial-gradient(
      220px circle at var(--metal-x, 50%) var(--metal-y, 0%),
      var(--metal-hi),
      transparent 62%
    ),
    repeating-linear-gradient(
      100deg,
      rgba(255, 255, 255, 0.04) 0 1px,
      transparent 1px 4px
    ),
    linear-gradient(
      140deg,
      var(--metal-hi) 0%,
      transparent 38%,
      var(--metal-shade) 64%,
      rgba(255, 255, 255, 0.2) 100%
    );
  box-shadow:
    inset 0 0 0 1px var(--metal-edge),
    inset 0 1px 0 rgba(255, 255, 255, 0.65);

  /* 扫光带：悬停瞬间从右向左扫过一次，模拟金属表面掠过光源 */
  &::after {
    content: '';
    position: absolute;
    inset: 0;
    background: linear-gradient(
      105deg,
      transparent 42%,
      var(--metal-band) 50%,
      transparent 58%
    );
    background-size: 250% 100%;
    background-position: 130% 0;
  }
}

.item-card:hover .card-metal,
.item-card:focus-visible .card-metal {
  opacity: 1;

  &::after {
    animation: card-metal-sweep 900ms ease-out;
  }
}

@keyframes card-metal-sweep {
  from {
    background-position: 130% 0;
  }

  to {
    background-position: -30% 0;
  }
}

/* 尊重系统「减少动态效果」：只保留高光，不做倾斜与扫光 */
@media (prefers-reduced-motion: reduce) {
  .item-card {
    transition: none;
  }

  .item-card:hover {
    transform: none;
  }

  .card-metal::after {
    animation: none;
  }
}

/* 封面底：浅色衬底 + 内嵌缩略，缩略溢出部分由封面裁掉 */
.card-cover {
  /* 暗色下 container 比 secondarycontainer 更深，这里统一把「纸」提亮，
     保证两种主题里纸页都浮在衬底之上 */
  --card-paper: color-mix(in srgb, var(--td-bg-color-container) 94%, #fff);

  position: relative;
  height: 112px;
  flex: none;
  padding: 12px 14px 0;
  overflow: hidden;
  border-bottom: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
}

/* 真实封面：铺满封面区，文档从顶部对齐，避免看到大片空白正文 */
.cover-thumb {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  display: block;
  object-fit: cover;
  object-position: top center;
  background: var(--card-paper);
}

/* 纸页：文档与数据表共用，贴着封面底边裁切，像从下方抽出来的一张纸 */
.cover-paper {
  height: 100%;
  padding: 12px 12px 0;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-bottom: 0;
  border-radius: var(--app-radius-sm) var(--app-radius-sm) 0 0;
  background: var(--card-paper);
}

.ln {
  display: block;
  height: 4px;
  margin-top: 7px;
  border-radius: 2px;
  background: color-mix(in srgb, var(--td-text-color-primary) 7%, transparent);
}

.ln-title {
  width: 52%;
  height: 6px;
  margin-top: 0;
  background: color-mix(in srgb, var(--td-text-color-primary) 22%, transparent);
}

/* 画布：点阵 + 节点，节点颜色与卡片背景一致，像贴在画布上的便签 */
.cover-board {
  height: 100%;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-bottom: 0;
  border-radius: var(--app-radius-sm) var(--app-radius-sm) 0 0;
  background-color: var(--card-paper);
  background-image: radial-gradient(
    color-mix(in srgb, var(--td-text-color-primary) 13%, transparent) 1px,
    transparent 1px
  );
  background-size: 12px 12px;
}

.board-art {
  display: block;
  width: 100%;
  height: 100%;
}

.board-edge {
  fill: none;
  stroke: var(--td-component-stroke);
  stroke-width: 1;
}

.board-node {
  fill: var(--card-paper);
  stroke: var(--td-component-stroke);
}

.board-line {
  stroke: color-mix(in srgb, var(--td-text-color-primary) 7%, transparent);
  stroke-width: 3;
  stroke-linecap: round;

  &.is-strong {
    stroke: color-mix(in srgb, var(--td-text-color-primary) 22%, transparent);
  }
}

/* 数据表：三列网格，首行加深当作表头 */
.cover-table {
  padding: 8px 0 0;
}

.table-grid {
  display: grid;
  grid-template-columns: 1.4fr 1fr 1fr;
}

.table-cell {
  display: flex;
  align-items: center;
  height: 18px;
  padding: 0 8px;
  border-top: 1px solid var(--td-component-stroke);

  &:nth-child(-n + 3) {
    border-top: 0;
  }

  &::after {
    content: '';
    display: block;
    width: 64%;
    height: 4px;
    border-radius: 2px;
    background: color-mix(in srgb, var(--td-text-color-primary) 7%, transparent);
  }

  &.is-head::after {
    width: 72%;
    height: 5px;
    background: color-mix(in srgb, var(--td-text-color-primary) 22%, transparent);
  }
}

.card-star {
  position: absolute;
  right: 8px;
  top: 8px;
  z-index: 2;
  width: 24px;
  height: 24px;
  display: grid;
  place-items: center;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  color: var(--td-text-color-placeholder);
  opacity: 0;
  transition:
    opacity var(--app-motion-fast),
    color var(--app-motion-fast);

  &.is-on {
    opacity: 1;
    color: var(--td-warning-color);
  }
}

.item-card:hover .card-star,
.item-card:focus-within .card-star {
  opacity: 1;
}

.card-body {
  display: flex;
  flex-direction: column;
  gap: 6px;
  flex: 1;
  padding: 10px 12px 12px;
}

.card-title {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  font-size: var(--app-text-md);
  font-weight: 600;
  line-height: 18px;
  color: var(--td-text-color-primary);
}

/* emoji 图标固定宽度，不参与压缩，避免长标题把它挤没 */
.card-title-icon {
  flex: none;
  font-size: 15px;
  line-height: 18px;
}

.card-title-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 类型与日期合并成一行，图标帮助扫读，不再单独占一行 */
.card-desc {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  min-width: 0;
  font-size: var(--app-text-xs);
  line-height: 16px;
  color: var(--td-text-color-placeholder);

  span {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.card-foot {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  margin-top: auto;
  padding-top: 2px;
  min-height: 20px;
}

.card-badge {
  display: flex;
  align-items: center;
  min-width: 0;
}

.spacer {
  flex: 1;
}

.card-time {
  flex: none;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
}

.card-more {
  position: relative;
  /* 高于金属叠层，避免「…」图标被高光冲淡 */
  z-index: 2;
  width: 22px;
  height: 22px;
  display: grid;
  place-items: center;
  border-radius: var(--app-radius-xs);
  color: var(--td-text-color-placeholder);
  opacity: 0.55;
  transition:
    opacity var(--app-motion-fast),
    background var(--app-motion-fast);

  &:hover {
    opacity: 1;
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

.item-card:hover .card-more,
.item-card:focus-within .card-more {
  opacity: 1;
}
</style>
