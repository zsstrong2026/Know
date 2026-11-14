import {
  menu,
  popFilterableSimpleMenu,
  popupTargetFromElement,
} from '@blocksuite/affine-components/context-menu';
import { SignalWatcher, WithDisposable } from '@blocksuite/global/lit';
import { AddCursorIcon } from '@blocksuite/icons/lit';
import { ShadowlessElement } from '@blocksuite/std';
import { css, nothing } from 'lit';
import { property } from 'lit/decorators.js';
import { repeat } from 'lit/directives/repeat.js';
import { html } from 'lit/static-html.js';

import { GroupTitle } from '../../../core/group-by/group-title.js';
import type { Group } from '../../../core/group-by/trait.js';
import { dragHandler } from '../../../core/utils/wc-dnd/dnd-context.js';
import type { KanbanViewUILogic } from './kanban-view-ui-logic.js';

/** 每个状态列默认最多完整显示的卡片数；超出后列内滚动。 */
const MAX_VISIBLE_CARDS = 10;

const styles = css`
  affine-data-view-kanban-group {
    width: 260px;
    flex-shrink: 0;
    border-radius: 8px;
    display: flex;
    flex-direction: column;
  }

  .group-header {
    height: 32px;
    padding: 6px 4px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    overflow: hidden;
  }

  .group-header-title {
    overflow: hidden;
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: var(--data-view-cell-text-size);
  }

  affine-data-view-kanban-group:hover .group-header-op {
    visibility: visible;
    opacity: 1;
  }

  .group-body {
    margin-top: 4px;
    display: flex;
    flex-direction: column;
    padding: 0 4px;
    gap: 12px;
    min-height: 0;
  }

  .group-cards {
    display: flex;
    flex-direction: column;
    gap: 12px;
    min-height: 0;
  }

  /* 超出 10 张时列内滚动。滚动条平时完全透明，鼠标悬停到该列才淡显，
     避免在浅色列底上抢视觉；窄条 + 圆角，尽量贴近原型的「无滚动条」观感。 */
  .group-cards.is-scrollable {
    overflow-y: auto;
    overscroll-behavior: contain;
    scrollbar-width: thin;
    scrollbar-color: transparent transparent;
  }

  .group-cards.is-scrollable:hover {
    scrollbar-color: rgba(128, 128, 128, 0.4) transparent;
  }

  /* 兼容不支持标准 scrollbar-color 的浏览器。 */
  .group-cards.is-scrollable::-webkit-scrollbar {
    width: 4px;
  }

  .group-cards.is-scrollable::-webkit-scrollbar-track {
    background: transparent;
  }

  .group-cards.is-scrollable::-webkit-scrollbar-thumb {
    background: transparent;
    border-radius: 2px;
  }

  .group-cards.is-scrollable:hover::-webkit-scrollbar-thumb {
    background: rgba(128, 128, 128, 0.32);
  }

  .group-cards.is-scrollable::-webkit-scrollbar-thumb:hover {
    background: rgba(128, 128, 128, 0.55);
  }

  .add-card {
    display: flex;
    align-items: center;
    padding: 4px;
    border-radius: 4px;
    cursor: pointer;
    font-size: var(--data-view-cell-text-size);
    line-height: var(--data-view-cell-text-line-height);
    visibility: hidden;
    opacity: 0;
    transition: all 150ms cubic-bezier(0.42, 0, 1, 1);
    color: var(--affine-text-secondary-color);
  }

  affine-data-view-kanban-group:hover .add-card {
    visibility: visible;
    opacity: 1;
  }

  affine-data-view-kanban-group .add-card:hover {
    background-color: var(--affine-hover-color);
    color: var(--affine-text-primary-color);
  }

  .sortable-ghost {
    background-color: var(--affine-hover-color);
    opacity: 0.5;
  }

  .sortable-drag {
    background-color: var(--affine-background-primary-color);
  }
`;

export class KanbanGroup extends SignalWatcher(
  WithDisposable(ShadowlessElement)
) {
  static override styles = styles;

  /** 卡片高度变化（标题换行等）时重新计算列内可视高度。 */
  private cardResizeObserver: ResizeObserver | null = null;
  private observedCards: HTMLElement[] = [];

  private readonly clickAddCard = () => {
    const id = this.view.addCard('end', this.group.key);
    requestAnimationFrame(() => {
      const columnId =
        this.view.mainProperties$.value.titleColumn ||
        this.view.propertyIds$.value[0];
      if (!columnId) return;
      this.kanbanViewLogic.selectionController.selection = {
        selectionType: 'cell',
        groupKey: this.group.key,
        cardId: id,
        columnId,
        isEditing: true,
      };
    });
    this.requestUpdate();
  };

  private readonly clickAddCardInStart = () => {
    const id = this.view.addCard('start', this.group.key);
    requestAnimationFrame(() => {
      const columnId =
        this.view.mainProperties$.value.titleColumn ||
        this.view.propertyIds$.value[0];
      if (!columnId) return;
      this.kanbanViewLogic.selectionController.selection = {
        selectionType: 'cell',
        groupKey: this.group.key,
        cardId: id,
        columnId,
        isEditing: true,
      };
    });
    this.requestUpdate();
  };

  private readonly clickGroupOptions = (e: MouseEvent) => {
    const ele = e.currentTarget as HTMLElement;
    popFilterableSimpleMenu(popupTargetFromElement(ele), [
      menu.action({
        name: 'Ungroup',
        hide: () => this.group.value == null,
        select: () => {
          this.group.rows.forEach(row => {
            this.group.manager.removeFromGroup(row.rowId, this.group.key);
          });
          this.requestUpdate();
        },
      }),
      menu.action({
        name: 'Delete Cards',
        select: () => {
          this.view.rowsDelete(this.group.rows.map(row => row.rowId));
          this.requestUpdate();
        },
      }),
    ]);
  };

  override updated() {
    this.applyCardListLimit();
    this.observeCards();
  }

  override disconnectedCallback() {
    super.disconnectedCallback();
    this.cardResizeObserver?.disconnect();
    this.cardResizeObserver = null;
    this.observedCards = [];
  }

  /**
   * 每列默认只完整显示 MAX_VISIBLE_CARDS 张卡片，其余在列内滚动。
   *
   * 卡片高度随标题行数与可见属性变化，无法用固定像素；这里取第 10 张卡片
   * 的下沿作为可视高度。用卡片的相对差值计算，列内已滚动时也不会算错。
   */
  private applyCardListLimit(): void {
    const list = this.querySelector('.group-cards') as HTMLElement | null;
    if (!list) return;
    const cards = Array.from(
      list.querySelectorAll(':scope > affine-data-view-kanban-card')
    ) as HTMLElement[];
    if (cards.length <= MAX_VISIBLE_CARDS) {
      list.classList.remove('is-scrollable');
      list.style.maxHeight = '';
      return;
    }
    const first = cards[0].getBoundingClientRect();
    const last = cards[MAX_VISIBLE_CARDS - 1].getBoundingClientRect();
    const height = Math.round(last.bottom - first.top);
    if (height <= 0) return;
    list.classList.add('is-scrollable');
    list.style.maxHeight = `${height}px`;
  }

  /** 监听卡片尺寸：标题换行会改变第 10 张卡片的下沿。 */
  private observeCards(): void {
    const list = this.querySelector('.group-cards') as HTMLElement | null;
    if (!list) return;
    if (!this.cardResizeObserver) {
      this.cardResizeObserver = new ResizeObserver(() => {
        this.applyCardListLimit();
      });
    }
    const cards = Array.from(list.children).filter(
      (el): el is HTMLElement => el instanceof HTMLElement
    );
    for (const card of this.observedCards) {
      if (!cards.includes(card)) this.cardResizeObserver.unobserve(card);
    }
    for (const card of cards) {
      if (!this.observedCards.includes(card)) {
        this.cardResizeObserver.observe(card);
      }
    }
    this.observedCards = cards;
  }

  override render() {
    const cards = this.group.rows;
    return html`
      <div class="group-header" ${dragHandler(this.group.key)}>
        ${GroupTitle(this.group, {
          readonly: this.view.readonly$.value,
          clickAdd: this.clickAddCardInStart,
          clickOps: this.clickGroupOptions,
        })}
      </div>
      <div class="group-body">
        <div class="group-cards">
          ${repeat(
            cards,
            row => row.rowId,
            row => {
              return html`
                <affine-data-view-kanban-card
                  data-card-id="${row.rowId}"
                  .groupKey="${this.group.key}"
                  .kanbanViewLogic="${this.kanbanViewLogic}"
                  .cardId="${row.rowId}"
                ></affine-data-view-kanban-card>
              `;
            }
          )}
        </div>
        ${
          this.view.readonly$.value
            ? nothing
            : html`<div class="add-card" @click="${this.clickAddCard}">
                <div
                  style="margin-right: 4px;width: 16px;height: 16px;display:flex;align-items:center;"
                >
                  ${AddCursorIcon()}
                </div>
                Add
              </div>`
        }
      </div>
    `;
  }

  @property({ attribute: false })
  accessor group!: Group;

  @property({ attribute: false })
  accessor kanbanViewLogic!: KanbanViewUILogic;

  get view() {
    return this.kanbanViewLogic.view;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'affine-data-view-kanban-group': KanbanGroup;
  }
}
