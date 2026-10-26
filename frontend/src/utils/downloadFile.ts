// 触发浏览器保存二进制响应：转 object URL 后模拟点击隐藏链接。
// 延迟释放 URL，避免部分浏览器在保存开始前就拿到空文件。
export function downloadBlob(blob: Blob, fileName: string): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = fileName || 'download';
  link.style.display = 'none';
  document.body.appendChild(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
