// 画布快照历史：只存状态快照，视图操作（缩放/平移）不入栈。
export class SnapshotHistory<T> {
  private readonly limit: number;
  private past: T[] = [];
  private future: T[] = [];

  constructor(limit = 50) {
    this.limit = Math.max(1, limit);
  }

  get canUndo(): boolean {
    return this.past.length > 1;
  }

  get canRedo(): boolean {
    return this.future.length > 0;
  }

  /** 入栈新快照：清空重做栈，超出上限时丢最旧的。 */
  push(snapshot: T): void {
    this.past.push(snapshot);
    if (this.past.length > this.limit) this.past.shift();
    this.future = [];
  }

  undo(): T | null {
    if (!this.canUndo) return null;
    const current = this.past.pop() as T;
    this.future.push(current);
    return this.past[this.past.length - 1];
  }

  redo(): T | null {
    if (!this.canRedo) return null;
    const next = this.future.pop() as T;
    this.past.push(next);
    return next;
  }

  reset(): void {
    this.past = [];
    this.future = [];
  }
}
