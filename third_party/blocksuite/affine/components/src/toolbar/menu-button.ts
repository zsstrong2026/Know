import {
  panelBaseStyle,
  scrollbarStyle,
} from '@blocksuite/affine-shared/styles';
import {
  type ButtonPopperOptions,
  createButtonPopper,
} from '@blocksuite/affine-shared/utils';
import { WithDisposable } from '@blocksuite/global/lit';
import {
  css,
  html,
  LitElement,
  type PropertyValues,
  type TemplateResult,
} from 'lit';
import { property, query } from 'lit/decorators.js';

import type { EditorIconButton } from './icon-button.js';

export class EditorMenuButton extends WithDisposable(LitElement) {
  static override styles = css`
    :host {
      display: flex;
      align-items: center;
      justify-content: center;
      gap: 8px;
    }
  `;

  private _popper: ReturnType<typeof createButtonPopper> | null = null;

  /** 当前已绑定点击监听的触发按钮，用于识别 lit 重建后的新节点。 */
  private _boundTrigger: EditorIconButton | null = null;

  private readonly _onTriggerClick = () => {
    this._popper?.toggle();
  };

  /**
   * 同步触发按钮。
   *
   * `button` 是一个模板属性，父级重渲染时 lit 可能替换掉内部的
   * `editor-icon-button`；而 @query 会在更新后指向新节点。若沿用旧节点的
   * 监听与 popper reference，点击就会落在「没有监听的新按钮」上。
   *
   * 另外，固定置顶工具条会在 body 与组件 shadow root 之间迁移，元素会先
   * 断开再连回来，`WithDisposable` 会在断开时清掉监听，因此这里每次都
   * 确保监听挂在当前节点上。
   */
  private _syncTrigger() {
    const trigger = this._trigger;
    if (!trigger) return;

    if (trigger !== this._boundTrigger) {
      this._boundTrigger?.removeEventListener('click', this._onTriggerClick);
      this._boundTrigger = trigger;
      // popper 的定位参照物是旧按钮，需要一并重建
      this._updatePopper();
    }

    // remove 后再 add，避免重连/重复更新造成多个监听
    trigger.removeEventListener('click', this._onTriggerClick);
    trigger.addEventListener('click', this._onTriggerClick);
  }

  override connectedCallback() {
    super.connectedCallback();

    // 首次连接时还处于首次更新之前，交由 firstUpdated 处理
    if (!this.hasUpdated) return;

    // 组件被移动（断开→重连）后监听已被清理，需重新挂载
    this._boundTrigger = null;
    this._syncTrigger();
  }

  private _updatePopper() {
    this._popper?.dispose();
    this._popper = createButtonPopper({
      reference: this._trigger,
      popperElement: this._content,
      stateUpdated: ({ display }) => {
        const opened = display === 'show';
        this._trigger.showTooltip = !opened;
        this.dispatchEvent(
          new CustomEvent('toggle', {
            detail: opened,
            bubbles: false,
            cancelable: false,
            composed: true,
          })
        );

        if (opened) {
          this.dataset.open = 'true';
        } else {
          delete this.dataset.open;
        }
      },
      mainAxis: 0,
      offsetHeight: 6 * 4,
      topLayer: true,
      ...this.popperOptions,
    });
  }

  override willUpdate(changedProperties: PropertyValues) {
    if (changedProperties.has('contentPadding')) {
      this.style.setProperty('--content-padding', this.contentPadding ?? '');
    }

    if (this.hasUpdated && changedProperties.has('popperOptions')) {
      this._updatePopper();
    }
  }

  override firstUpdated() {
    this._disposables.addFromEvent(this, 'keydown', (e: KeyboardEvent) => {
      e.stopPropagation();
      if (e.key === 'Escape') {
        this._popper?.hide();
      }
    });
    this._disposables.add(() => this._popper?.dispose());
    this._syncTrigger();
  }

  override updated() {
    // 父级重渲染可能替换掉触发按钮，需要把监听与 popper 迁到新节点
    this._syncTrigger();
  }

  hide() {
    this._popper?.hide();
  }

  override render() {
    return html`
      ${this.button}
      <editor-menu-content role="menu" tabindex="-1">
        <slot></slot>
      </editor-menu-content>
    `;
  }

  show(force = false) {
    this._popper?.show(force);
  }

  @query('editor-menu-content')
  private accessor _content!: EditorMenuContent;

  @query('editor-icon-button')
  private accessor _trigger!: EditorIconButton;

  @property({ attribute: false })
  accessor button!: TemplateResult;

  @property({ attribute: false })
  accessor contentPadding: string | undefined = undefined;

  @property({ attribute: false })
  accessor popperOptions: Partial<ButtonPopperOptions> = {};
}

export class EditorMenuContent extends LitElement {
  static override styles = css`
    :host {
      padding: 12px 0;
      display: none;
      outline: none;
    }

    :host([popover]) {
      inset: auto;
      margin: 0;
      border: 0;
      background: transparent;
      color: inherit;
    }

    :host([data-show]) {
      display: flex;
      justify-content: center;
    }

    ${panelBaseStyle('.content-wrapper')}
    ${scrollbarStyle('.content-wrapper')}
    .content-wrapper {
      overscroll-behavior: contain;
      overflow-y: auto;
      padding: var(--content-padding, 0 6px);
    }

    ::slotted(:not(.custom)) {
      display: flex;
      align-items: center;
      align-self: stretch;
      gap: 8px;
      min-height: 36px;
    }

    ::slotted([data-size]) {
      min-width: 146px;
    }

    ::slotted([data-size='small']) {
      min-width: 164px;
    }

    ::slotted([data-size='large']) {
      min-width: 176px;
    }

    ::slotted([data-orientation='vertical']) {
      flex-direction: column;
      align-items: stretch;
      gap: unset;
      min-height: fit-content;
    }
  `;

  override render() {
    return html`<div class="content-wrapper"><slot></slot></div>`;
  }
}

export class EditorMenuAction extends LitElement {
  static override styles = css`
    :host {
      display: flex;
      width: 100%;
      align-items: center;
      white-space: nowrap;
      box-sizing: border-box;
      padding: 4px 8px;
      border-radius: 4px;
      overflow: hidden;
      text-overflow: ellipsis;
      cursor: pointer;
      gap: 8px;
      color: var(--affine-text-primary-color);
      font-weight: 400;
      min-height: 30px; // 22 + 8
      user-select: none;
    }

    :host(:hover),
    :host([data-selected]) {
      background-color: var(--affine-hover-color);
    }

    :host([data-selected]) {
      pointer-events: none;
    }

    :host(:hover.delete),
    :host(:hover.delete) ::slotted(svg) {
      background-color: var(--affine-background-error-color);
      color: var(--affine-error-color);
    }

    :host([disabled]) {
      pointer-events: none;
      cursor: not-allowed;
      color: var(--affine-text-disable-color);
    }

    ::slotted(svg) {
      color: var(--affine-icon-color);
      font-size: 20px;
    }

    ::slotted(.label) {
      color: inherit !important;
    }
    ::slotted(.label.capitalize) {
      text-transform: capitalize !important;
    }
  `;

  override connectedCallback() {
    super.connectedCallback();
    this.role = 'button';
  }

  override render() {
    return html`<slot></slot>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'editor-menu-button': EditorMenuButton;
    'editor-menu-content': EditorMenuContent;
    'editor-menu-action': EditorMenuAction;
  }
}
