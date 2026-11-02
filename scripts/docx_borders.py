#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Word 文档表格边框安全工具（python-docx 生成链路专用，零第三方依赖）

根因：部分脚本把 PPT 的 DrawingML 边框写法（a:lnL / a:lnR / a:lnT / a:lnB）
误写进了 Word 的 <w:tcPr>，Word 打开即报「文件已损坏」。
Word 只认 <w:tcBorders><w:top/><w:left/><w:bottom/><w:right/></w:tcBorders>。

两种用法（推荐同时使用）：
  1) 生成时直接写合法边框：
        from docx_borders import set_table_borders
        set_table_borders(table, color="DDE4EE")
  2) 保存后兜底清洗（可处理历史遗留/第三方生成的坏文件）：
        python docx_borders.py a.docx [b.docx ...]   # 输出 *_修复版.docx
        python docx_borders.py a.docx -i             # 就地覆盖

注意：清洗走字节级正则，不解析/重排 XML，因此不会破坏 document.xml 的
命名空间声明与 mc:Ignorable 前缀映射（这是 lxml/ElementTree 重写 XML 时
最容易踩的坑）。
"""

from __future__ import annotations

import argparse
import re
import sys
import zipfile
from pathlib import Path

W = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
A = "http://schemas.openxmlformats.org/drawingml/2006/main"

# 需要清洗的部件：正文、页眉页脚、脚注尾注
PART_RE = re.compile(r"^word/(document|header\d*|footer\d*|footnotes|endnotes)\.xml$")

# 单元格属性块
_TCPR_RE = re.compile(rb"<w:tcPr\b[^>]*>(?P<body>.*?)</w:tcPr>", re.S)
# DrawingML 单边线条：自闭合 或 成对闭合
_LN_RE = re.compile(
    rb"<a:ln(?P<side>[LRBT])\b(?P<attrs>[^>]*?)(?:/>|>(?P<body>.*?)</a:ln(?P=side)>)",
    re.S,
)
_ATTR_W = re.compile(rb'\bw="(\d+)"')
_ATTR_COLOR = re.compile(rb'<a:srgbClr\b[^>]*\bval="([0-9A-Fa-f]{6})"')
_HAS_BORDERS = re.compile(rb"<w:tcBorders\b")


def _extract_style(ln_match: re.Match) -> tuple[int, str]:
    """从 DrawingML 线条还原线宽与颜色。

    DrawingML 的 w 属性单位是 EMU（9525 EMU = 0.75pt = 1px@96dpi），
    而 w:sz 单位是 1/8 磅，故 sz = EMU / 12700 * 8，钳制在 2~96。
    """
    sz = 6
    width = _ATTR_W.search(ln_match.group("attrs") or b"")
    if width:
        sz = max(2, min(96, round(int(width.group(1)) / 12700.0 * 8)))
    color = "DDE4EE"
    srgb = _ATTR_COLOR.search(ln_match.group("body") or b"")
    if srgb:
        color = srgb.group(1).decode("ascii").upper()
    return sz, color


def _tc_borders(sz: int, color: str) -> bytes:
    """构造合法的 w:tcBorders 片段。"""
    one = (
        '<w:{side} w:val="single" w:sz="{sz}" w:space="0" w:color="{color}"/>'
    )
    inner = "".join(
        one.format(side=side, sz=sz, color=color) for side in ("top", "left", "bottom", "right")
    )
    return f"<w:tcBorders>{inner}</w:tcBorders>".encode("ascii")


def _clean_tcpr(match: re.Match) -> tuple[bytes, int]:
    """清洗单个 w:tcPr：删掉 a:lnX，补上 w:tcBorders。"""
    body = match.group("body")
    lines = list(_LN_RE.finditer(body))
    if not lines:
        return match.group(0), 0

    sz, color = _extract_style(lines[0])
    cleaned = _LN_RE.sub(b"", body)
    # 已有合法边框时只删非法节点，不重复插入
    if not _HAS_BORDERS.search(cleaned):
        borders = _tc_borders(sz, color)
        shd = cleaned.find(b"<w:shd")
        # CT_TcPr 要求 tcBorders 排在 shd 之前
        cleaned = cleaned[:shd] + borders + cleaned[shd:] if shd >= 0 else cleaned + borders
    return match.group(0).replace(body, cleaned, 1), 1


def _sanitize_xml(data: bytes) -> tuple[bytes, int]:
    """清洗单个 XML 部件，返回(新字节, 修复的单元格数)。"""
    total = 0

    def repl(match: re.Match) -> bytes:
        nonlocal total
        new, n = _clean_tcpr(match)
        total += n
        return new

    return _TCPR_RE.sub(repl, data), total


def sanitize_docx(src, dst=None, inplace: bool = False) -> tuple[int, Path]:
    """清洗一个 docx：修复非法边框 + 规范化 ZIP 条目名（反斜杠→正斜杠）。"""
    src = Path(src)
    if not src.is_file():
        raise FileNotFoundError(src)
    dst = src if inplace else Path(dst or src.with_name(f"{src.stem}_修复版{src.suffix}"))
    dst.parent.mkdir(parents=True, exist_ok=True)
    tmp = dst.with_name(dst.name + ".tmpdocx")

    total = 0
    with zipfile.ZipFile(src) as zin, zipfile.ZipFile(tmp, "w", zipfile.ZIP_DEFLATED) as zout:
        for item in zin.infolist():
            name = item.filename.replace("\\", "/")  # OPC 规范只允许正斜杠
            data = zin.read(item.filename)
            if PART_RE.match(name):
                data, n = _sanitize_xml(data)
                total += n
            zout.writestr(name, data, compress_type=item.compress_type)
    tmp.replace(dst)
    return total, dst


# ---------------------------------------------------------------------------
# 生成侧 API：python-docx 写入时直接产出合法边框，从源头断根
# ---------------------------------------------------------------------------

def set_table_borders(table, color: str = "DDE4EE", size: int = 6) -> None:
    """为 python-docx 的 Table 设置合法边框（替代任何 a:lnX 写法）。

    size 单位 1/8 磅，6 = 0.75pt；同时覆盖外框与内部横竖线。
    """
    from docx.oxml import OxmlElement
    from docx.oxml.ns import qn

    tbl_pr = table._tbl.tblPr
    for old in tbl_pr.findall(qn("w:tblBorders")):
        tbl_pr.remove(old)
    borders = OxmlElement("w:tblBorders")
    for side in ("top", "left", "bottom", "right", "insideH", "insideV"):
        el = OxmlElement(f"w:{side}")
        el.set(qn("w:val"), "single")
        el.set(qn("w:sz"), str(size))
        el.set(qn("w:space"), "0")
        el.set(qn("w:color"), color)
        borders.append(el)
    tbl_pr.append(borders)


def clean_table_borders(table) -> int:
    """删除表格里误写的 DrawingML 边框节点，返回删除数量（生成时兜底）。"""
    from docx.oxml.ns import qn

    removed = 0
    for tc_pr in table._tbl.iter(qn("w:tcPr")):
        for tag in ("a:lnL", "a:lnR", "a:lnT", "a:lnB"):
            for el in tc_pr.findall(qn(tag)):
                tc_pr.remove(el)
                removed += 1
    return removed


def install_guard() -> None:
    """一行接入：给 python-docx 的 Document.save 挂上「保存即清洗」钩子。

    调用后，生成脚本无需改动任何内部逻辑，写出的 docx 自动不含非法边框：
        import docx_borders
        docx_borders.install_guard()
    """
    from docx.document import Document as _Doc

    if getattr(_Doc.save, "_docx_borders_guard", False):
        return

    original = _Doc.save

    def save(self, path_or_stream):
        original(self, path_or_stream)
        # 仅处理真实文件路径；写入 IO 流时交由调用方兜底清洗
        if isinstance(path_or_stream, (str, bytes, Path)):
            try:
                sanitize_docx(path_or_stream, inplace=True)
            except Exception as exc:  # noqa: BLE001
                print(f"[docx_borders] 保存后清洗失败：{exc}", file=sys.stderr)

    save._docx_borders_guard = True  # 防止重复包装
    _Doc.save = save


def _main(argv=None) -> int:
    parser = argparse.ArgumentParser(description="修复 docx 中非法的 DrawingML 表格边框")
    parser.add_argument("files", nargs="+", help="待修复的 .docx 文件或目录")
    parser.add_argument("-i", "--inplace", action="store_true", help="就地覆盖原文件")
    parser.add_argument("-o", "--out-dir", help="输出目录")
    args = parser.parse_args(argv)

    rc = 0
    for raw in args.files:
        src = Path(raw)
        targets = sorted(src.glob("*.docx")) if src.is_dir() else [src]
        for path in targets:
            dst = Path(args.out_dir) / path.name if args.out_dir else None
            try:
                count, out = sanitize_docx(path, dst, args.inplace)
            except Exception as exc:  # noqa: BLE001
                print(f"[失败] {path.name}: {exc}")
                rc = 1
                continue
            print(f"[完成] {path.name}: 修复边框 {count} 处 -> {out}")
    return rc


if __name__ == "__main__":
    raise SystemExit(_main())
