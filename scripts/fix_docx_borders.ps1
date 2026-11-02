<#
  修复 docx 中的非法 DrawingML 单元格边框，并规范化 ZIP 条目路径。

  背景：某些生成脚本（如 python-docx 手写 XML）会把 PPT 的边框写法
        <a:lnL>/<a:lnR>/<a:lnT>/<a:lnB> 塞进 Word 的 <w:tcPr>，
        Word 打开即报"文件已损坏"。本脚本将其转换为合法的
        <w:tcBorders>，并把 ZIP 条目名统一为正斜杠（OPC 规范）。

  用法：
    .\fix_docx_borders.ps1 -Path 'D:\x\a.docx'
    .\fix_docx_borders.ps1 -Path 'D:\x\*.docx' -OutDir 'D:\out'
    .\fix_docx_borders.ps1 -Path 'D:\x\a.docx' -InPlace
#>
param(
  [Parameter(Mandatory = $true)][string[]]$Path,
  [string]$OutDir,
  [switch]$InPlace
)

Add-Type -AssemblyName System.IO.Compression.FileSystem
Add-Type -AssemblyName System.IO.Compression

$W = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'
$A = 'http://schemas.openxmlformats.org/drawingml/2006/main'
$workRoot = Join-Path $PSScriptRoot '.work'

# 收集目标文件：支持文件、通配符、目录
function Resolve-Targets([string[]]$patterns) {
  $list = New-Object System.Collections.ArrayList
  foreach ($p in $patterns) {
    if (Test-Path -LiteralPath $p -PathType Container) {
      foreach ($f in @(Get-ChildItem -LiteralPath $p -Filter '*.docx' -File)) { [void]$list.Add($f.FullName) }
    }
    else {
      foreach ($f in @(Get-Item -Path $p -ErrorAction SilentlyContinue)) {
        if (-not $f.PSIsContainer -and $f.Extension -eq '.docx') { [void]$list.Add($f.FullName) }
      }
    }
  }
  return @($list)
}

# 修复单个 XML 部件中的非法边框，返回修复数量
function Repair-Part([string]$partPath, [string]$wNs, [string]$aNs) {
  $xml = New-Object System.Xml.XmlDocument
  $xml.Load($partPath)
  $ns = New-Object System.Xml.XmlNamespaceManager($xml.NameTable)
  $ns.AddNamespace('w', $wNs); $ns.AddNamespace('a', $aNs)

  $fixed = 0
  foreach ($tcPr in $xml.SelectNodes('//w:tcPr', $ns)) {
    $lns = @($tcPr.SelectNodes('./a:lnL | ./a:lnR | ./a:lnT | ./a:lnB', $ns))
    if ($lns.Count -eq 0) { continue }

    # DrawingML 线宽单位是 EMU，w:sz 单位是 1/8 磅：sz = EMU / 12700 * 8
    $sz = 6; $color = 'DDE4EE'
    $emu = $lns[0].GetAttribute('w')
    if ($emu) {
      $sz = [int][Math]::Round(([double]$emu / 12700.0) * 8)
      if ($sz -lt 2) { $sz = 2 } elseif ($sz -gt 96) { $sz = 96 }
    }
    $srgb = $lns[0].GetElementsByTagName('srgbClr', $aNs)
    if ($srgb.Count -gt 0) { $color = $srgb[0].GetAttribute('val') }

    foreach ($ln in $lns) { [void]$tcPr.RemoveChild($ln) }

    # 生成合法的 w:tcBorders（用 DocumentFragment 保证 w: 前缀）
    $frag = $xml.CreateDocumentFragment()
    $frag.InnerXml = '<w:tcBorders xmlns:w="' + $wNs + '">' +
    '<w:top w:val="single" w:sz="' + $sz + '" w:space="0" w:color="' + $color + '"/>' +
    '<w:left w:val="single" w:sz="' + $sz + '" w:space="0" w:color="' + $color + '"/>' +
    '<w:bottom w:val="single" w:sz="' + $sz + '" w:space="0" w:color="' + $color + '"/>' +
    '<w:right w:val="single" w:sz="' + $sz + '" w:space="0" w:color="' + $color + '"/>' +
    '</w:tcBorders>'
    $borders = $frag.ChildNodes[0]

    # w:tcPr 子元素有顺序要求：tcBorders 需在 tcW/gridSpan/vMerge 之后、shd 之前
    $anchor = $tcPr.SelectSingleNode('./w:tcW', $ns)
    if (-not $anchor) { $anchor = $tcPr.SelectSingleNode('./w:gridSpan', $ns) }
    if (-not $anchor) { $anchor = $tcPr.SelectSingleNode('./w:vMerge', $ns) }
    if ($anchor) { [void]$tcPr.InsertAfter($borders, $anchor) }
    elseif ($tcPr.FirstChild) { [void]$tcPr.InsertBefore($borders, $tcPr.FirstChild) }
    else { [void]$tcPr.AppendChild($borders) }

    $fixed++
  }

  if ($fixed -gt 0) { $xml.Save($partPath) }
  return $fixed
}

# 重新打包，同时把条目名中的反斜杠替换为正斜杠
function Pack-Normalized([string]$srcDir, [string]$dstFile) {
  $tmp = $dstFile + '.tmp'
  if (Test-Path -LiteralPath $tmp) { Remove-Item -LiteralPath $tmp -Force }
  $stream = [System.IO.File]::Open($tmp, [System.IO.FileMode]::CreateNew)
  $zip = New-Object System.IO.Compression.ZipArchive($stream, [System.IO.Compression.ZipArchiveMode]::Create)
  $root = (Resolve-Path -LiteralPath $srcDir).Path.TrimEnd('\')
  foreach ($file in @(Get-ChildItem -LiteralPath $root -Recurse -File)) {
    $rel = $file.FullName.Substring($root.Length + 1).Replace('\', '/')
    $entry = $zip.CreateEntry($rel, [System.IO.Compression.CompressionLevel]::Optimal)
    $out = $entry.Open()
    $fs = [System.IO.File]::OpenRead($file.FullName)
    $fs.CopyTo($out); $fs.Dispose(); $out.Dispose()
  }
  $zip.Dispose(); $stream.Dispose()
  Move-Item -LiteralPath $tmp -Destination $dstFile -Force
}

# ---------------- 主流程 ----------------
$targets = Resolve-Targets $Path
if ($targets.Count -eq 0) { Write-Output '未找到 .docx 文件'; exit 1 }

if (-not (Test-Path -LiteralPath $workRoot)) { New-Item -ItemType Directory -Path $workRoot -Force | Out-Null }

foreach ($file in $targets) {
  $base = [System.IO.Path]::GetFileNameWithoutExtension($file)
  $dir = if ($OutDir) { $OutDir } else { if ($InPlace) { Split-Path $file -Parent } else { Split-Path $file -Parent } }
  if (-not (Test-Path -LiteralPath $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }

  $out = if ($InPlace) { $file } else { Join-Path $dir ($base + '_修复版.docx') }
  $tmpOut = Join-Path $workRoot ($base + '.docx')
  $extract = Join-Path $workRoot ('x_' + $base)
  if (Test-Path -LiteralPath $extract) { Remove-Item -LiteralPath $extract -Recurse -Force }
  [System.IO.Compression.ZipFile]::ExtractToDirectory($file, $extract)

  # 正文、页眉、页脚都可能含表格
  $total = 0
  foreach ($part in @(Get-ChildItem -LiteralPath (Join-Path $extract 'word') -Filter '*.xml' -File)) {
    if ($part.Name -match '^(document|header\d*|footer\d*|footnotes|endnotes)\.xml$') {
      $total += Repair-Part $part.FullName $W $A
    }
  }

  Pack-Normalized $extract $tmpOut
  Copy-Item -LiteralPath $tmpOut -Destination $out -Force
  Write-Output ('[完成] ' + (Split-Path $file -Leaf) + ' -> ' + (Split-Path $out -Leaf) + ' | 修复边框=' + $total)

  Remove-Item -LiteralPath $extract -Recurse -Force
  Remove-Item -LiteralPath $tmpOut -Force
}
