/**
 * 编辑器 UI 文案的翻译桥。
 *
 * 上游把工具条 tooltip、下拉菜单项名称等文案硬编码成英文；
 * 宿主（Vue 前端）有自己的 i18n，这里提供一个运行期注入点：
 * 前端挂载编辑器时调用 `setEditorUiTranslator` 传入翻译函数，
 * 工具条渲染时统一走 `editorT()`，让编辑器文案跟随界面语言。
 */
let translator: (text: string) => string = text => text;

let revision = 0;

/** 注入/替换翻译函数；revision 自增，供依赖文案的渲染缓存判断失效。 */
export function setEditorUiTranslator(fn: (text: string) => string): void {
  translator = fn;
  revision += 1;
}

/** 当前翻译版本号；语言切换后会变化。 */
export function getEditorUiRevision(): number {
  return revision;
}

/** 翻译一段 UI 文案；没有对应词条时原样返回（英文本身即正确）。 */
export function editorT(text: string): string {
  return translator(text);
}
