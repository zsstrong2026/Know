import {
  type EditorMenuButton,
  type EditorToolbar,
  renderToolbarSeparator,
} from '@blocksuite/affine-components/toolbar';
import {
  ActionPlacement,
  type ToolbarAction,
  type ToolbarActions,
  type ToolbarContext,
  type ToolbarPlacement,
} from '@blocksuite/affine-shared/services';
import { nextTick } from '@blocksuite/global/utils';
import { TextSelection } from '@blocksuite/std';
import { MoreVerticalIcon } from '@blocksuite/icons/lit';
import type {
  AutoUpdateOptions,
  ComputePositionConfig,
  ReferenceElement,
  SideObject,
} from '@floating-ui/dom';
import {
  autoUpdate,
  computePosition,
  flip,
  hide,
  inline,
  limitShift,
  offset,
  shift,
  size,
} from '@floating-ui/dom';
import { html, render } from 'lit';
import { ifDefined } from 'lit/directives/if-defined.js';
import { join } from 'lit/directives/join.js';
import { keyed } from 'lit/directives/keyed.js';
import { repeat } from 'lit/directives/repeat.js';
import groupBy from 'lodash-es/groupBy';
import mergeWith from 'lodash-es/mergeWith';
import orderBy from 'lodash-es/orderBy';
import partition from 'lodash-es/partition';
import toPairs from 'lodash-es/toPairs';

export const sideMap = new Map([
  // includes frame element
  ['affine:surface:frame', { top: 28 }],
  // includes group element
  ['affine:surface:group', { top: 20 }],
  // has only one shape element
  ['affine:surface:shape', { top: 26, bottom: -26 }],
]);

export function autoUpdatePosition(
  signal: AbortSignal,
  toolbar: EditorToolbar,
  referenceElement: ReferenceElement,
  flavour: string,
  placement: ToolbarPlacement,
  sideOptions: Partial<SideObject> | null,
  options: AutoUpdateOptions = { elementResize: false }
) {
  const isInline = flavour === 'affine:note';
  const hasSurfaceScope = flavour.includes('surface');
  const isInner = placement === 'inner';
  const offsetTop = sideOptions?.top ?? 0;
  const offsetBottom = sideOptions?.bottom ?? 0;
  const offsetY = offsetTop + (hasSurfaceScope ? 2 : 0);
  const config: Partial<ComputePositionConfig> = isInner
    ? {
        placement: 'top-start',
        middleware: [
          offset(({ rects }) => -rects.floating.height),
          size({
            apply: ({ elements }) => {
              const { width } = elements.reference.getBoundingClientRect();
              elements.floating.style.width = `${width}px`;
            },
          }),
        ],
      }
    : {
        placement,
        middleware: [
          offset(10 + offsetY),
          size({
            apply: ({ elements }) => {
              elements.floating.style.width = 'fit-content';
            },
          }),
          isInline ? inline() : undefined,
          shift(state => ({
            padding: {
              top: 10,
              right: 10,
              bottom: 150,
              left: 10,
            },
            crossAxis: state.placement.includes('bottom'),
            limiter: limitShift(),
          })),
          flip({ padding: 10 }),
          hide(),
        ],
      };
  const update = async () => {
    await Promise.race([
      new Promise(resolve => {
        signal.addEventListener('abort', () => resolve(signal.reason), {
          once: true,
        });

        if (signal.aborted) return;

        resolve(null);
      }),
      isInline ? toolbar.updateComplete.then(nextTick) : toolbar.updateComplete,
    ]);

    if (signal.aborted) return;

    const result = await computePosition(referenceElement, toolbar, config);

    const { x, middlewareData, placement: currentPlacement } = result;
    const y =
      result.y -
      (currentPlacement.includes('top') ? 0 : offsetTop + offsetBottom);

    toolbar.style.transform = `translate3d(${x}px, ${y}px, 0)`;

    if (middlewareData.hide) {
      if (toolbar.dataset.open) {
        if (middlewareData.hide.referenceHidden) {
          delete toolbar.dataset.open;
          // Closes dropdown menus
          toolbar
            .querySelector<EditorMenuButton>('editor-menu-button[data-open]')
            ?.hide();
        }
      } else {
        toolbar.dataset.open = 'true';
      }
    }
  };

  return autoUpdate(
    referenceElement,
    toolbar,
    () => {
      update().catch(console.error);
    },
    { animationFrame: hasSurfaceScope, ...options }
  );
}

export function combine(actions: ToolbarActions, context: ToolbarContext) {
  const grouped = group(actions);

  const generated = grouped.map(action => {
    const newAction = {
      ...action,
      placement: action.placement ?? ActionPlacement.Normal,
    };

    if ('generate' in action && typeof action.generate === 'function') {
      // TODO(@fundon): should delete `generate` fn
      return {
        ...newAction,
        ...action.generate(context),
      };
    }

    return newAction;
  });

  const filtered = generated.filter(action => {
    if (typeof action.when === 'function') return action.when(context);
    return action.when ?? true;
  });

  return filtered;
}

function group(actions: ToolbarAction[]): ToolbarAction[] {
  const grouped = groupBy(actions, a => a.id);

  const paired = toPairs(grouped).map(([_, items]) => {
    if (items.length === 1) return items[0];
    const [first, ...others] = items;
    if (others.length === 1) return merge({ ...first }, others[0]);
    return others.reduce(merge, { ...first });
  });

  return paired;
}

const merge = (a: any, b: any) =>
  mergeWith(a, b, (obj, src) =>
    Array.isArray(obj) ? group(obj.concat(src)) : src
  );

/**
 * 固定置顶工具条的上一次渲染签名。
 *
 * 上游在每次选区变化（包括每敲一个字）都会重新 render 整条工具条；
 * 工具条里全是 `editor-icon-button` / `editor-menu-button` 这类自定义元素，
 * 重建后要等各自一轮异步更新才画出图标，输入时就会看到按钮一闪一闪。
 * 固定模式下内容没有实质变化时直接跳过重建，只同步显隐相关属性。
 */
const fixedToolbarSignature = new WeakMap<EditorToolbar, string>();

/**
 * 计算固定工具条的渲染签名。
 *
 * 只收录会改变视觉结果的字段：动作集合、激活/禁用态、当前文本块的类型与对齐。
 * 刻意不含光标位置与文本长度，保证同一段落内连续输入不会触发重绘。
 */
function fixedToolbarSignatureOf(
  context: ToolbarContext,
  flavour: string,
  actions: ToolbarActions
): string {
  const signatureOf = (list: ToolbarActions): string =>
    list
      .map(action => {
        const active =
          typeof action.active === 'function'
            ? action.active(context)
            : action.active;
        const disabled =
          typeof action.disabled === 'function'
            ? action.disabled(context)
            : action.disabled;
        const nested =
          'actions' in action && action.actions?.length
            ? `(${signatureOf(combine(action.actions, context))})`
            : '';
        return `${action.id}:${active ? 1 : 0}:${disabled ? 1 : 0}${nested}`;
      })
      .join(',');

  const selection = context.std.selection.find(TextSelection);
  const model = selection
    ? context.store.getBlock(selection.blockId)?.model
    : (context.getCurrentModel() as any);
  const props = (model as any)?.props ?? {};
  const modelKey = model
    ? [
        model.flavour,
        model.id,
        props.type ?? '',
        props.textAlign ?? '',
        props.checked ? 1 : 0,
      ].join(':')
    : 'none';
  const range = context.std.range.value;

  return [
    flavour,
    range?.collapsed ? 'collapsed' : 'range',
    modelKey,
    signatureOf(actions),
  ].join('|');
}

/**
 * Renders toolbar
 *
 * Merges the following configs:
 * 1. `affine:note`
 * 2. `custom:affine:note`
 * 3. `affine:*`
 * 4. `custom:affine:*`
 */
export function renderToolbar(
  toolbar: EditorToolbar,
  context: ToolbarContext,
  flavour: string
) {
  const hasSurfaceScope = flavour.includes('surface');
  const toolbarRegistry = context.toolbarRegistry;

  const actions = [
    flavour,
    `custom:${flavour}`,
    hasSurfaceScope ? ['affine:surface:*', 'custom:affine:surface:*'] : [],
    'affine:*',
    'custom:affine:*',
  ]
    .flat()
    .map(key => toolbarRegistry.modules.get(key))
    .filter(module => !!module)
    .filter(module =>
      typeof module.config.when === 'function'
        ? module.config.when(context)
        : (module.config.when ?? true)
    )
    .flatMap(module => module.config.actions);

  const combined = combine(actions, context);

  const ordered = orderBy(
    combined,
    ['placement', 'id', 'score'],
    ['asc', 'asc', 'asc']
  );

  const [moreActionGroup, primaryActionGroup] = partition(
    ordered,
    a => a.placement === ActionPlacement.More
  );

  // Resets
  if (primaryActionGroup.length === 0) {
    fixedToolbarSignature.delete(toolbar);
    context.reset();
    return;
  }

  const innerToolbar = context.placement$.value === 'inner';

  if (moreActionGroup.length) {
    const moreMenuItems = renderActions(
      moreActionGroup,
      context,
      renderMenuActionItem
    );
    if (moreMenuItems.length) {
      const key = `${context.getCurrentModel()?.id}`;

      primaryActionGroup.push({
        id: 'more',
        content: html`${keyed(
          `${flavour}:${key}`,
          html`
            <editor-menu-button
              aria-label="More menu"
              .contentPadding="${'8px'}"
              .button=${html`
                <editor-icon-button
                  aria-label="More"
                  .tooltip="${'More'}"
                  .iconContainerPadding=${innerToolbar ? 4 : 2}
                  .iconSize=${innerToolbar ? '16px' : undefined}
                >
                  ${MoreVerticalIcon()}
                </editor-icon-button>
              `}
            >
              <div
                data-size=${innerToolbar ? '' : 'large'}
                data-orientation="vertical"
              >
                ${join(moreMenuItems, renderToolbarSeparator('horizontal'))}
              </div>
            </editor-menu-button>
          `
        )}`,
      });
    }
  }

  // 固定置顶模式下内容没变就不重建 DOM（输入时逐字重建会让按钮闪动）。
  const fixed = toolbar.dataset.fixed === 'true';
  const signature = fixed
    ? fixedToolbarSignatureOf(context, flavour, ordered)
    : '';

  if (!fixed || signature !== fixedToolbarSignature.get(toolbar)) {
    if (fixed) fixedToolbarSignature.set(toolbar, signature);
    render(
      join(
        renderActions(primaryActionGroup, context),
        innerToolbar ? null : renderToolbarSeparator()
      ),
      toolbar
    );
  }

  // Avoids shaking
  if (flavour === 'affine:note' && context.std.range.value) {
    if (!('inline' in toolbar.dataset)) {
      toolbar.dataset.inline = '';
    } else {
      toolbar.dataset.inline = 'true';
    }
  } else {
    delete toolbar.dataset.inline;
  }

  if (toolbar.dataset.open) return;
  toolbar.dataset.open = 'true';
}

function renderActions(
  actions: ToolbarActions,
  context: ToolbarContext,
  render = renderActionItem
) {
  return actions
    .map(action => {
      if ('content' in action) {
        if (typeof action.content === 'function') {
          return action.content(context);
        } else {
          return action.content ?? null;
        }
      }

      if ('actions' in action && action.actions.length) {
        const combined = combine(action.actions, context);

        if (!combined.length) return null;

        const ordered = orderBy(combined, ['id', 'score'], ['asc', 'asc']);

        return repeat(
          ordered,
          a => a.id,
          a => {
            if ('content' in a) {
              if (typeof a.content === 'function') {
                return a.content(context);
              } else {
                return a.content ?? null;
              }
            }
            return render(a, context);
          }
        );
      }

      if ('run' in action && action.run) {
        return render(action, context);
      }

      return null;
    })
    .filter(action => action !== null);
}

// TODO(@fundon): supports templates
function renderActionItem(action: ToolbarAction, context: ToolbarContext) {
  const innerToolbar = context.placement$.value === 'inner';
  const ids = action.id.split('.');
  const id = ids[ids.length - 1];
  const label = action.label ?? action.tooltip ?? id;
  const actived =
    typeof action.active === 'function'
      ? action.active(context)
      : action.active;
  const disabled =
    typeof action.disabled === 'function'
      ? action.disabled(context)
      : action.disabled;

  return html`
    <editor-icon-button
      data-testid=${ifDefined(id)}
      aria-label=${ifDefined(label)}
      ?active=${actived}
      ?disabled=${disabled}
      .tooltip=${action.tooltip}
      .iconContainerPadding=${innerToolbar ? 4 : 2}
      .iconSize=${innerToolbar ? '16px' : undefined}
      @click=${() => action.run?.(context)}
    >
      ${action.icon}
      ${
        action.showLabel && action.label
          ? html`<span class="label">${action.label}</span>`
          : null
      }
    </editor-icon-button>
  `;
}

function renderMenuActionItem(action: ToolbarAction, context: ToolbarContext) {
  const innerToolbar = context.placement$.value === 'inner';
  const ids = action.id.split('.');
  const id = ids[ids.length - 1];
  const label = action.label ?? action.tooltip ?? id;
  const actived =
    typeof action.active === 'function'
      ? action.active(context)
      : action.active;
  const disabled =
    typeof action.disabled === 'function'
      ? action.disabled(context)
      : action.disabled;
  const destructive = action.variant === 'destructive' ? 'delete' : undefined;

  return html`
    <editor-menu-action
      data-testid=${ifDefined(id)}
      aria-label=${ifDefined(label)}
      class="${ifDefined(destructive)}"
      ?active=${actived}
      ?disabled=${disabled}
      .tooltip=${ifDefined(action.tooltip)}
      .iconContainerPadding=${innerToolbar ? 4 : 2}
      .iconSize=${innerToolbar ? '16px' : undefined}
      @click=${() => action.run?.(context)}
    >
      ${action.icon}
      ${action.label ? html`<span class="label">${action.label}</span>` : null}
    </editor-menu-action>
  `;
}
