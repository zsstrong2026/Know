<template>
  <!--
    关联卡片覆盖层：画布上只建了空 frame，卡片文字由这里渲染。

    为什么不用画布内元素承载文字：frame 标题会进归档正文（projection.ts 按块序拼接），
    而关联卡片是「阅读辅助」，不该污染文档正文。覆盖层完全独立于 Y.Doc，删掉也不影响内容。

    为什么整层 pointer-events: none：卡片区域正压在 frame 上，一旦接管指针事件，
    用户就没法拖动/缩放 frame 了（连线会跟着 frame 走，摆位是常见需求）。
    因此这里只负责「显示」，操作入口在关联对话框里。
  -->
  <div class="wrel-layer" aria-hidden="true">
    <div
      v-for="item in items"
      :key="item.card.frameId"
      class="wrel-card"
      :class="`is-${item.card.strength}`"
      :style="item.style"
    >
      <div class="wrel-head">
        <t-icon name="link" size="12px" />
        <span class="wrel-title">{{ item.card.title || t('workbench.relation.untitled') }}</span>
        <!--
          整层 pointer-events: none，只有这个按钮单独接管点击：
          卡片主体仍可穿透去拖动下面的 frame，避免盖住画布交互。
        -->
        <button
          class="wrel-open"
          type="button"
          :title="t('workbench.relation.open')"
          @click.stop="emit('open', item.card)"
        >
          {{ t('workbench.relation.open') }}
        </button>
      </div>
      <p v-if="item.card.reason" class="wrel-reason">{{ item.card.reason }}</p>
      <!--
        关联原句要「高亮」而不是普通引用：用户扫一眼就能看出两篇文档的哪一句对上了。
        用 <mark> 语义标签 + 淡黄底，打印与暗色主题下都可读。
      -->
      <p v-if="item.card.targetQuote" class="wrel-quote">
        <mark>{{ item.card.targetQuote }}</mark>
      </p>
      <span v-if="item.card.label" class="wrel-label">{{ item.card.label }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 关联卡片渲染层（P3）。
 *
 * 输入是「卡片状态 + 画布上 frame 的实际包围盒 + 视图矩阵」，
 * 三者都由父级在同一帧内算好传进来：这样拖动 frame、平移画布、缩放，
 * 文字都严丝合缝地贴在 frame 上。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { CanvasViewState } from '@/workbench/editor/blocksuite'
import type { RelationCardState, RelationRect } from '@/workbench/editor/canvas-relations'

const props = defineProps<{
  cards: RelationCardState[]
  /** frameId → 包围盒（模型坐标）；已被删除的 frame 不在表里 */
  rects: Map<string, RelationRect>
  view: CanvasViewState | null
}>()

const emit = defineEmits<{
  open: [card: RelationCardState]
}>()

const { t } = useI18n()

const items = computed(() =>
  props.cards
    .map((card) => {
      const rect = props.rects.get(card.frameId)
      if (!rect || !props.view) return null
      const scale = props.view.zoom * props.view.viewScale
      return {
        card,
        style: {
          left: `${(rect.x - props.view.viewportX) * scale}px`,
          top: `${(rect.y - props.view.viewportY) * scale}px`,
          width: `${rect.w}px`,
          height: `${rect.h}px`,
          // 用 transform 缩放而不是按比例改字号：卡片内部排版与画布内容一起缩放，视觉才一致
          transform: `scale(${scale})`,
        },
      }
    })
    .filter((item): item is NonNullable<typeof item> => item !== null),
)
</script>

<style scoped lang="less">
.wrel-layer {
  position: absolute;
  inset: 0;
  /*
    与 .editor-mount 完全重合：Viewport 给出的 left/top 是相对那个元素量的，
    差一个「常驻格式工具条」的高度，卡片就会整层上移 52px。
  */
  top: var(--workbench-format-bar-height, 0px);
  /*
    AI 侧栏展开时 .editor-mount 会让出右侧一块宽度，覆盖层必须跟着让，
    否则 left/top 的坐标基准与画布视口不一致（由宿主页的 .is-ai-open 规则覆盖）。
  */
  overflow: hidden;
  pointer-events: none;
  z-index: 2;
}

.wrel-card {
  position: absolute;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px 14px;
  border: 1px solid var(--td-component-stroke);
  border-left: 3px solid #0052d9;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  box-shadow: 0 2px 10px rgb(0 0 0 / 6%);
  transform-origin: 0 0;
  overflow: hidden;

  /* 弱关联降低存在感：默认不勾选，落盘后也是次要信息 */
  &.is-weak {
    border-left-color: var(--td-text-color-placeholder);
    opacity: 0.78;
  }
}

.wrel-head {
  display: flex;
  align-items: center;
  gap: 5px;
  color: #0052d9;
}

.wrel-title {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.wrel-open {
  /* 整层禁用指针，只有这里恢复：点击后跳到原文档并定位高亮 */
  pointer-events: auto;
  flex-shrink: 0;
  padding: 0 4px;
  border: none;
  border-radius: 3px;
  background: transparent;
  color: #0052d9;
  font-size: 11px;
  line-height: 16px;
  cursor: pointer;

  &:hover {
    background: #e6f2ff;
  }
}

.wrel-reason {
  margin: 0;
  font-size: 12px;
  line-height: 1.45;
  color: var(--td-text-color-secondary);
}

.wrel-quote {
  margin: 0;
  padding-left: 8px;
  border-left: 2px solid var(--td-component-stroke);
  font-size: 12px;
  line-height: 1.45;
  color: var(--td-text-color-placeholder);
  /* 关联原句最多三行，超出截断：卡片高度固定，滚动会让覆盖层与 frame 错位 */
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;

  mark {
    padding: 0 2px;
    border-radius: 2px;
    background: #fff1b8;
    color: inherit;
  }
}

.wrel-label {
  align-self: flex-start;
  margin-top: auto;
  padding: 0 6px;
  border-radius: var(--app-radius-pill);
  background: #e6f2ff;
  color: #0052d9;
  font-size: 11px;
  line-height: 18px;
}
</style>
