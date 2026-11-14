import { DatabaseSelection } from '@blocksuite/affine-block-database';
import { EdgelessLegacySlotIdentifier } from '@blocksuite/affine-block-surface';
import { TableSelection } from '@blocksuite/affine-block-table';
import {
  darkToolbarStyles,
  type EditorMenuButton,
  EditorToolbar,
  lightToolbarStyles,
} from '@blocksuite/affine-components/toolbar';
import {
  CodeBlockModel,
  ImageBlockModel,
  ListBlockModel,
  ParagraphBlockModel,
} from '@blocksuite/affine-model';
import {
  ToolbarContext,
  ToolbarFlag as Flag,
  ToolbarRegistryIdentifier,
} from '@blocksuite/affine-shared/services';
import { unsafeCSSVar, unsafeCSSVarV2 } from '@blocksuite/affine-shared/theme';
import { matchModels } from '@blocksuite/affine-shared/utils';
import { IS_MOBILE } from '@blocksuite/global/env';
import {
  Bound,
  getCommonBound,
  getCommonBoundWithRotation,
} from '@blocksuite/global/gfx';
import { nextTick } from '@blocksuite/global/utils';
import {
  type BlockComponent,
  BlockSelection,
  TextSelection,
  WidgetComponent,
} from '@blocksuite/std';
import {
  GfxBlockElementModel,
  type GfxController,
  type GfxModel,
  GfxPrimitiveElementModel,
} from '@blocksuite/std/gfx';
import { RANGE_SYNC_EXCLUDE_ATTR } from '@blocksuite/std/inline';
import type { ReferenceElement, SideObject } from '@floating-ui/dom';
import { batch, effect, signal } from '@preact/signals-core';
import { css, unsafeCSS } from 'lit';
import groupBy from 'lodash-es/groupBy';
import throttle from 'lodash-es/throttle';
import toPairs from 'lodash-es/toPairs';

import { autoUpdatePosition, renderToolbar, sideMap } from './utils';

export const AFFINE_TOOLBAR_WIDGET = 'affine-toolbar-widget';

/**
 * 正文格式工具条固定在编辑区顶部时，工具条下沿与正文之间保留的间距。
 *
 * 宿主被工作台下移了一条带子（.has-format-bar，默认 52px），工具条就落在这条
 * 带子里；定位时按工具条自身高度反推 top，保证任何主题/缩放下都不压住正文首行。
 */
const PAGE_TEXT_TOOLBAR_GAP = 8;

/** 工具条尚未渲染（offsetHeight 为 0）时的兜底高度，与 EditorToolbar 的 36px 对齐。 */
const PAGE_TEXT_TOOLBAR_FALLBACK_HEIGHT = 36;

export class AffineToolbarWidget extends WidgetComponent {
  static override styles = css`
    editor-toolbar {
      position: absolute;
      top: 0;
      left: 0;
      opacity: 0;
      display: none;
      width: max-content;
      touch-action: none;
      backface-visibility: hidden;
      z-index: var(--affine-z-index-popover);

      will-change: opacity, overlay, display, transform;
      transition-property: opacity, overlay, display;
      transition-duration: 120ms;
      transition-timing-function: ease-out;
      transition-behavior: allow-discrete;
    }

    editor-toolbar[data-open] {
      display: flex;
      opacity: 1;
      transition-timing-function: ease-in;

      @starting-style {
        opacity: 0;
      }
    }

    editor-toolbar[data-open][data-inline='true'] {
      transition-property: opacity, overlay, display, transform;
      transition-timing-function: ease;
    }

    editor-toolbar[data-placement='inner'] {
      background-color: unset;
      box-shadow: unset;
      height: fit-content;
      padding-top: 4px;
      border-radius: 0;
      border: unset;
      justify-content: flex-end;
      box-sizing: border-box;
      gap: 4px;

      .inner-button,
      editor-icon-button,
      editor-menu-button {
        color: ${unsafeCSSVarV2('text/primary')};
        box-shadow: ${unsafeCSSVar('buttonShadow')};
        background: ${unsafeCSSVar('white')};
        border-radius: 4px;
      }
      editor-menu-button > div {
        gap: 4px;
      }
    }

    editor-toolbar[data-mobile='true'] {
      position: fixed;
      top: auto;
      left: 50%;
      bottom: 16px;
      transform: translateX(-50%);
      max-width: calc(100vw - 32px);
      overflow-x: auto;
      touch-action: pan-x;
    }

    /* 正文文本选区：工具条固定在编辑区顶部，不跟随选区浮动。
       位置由 JS 写入 inline left/top，这里只兜窄屏下的换行与滚动。 */
    editor-toolbar[data-fixed] {
      /* 顶栏是工作台预留的一条实体带子：去掉浮层的圆角 / 描边 / 阴影，
         让工具条贴在这条带子里，而不是看起来浮在正文上方。 */
      background: transparent;
      border: none;
      border-radius: 0;
      box-shadow: none;
      padding: 0 4px;
      max-width: calc(100vw - 32px);
      overflow-x: auto;
      touch-action: pan-x;
      scrollbar-width: none;
    }

    editor-toolbar[data-fixed]::-webkit-scrollbar {
      display: none;
    }

    ${unsafeCSS(darkToolbarStyles('editor-toolbar'))}
    ${unsafeCSS(lightToolbarStyles('editor-toolbar'))}
  `;

  sideOptions$ = signal<Partial<SideObject> | null>(null);

  referenceElement$ = signal<(() => ReferenceElement | null) | null>(null);

  setReferenceElementWithRange(range: Range | null) {
    this.referenceElement$.value = range
      ? () => ({
          getBoundingClientRect: () => range.getBoundingClientRect(),
          getClientRects: () =>
            Array.from(range.getClientRects()).filter(rect =>
              Math.round(rect.width)
            ),
        })
      : null;
  }

  setReferenceElementWithHtmlElement(element: Element | null) {
    this.referenceElement$.value = element ? () => element : null;
  }

  setReferenceElementWithBlocks(blocks: BlockComponent[]) {
    let cachedClientRects: DOMRect[] | null = null;
    const getClientRects = () => {
      if (!cachedClientRects) {
        cachedClientRects = blocks.map(e => e.getBoundingClientRect());
        requestAnimationFrame(() => {
          cachedClientRects = null;
        });
      }
      return cachedClientRects;
    };

    this.referenceElement$.value = blocks.length
      ? () => ({
          getBoundingClientRect: () => {
            const rects = getClientRects();
            const bounds = getCommonBound(rects.map(Bound.fromDOMRect));
            if (!bounds) return rects[0];
            return new DOMRect(bounds.x, bounds.y, bounds.w, bounds.h);
          },
          getClientRects,
        })
      : null;
  }

  setReferenceElementWithElements(gfx: GfxController, elements: GfxModel[]) {
    const surfaceBounds = getCommonBoundWithRotation(elements);

    const getBoundingClientRect = () => {
      const { x: offsetX, y: offsetY } = this.getBoundingClientRect();
      const [x, y, w, h] = gfx.viewport.toViewBound(surfaceBounds).toXYWH();
      const rect = new DOMRect(x + offsetX, y + offsetY, w, h);
      return rect;
    };

    this.referenceElement$.value = elements.length
      ? () => ({
          getBoundingClientRect,
          getClientRects: () => [getBoundingClientRect()],
        })
      : null;
  }

  updateWithSurface(
    ctx: ToolbarContext,
    activated: boolean,
    elementIds: string[]
  ) {
    const gfx = ctx.gfx;
    const surface = gfx.surface;
    let flavour = 'affine:surface';
    let elements: GfxModel[] = [];
    let hasLocked = false;
    let sideOptions = null;
    let paired: [string, GfxModel[]][] = [];

    if (activated && surface) {
      elements = elementIds
        .map(id => gfx.getElementById(id))
        .filter(model => model !== null) as GfxModel[];

      // Should double check
      activated &&= Boolean(elements.length);

      hasLocked = elements.some(e => e.isLocked());

      const grouped = groupBy(
        elements.map(model => {
          let flavour = surface.flavour;

          if (model instanceof GfxBlockElementModel) {
            flavour += `:${model.flavour.split(':').pop()}`;
          } else if (model instanceof GfxPrimitiveElementModel) {
            flavour += `:${model.type}`;
          }

          return { model, flavour };
        }),
        e => e.flavour
      );

      paired = toPairs(grouped).map(([flavour, items]) => [
        flavour,
        items.map(({ model }) => model),
      ]);

      if (hasLocked) {
        flavour = 'affine:surface:locked';
      } else {
        if (paired.length === 1) {
          flavour = paired[0][0];
          if (flavour === 'affine:surface:shape' && paired[0][1].length === 1) {
            sideOptions = sideMap.get(flavour) ?? null;
          }
        }
      }
      if (!sideOptions) {
        const flavours = new Set(paired.map(([f]) => f));
        if (flavours.has('affine:surface:frame')) {
          sideOptions = sideMap.get('affine:surface:frame') ?? null;
        } else if (flavours.has('affine:surface:group')) {
          sideOptions = sideMap.get('affine:surface:group') ?? null;
        }
      }
    }

    batch(() => {
      ctx.flags.toggle(Flag.Surface, activated);

      ctx.elementsMap$.value = new Map(paired);

      if (!activated || !flavour) return;

      this.setReferenceElementWithElements(gfx, elements);

      this.sideOptions$.value = sideOptions;
      ctx.flavour$.value = flavour;
      ctx.placement$.value = hasLocked ? 'top' : 'top-start';
      ctx.flags.refresh(Flag.Surface);
    });
  }

  toolbar = new EditorToolbar();

  get toolbarRegistry() {
    return this.std.get(ToolbarRegistryIdentifier);
  }

  override connectedCallback() {
    super.connectedCallback();

    this.setAttribute(RANGE_SYNC_EXCLUDE_ATTR, 'true');

    const {
      sideOptions$,
      referenceElement$,
      disposables,
      toolbar,
      toolbarRegistry,
      host,
      std,
    } = this;
    const { flags, flavour$, message$, placement$ } = toolbarRegistry;
    const context = new ToolbarContext(std);

    const isNativeTextSelection = () => {
      const dbSel = std.selection.find(DatabaseSelection);
      const dbViewSel = dbSel?.viewSelection;
      if (
        dbViewSel &&
        ((dbViewSel.selectionType === 'area' && dbViewSel.isEditing) ||
          (dbViewSel.selectionType === 'cell' && dbViewSel.isEditing))
      ) {
        return true;
      }

      const tableViewSelection = std.selection.find(TableSelection)?.data;
      return tableViewSelection?.type === 'area';
    };

    let updateMobilePosition: (() => void) | null = null;

    if (IS_MOBILE) {
      toolbar.dataset.mobile = 'true';
      this.shadowRoot!.append(toolbar);

      // Position toolbar above virtual keyboard using Visual Viewport API
      updateMobilePosition = () => {
        const vv = window.visualViewport;
        if (!vv) return;
        const keyboardHeight = window.innerHeight - vv.height - vv.offsetTop;
        toolbar.style.bottom = `${Math.max(16, keyboardHeight + 16)}px`;
      };
      if (window.visualViewport) {
        disposables.addFromEvent(
          window.visualViewport,
          'resize',
          updateMobilePosition
        );
        disposables.addFromEvent(
          window.visualViewport,
          'scroll',
          updateMobilePosition
        );
      }

      // Keep mobile selection in sync with toolbar flags. On some mobile browsers,
      // long-press selection may skip the std selection stream intermittently.
      const syncMobileTextSelection = () => {
        if (!context.activated) {
          flags.toggle(Flag.Text, false);
          return;
        }
        if (isNativeTextSelection()) {
          flags.toggle(Flag.Text, false);
          return;
        }

        const selection = window.getSelection();
        const hasSelection =
          selection &&
          selection.rangeCount > 0 &&
          !selection.isCollapsed &&
          selection.toString().length > 0;
        const range = hasSelection ? selection.getRangeAt(0) : null;
        const inEditor = Boolean(
          range && host.contains(range.commonAncestorContainer)
        );

        batch(() => {
          flags.toggle(Flag.Text, inEditor);

          if (!inEditor || !range) return;

          this.setReferenceElementWithRange(range);

          sideOptions$.value = null;
          flavour$.value = 'affine:note';
          placement$.value = toolbarRegistry.getModulePlacement('affine:note');
          flags.refresh(Flag.Text);
        });
      };

      let selectionTimeout: ReturnType<typeof setTimeout> | null = null;
      let touchTimeout: ReturnType<typeof setTimeout> | null = null;
      const scheduleSyncMobileTextSelection = (delay: number) => {
        if (selectionTimeout) clearTimeout(selectionTimeout);
        selectionTimeout = setTimeout(syncMobileTextSelection, delay);
      };
      const scheduleTouchSync = (delay: number) => {
        if (touchTimeout) clearTimeout(touchTimeout);
        touchTimeout = setTimeout(syncMobileTextSelection, delay);
      };
      disposables.addFromEvent(document, 'selectionchange', () => {
        scheduleSyncMobileTextSelection(50);
      });
      disposables.addFromEvent(host, 'touchend', () => {
        scheduleTouchSync(100);
      });
      disposables.add(() => {
        if (selectionTimeout) clearTimeout(selectionTimeout);
        if (touchTimeout) clearTimeout(touchTimeout);
      });

      // Ensures a stable initial offset before the first viewport event arrives.
      updateMobilePosition?.();
    } else {
      this.shadowRoot!.append(toolbar);
    }

    // Formatting
    // Text caret or selection in note.
    //
    // 与上游的差异：不再要求选区非折叠，也不要求编辑器处于事件活跃态。
    // 工作台要的是「常驻顶部工具栏」：光标落在正文里就显示，
    // 点侧栏 AI 输入框时不消失；无选区时按钮作用于光标所在块。
    disposables.add(
      std.selection.find$(TextSelection).subscribe(result => {
        const range = std.range.value ?? null;
        const activated = Boolean(!this.store.readonly && result);

        batch(() => {
          flags.toggle(Flag.Text, activated);

          if (!activated) return;

          this.setReferenceElementWithRange(range);

          sideOptions$.value = null;
          flavour$.value = 'affine:note';
          placement$.value = toolbarRegistry.getModulePlacement('affine:note');
          flags.refresh(Flag.Text);
        });
      })
    );

    // Formatting
    // Selects `native` text in database's cell or in table.
    disposables.addFromEvent(document, 'selectionchange', () => {
      const range = std.range.value ?? null;
      let activated = context.activated && Boolean(range && !range.collapsed);

      if (activated) {
        activated = isNativeTextSelection();
      }

      batch(() => {
        // 正文格式条常驻：焦点移到编辑器外（例如点侧栏 AI 输入框）时不再强制关闭，
        // 否则常驻工具栏会在用户一离开正文就消失。只读与切文档仍由 selection 订阅处理。
        flags.toggle(Flag.Native, activated);

        if (!activated) return;

        this.setReferenceElementWithRange(range);

        sideOptions$.value = null;
        flavour$.value = 'affine:note';
        placement$.value = toolbarRegistry.getModulePlacement('affine:note');
        flags.refresh(Flag.Native);
      });
    });

    // Selects blocks in note.
    disposables.add(
      std.selection.filter$(BlockSelection).subscribe(selections => {
        const blockIds = selections.map(s => s.blockId);
        const count = blockIds.length;
        let flavour = 'affine:note';
        let activated = context.activated && Boolean(count);

        if (activated) {
          // Handles a signal block.
          const block = count === 1 && std.store.getBlock(blockIds[0]);

          // Chencks if block's config exists.
          if (block) {
            const modelFlavour = block.model.flavour;
            const existed =
              toolbarRegistry.modules.has(modelFlavour) ||
              toolbarRegistry.modules.has(`custom:${modelFlavour}`);
            if (existed) {
              flavour = modelFlavour;
            } else {
              activated = matchModels(block.model, [
                ParagraphBlockModel,
                ListBlockModel,
                CodeBlockModel,
                ImageBlockModel,
              ]);
            }
          }
        }

        batch(() => {
          flags.toggle(Flag.Block, activated);

          if (!activated) return;

          this.setReferenceElementWithBlocks(
            blockIds
              .map(id => std.view.getBlock(id))
              .filter(block => block !== null)
          );

          sideOptions$.value = null;
          flavour$.value = flavour;
          placement$.value = toolbarRegistry.getModulePlacement(
            flavour,
            flavour === 'affine:note' ? 'top' : 'top-start'
          );
          flags.refresh(Flag.Block);
        });
      })
    );

    // Selects elements in edgeless.
    // Triggered only when not in editing state.
    disposables.add(
      context.gfx.selection.slots.updated.subscribe(selections => {
        // Should remove selections when clicking on frame navigator
        if (context.isPageMode) {
          if (
            std.host.contains(std.range.value?.commonAncestorContainer ?? null)
          ) {
            std.range.clear();
          }
          context.reset();
          return;
        }

        const elementIds = selections.flatMap(s =>
          s.editing || s.inoperable ? [] : s.elements
        );
        const count = elementIds.length;
        const activated = context.activated && Boolean(count);

        this.updateWithSurface(context, activated, elementIds);
      })
    );

    disposables.add(
      std.selection.slots.changed.subscribe(selections => {
        if (!context.activated) return;

        const value = flags.value$.peek();
        if (flags.contains(Flag.Hovering | Flag.Hiding, value)) return;
        if (!flags.check(Flag.Text, value)) return;

        const hasTextSelection =
          selections.filter(s => s.is(TextSelection)).length > 0;
        if (!hasTextSelection) return;

        const range = std.range.value ?? null;

        this.setReferenceElementWithRange(range);

        // TODO(@fundon): maybe here can be further optimized
        // 1. Prevents flickering effects.
        // 2. We cannot use `host.getUpdateComplete()` here
        // because it would cause excessive DOM queries, leading to UI jamming.
        nextTick()
          .then(() => flags.refresh(Flag.Text))
          .catch(console.error);
      })
    );

    // Handles blocks when adding
    // TODO(@fundon): improve these cases
    // Waits until the view is created when switching the view mode.
    // `card view` or `embed view`
    disposables.add(
      std.view.viewUpdated.subscribe(record => {
        const hasAdded = record.type === 'block' && record.method === 'add';
        if (!hasAdded) return;

        if (flags.isBlock()) {
          const blockIds = std.selection
            .filter$(BlockSelection)
            .peek()
            .map(s => s.blockId);
          if (blockIds.includes(record.id)) {
            batch(() => {
              this.setReferenceElementWithBlocks(
                blockIds
                  .map(id => std.view.getBlock(id))
                  .filter(block => block !== null)
              );
              flags.refresh(Flag.Block);
            });
          }
          return;
        }

        if (flags.isSurface()) {
          flags.refresh(Flag.Surface);
          return;
        }
      })
    );

    // Handles blocks when updating
    disposables.add(
      // TODO(@fundon): use rxjs' filter
      std.store.slots.blockUpdated.subscribe(record => {
        const hasUpdated = record.type === 'update';
        if (!hasUpdated) return;

        if (flags.isBlock()) {
          const blockIds = std.selection
            .filter$(BlockSelection)
            .peek()
            .map(s => s.blockId);
          if (blockIds.includes(record.id)) {
            batch(() => {
              this.setReferenceElementWithBlocks(
                blockIds
                  .map(id => std.view.getBlock(id))
                  .filter(block => block !== null)
              );
              flags.refresh(Flag.Block);
            });
          }
          return;
        }

        if (flags.isSurface()) {
          const elementIds = context.gfx.selection.selectedIds;
          this.updateWithSurface(
            context,
            context.activated && Boolean(elementIds.length),
            elementIds
          );
          return;
        }
      })
    );

    // Handles elements when updating
    disposables.add(
      context.gfx.surface$.subscribe(surface => {
        if (!surface) return;

        const subscription = surface.elementUpdated.subscribe(() => {
          if (!flags.isSurface()) return;

          const elementIds = context.gfx.selection.selectedIds;
          this.updateWithSurface(
            context,
            context.activated && Boolean(elementIds.length),
            elementIds
          );
        });

        disposables.add(subscription);
      })
    );

    // Handles `drag and drop`
    const dragStart = () => flags.toggle(Flag.Hiding, true);
    const dragEnd = () => flags.toggle(Flag.Hiding, false);
    const eventOptions = { passive: false };
    this.handleEvent('dragStart', () => {
      dragStart();
      document.addEventListener('pointerup', dragEnd, { once: true });
    });
    this.handleEvent('nativeDrop', dragEnd);
    disposables.addFromEvent(host, 'dragenter', dragStart, eventOptions);
    disposables.addFromEvent(
      host,
      'dragleave',
      throttle(
        (event: DragEvent) => {
          const { x, y, target } = event;
          if (target === this) return;
          const rect = host.getBoundingClientRect();
          if (
            x >= rect.left &&
            x <= rect.right &&
            y >= rect.top &&
            y <= rect.bottom
          )
            return;
          dragEnd();
        },
        144,
        { trailing: true }
      ),
      eventOptions
    );

    // Handles elements when resizing
    const edgelessSlots = std.getOptional(EdgelessLegacySlotIdentifier);
    if (edgelessSlots) {
      disposables.add(edgelessSlots.elementResizeStart.subscribe(dragStart));
      disposables.add(edgelessSlots.elementResizeEnd.subscribe(dragEnd));
    }

    // Handles elements when hovering
    disposables.add(
      message$.subscribe(data => {
        if (
          !context.activated ||
          flags.contains(Flag.Text | Flag.Native | Flag.Block)
        ) {
          flags.toggle(Flag.Hovering, false);
          return;
        }

        const activated = !!data;

        batch(() => {
          flags.toggle(Flag.Hovering, activated);

          if (!activated) return;

          const { flavour, setFloating } = data;

          setFloating(toolbar);

          this.setReferenceElementWithHtmlElement(data.element);

          sideOptions$.value = null;
          flavour$.value = flavour;
          placement$.value = toolbarRegistry.getModulePlacement(flavour);
          flags.refresh(Flag.Hovering);
        });
      })
    );

    // Updates toolbar theme when `app-theme` changing
    disposables.add(
      context.theme.app$.subscribe(theme => {
        toolbar.dataset.appTheme = theme;
      })
    );

    // Updates layout when placement changing to `inner`
    disposables.add(
      effect(() => {
        toolbar.dataset.placement = placement$.value;
      })
    );

    disposables.add(
      effect(() => {
        const value = flags.value$.value;

        // Hides toolbar
        if (Flag.None === value || flags.check(Flag.Hiding, value)) {
          if ('inline' in toolbar.dataset) delete toolbar.dataset.inline;
          if (toolbar.dataset.open) delete toolbar.dataset.open;
          // Closes dropdown menus
          toolbar
            .querySelector<EditorMenuButton>('editor-menu-button[data-open]')
            ?.hide();
          return;
        }

        const flavour = flavour$.value;

        // Shows toolbar
        // 1. `Flag.Text`: formatting in note
        // 2. `Flag.Native`: formating in database/table
        // 3. `Flag.Block`: blocks in note
        // 4. `Flag.Hovering`: inline links in note/database/table
        // 5. `Flag.Surface`: elements in edgeless
        renderToolbar(toolbar, context, flavour);
      })
    );

    let abortController = new AbortController();

    /**
     * 找到会让 position: fixed 改以它为包含块的祖先。
     *
     * 无限画布的 .widgets-container 带 contain: size layout，layout containment
     * 会成为 fixed 后代的包含块；此时 left/top 是相对画布控件层的坐标，
     * 直接写视口坐标会把工具条推到屏幕外。
     */
    const findFixedContainingBlock = (start: HTMLElement): HTMLElement | null => {
      let node: Element | null = start
      while (node) {
        const style = getComputedStyle(node)
        if (
          style.transform !== 'none' ||
          style.perspective !== 'none' ||
          style.filter !== 'none' ||
          /(layout|paint|strict|content)/.test(style.contain)
        ) {
          return node as HTMLElement
        }
        node = node.parentElement ?? (node.getRootNode() as ShadowRoot)?.host ?? null
      }
      return null
    }

    /**
     * 把正文格式工具条固定在编辑区顶部的居中位置。
     *
     * 用 fixed 而不是 absolute：工具条在 shadow DOM 里，absolute 的包含块
     * 由上游布局决定，滚动时可能跟着正文跑；fixed 才能保证「置顶固定」。
     *
     * 定位基准不能直接用视口元素或 host 的 rect：
     *   - host 是滚动内容的一部分，滚动后它的 rect.top 会变成负数；
     *   - 无限画布的视口带世界坐标变换，rect 会随画布平移/缩放跑偏。
     * 因此用「视口父容器 + offsetTop」还原顶栏下沿：offsetTop 是布局值，
     * 不受滚动与画布变换影响，两种模式都能稳定落在预留的顶栏里。
     */
    const pinPageTextToolbar = () => {
      const viewport =
        (host.closest('.affine-page-viewport') as HTMLElement | null) ??
        (host.closest('.affine-edgeless-viewport') as HTMLElement | null) ??
        host
      const frame = viewport.parentElement
      const frameRect = frame?.getBoundingClientRect()
      const viewportRect = viewport.getBoundingClientRect()
      // offsetTop 是相对定位父容器的布局偏移（工作台里就是顶栏的 52px）。
      const bandBottom = frameRect ? frameRect.top + viewport.offsetTop : viewportRect.top
      // clientWidth 不含纵向滚动条，居中时才不会往右偏半个滚动条。
      const width = viewport.clientWidth || frame?.clientWidth || viewportRect.width
      let left = (frameRect?.left ?? viewportRect.left) + width / 2
      let top = bandBottom
      // 先读高度再统一写入定位：offsetHeight 为 0 说明工具条还没渲染出来，用兜底值。
      const barHeight = toolbar.offsetHeight || PAGE_TEXT_TOOLBAR_FALLBACK_HEIGHT
      top -= barHeight + PAGE_TEXT_TOOLBAR_GAP

      // 画布控件层是 fixed 的包含块：换算成它的局部坐标，效果仍然是视口坐标。
      // 页面模式没有这类祖先（顶层单位矩阵的 rect 是 0,0），减法不影响结果。
      const containingRect = findFixedContainingBlock(this)?.getBoundingClientRect()
      if (containingRect) {
        left -= containingRect.left
        top -= containingRect.top
      }

      toolbar.style.position = 'fixed'
      toolbar.style.left = `${Math.round(left)}px`
      toolbar.style.top = `${Math.round(top)}px`
      // 窄屏 / AI 侧栏展开时工具条不能溢出编辑区，超出部分交给自身横向滚动。
      toolbar.style.maxWidth = `${Math.round(width - 24)}px`
      toolbar.style.transform = 'translateX(-50%)'
      toolbar.dataset.fixed = 'true'
      // 固定模式下没有 hide() middleware 兜底，显隐由 flag 自己负责。
      toolbar.dataset.open = 'true'
    };

    /** 离开固定模式时清掉 inline 定位，把控制权交回 autoUpdatePosition。 */
    const unpinPageTextToolbar = () => {
      if (!('fixed' in toolbar.dataset)) return;
      delete toolbar.dataset.fixed;
      toolbar.style.position = '';
      toolbar.style.transform = '';
      // left/top 一并清掉：否则回到 absolute 时会残留 fixed 的坐标，闪一下左上角。
      toolbar.style.left = '';
      toolbar.style.top = '';
      toolbar.style.maxWidth = '';
    };

    // 编辑区尺寸变化时重算居中位置：AI 侧栏展开/收起、窗口缩放都会改变宽度。
    // 用 ResizeObserver 而不是 window.resize —— 侧栏开合不改窗口尺寸。
    const toolbarResizeObserver = new ResizeObserver(() => {
      if (flags.check(Flag.Text, flags.value$.peek())) {
        pinPageTextToolbar()
      }
    });
    toolbarResizeObserver.observe(host);
    disposables.add(() => toolbarResizeObserver.disconnect());

    disposables.add(
      effect(() => {
        if (IS_MOBILE) {
          const value = flags.value$.value;
          if (!context.activated) return;
          if (Flag.None === value || flags.contains(Flag.Hiding, value)) return;
          updateMobilePosition?.();
          return;
        }

        if (!abortController.signal.aborted) {
          abortController.abort();
        }

        const value = flags.value$.value;

        // 正文文本选区：固定置顶，不再跟随选区浮动。
        // 只接管 Flag.Text；表格/数据库的 Flag.Native、
        // 整块选中的 Flag.Block 与画布元素的 Flag.Surface 仍走原定位。
        // 无限画布里的文本编辑同样固定在顶栏（定位基准已避开画布变换）。
        //
        // 固定模式刻意不判 context.activated：常驻工具栏在编辑器未聚焦
        // （例如刚打开文档、或点过侧栏）时也要停在顶部，否则会闪回左上角。
        if (flags.check(Flag.Text, value)) {
          pinPageTextToolbar()
          return
        }

        if (!context.activated) {
          unpinPageTextToolbar();
          return;
        }
        if (Flag.None === value || flags.contains(Flag.Hiding, value)) {
          unpinPageTextToolbar();
          return;
        }

        unpinPageTextToolbar();

        const build = referenceElement$.value;
        const referenceElement = build?.();
        if (!referenceElement) return;

        const flavour = flavour$.value;
        const placement = placement$.value;
        const sideOptions = sideOptions$.value;

        if (abortController.signal.aborted) {
          abortController = new AbortController();
        }
        const signal = abortController.signal;

        const cleanup = autoUpdatePosition(
          signal,
          toolbar,
          referenceElement,
          flavour,
          placement,
          sideOptions
        );

        signal.addEventListener('abort', cleanup, { once: true });

        return () => {
          if (signal.aborted) return;
          abortController.abort();
        };
      })
    );

    /**
     * 常驻工具栏的初始光标。
     *
     * 打开文档时若还没有任何选区，把光标放到正文第一个文本块的开头，
     * 让格式工具条立刻出现在顶部（否则要先点一下正文才出现）。
     * 只读、画布模式与已有选区时都不动，避免覆盖用户或上游的初始状态。
     */
    const ensureInitialCaret = () => {
      if (this.store.readonly || context.isEdgelessMode) return;
      if (std.selection.value.length > 0) return;

      let targetId = '';
      const walk = (blocks: readonly any[]) => {
        for (const block of blocks) {
          if (targetId) return;
          const flavour = block?.flavour;
          // 只挑有 text 的块：标题、段落、列表与代码块。
          if (
            flavour === 'affine:paragraph' ||
            flavour === 'affine:list' ||
            flavour === 'affine:code'
          ) {
            targetId = block.id;
            return;
          }
          if (block?.children?.length) walk(block.children);
        }
      };
      for (const note of (this.store.root?.children ?? []) as readonly any[]) {
        if (note?.flavour !== 'affine:note') continue;
        walk(note.children ?? []);
        if (targetId) break;
      }
      if (!targetId) return;

      std.selection.setGroup('note', [
        std.selection.create(TextSelection, {
          from: { blockId: targetId, index: 0, length: 0 },
          to: null,
        }),
      ]);
    };

    // 等一帧：note 与首个段落可能还没完成渲染。
    nextTick()
      .then(ensureInitialCaret)
      .catch(() => {
        // 初始光标失败不影响编辑，忽略。
      });
  }
}
