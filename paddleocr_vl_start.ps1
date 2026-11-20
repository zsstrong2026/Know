# PaddleOCR-VL 服务自启脚本（登录触发，与 MinerU 栈自启并列）
# 启动 PaddleX PaddleOCR-VL-1.6 serving，监听 8081，供 WeKnora 的 paddleocr_vl 引擎调用。
# 幂等：端口已在监听则跳过，可安全重复执行。

$ErrorActionPreference = 'Continue'
# 硬编码根目录：计划任务/WMI 启动时 $PSScriptRoot 可能为空
$BaseDir = 'D:\知识库\Know'
$LogDir = Join-Path $BaseDir 'logs'
New-Item -ItemType Directory -Force -Path $LogDir | Out-Null
$LogFile = Join-Path $LogDir 'paddleocr_vl_stack.log'

function Write-Log([string]$msg) {
    $line = "[{0}] {1}" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $msg
    Add-Content -Path $LogFile -Value $line -Encoding UTF8
}

if (Get-NetTCPConnection -LocalPort 8081 -State Listen -ErrorAction SilentlyContinue) {
    Write-Log 'port 8081 already listening, skip.'
    exit 0
}

# 模型缓存必须放在纯 ASCII 路径：Paddle Inference 的 C++ 层无法打开含中文的路径
$env:PADDLE_PDX_CACHE_HOME = 'D:\paddlex-cache'
$env:PADDLE_PDX_MODEL_SOURCE = 'ModelScope'
$env:PADDLE_PDX_DISABLE_MODEL_SOURCE_CHECK = 'True'
$env:PYTHONUTF8 = '1'
# 版面可视化需要字体，默认会联网下载；本机代理不可用时会导致 500，改用系统字体
$env:PADDLE_PDX_LOCAL_FONT_FILE_PATH = 'C:\Windows\Fonts\simfang.ttf'

$exe = 'D:\知识库\Know\paddleocr-env\Scripts\paddlex.exe'
$out = Join-Path $LogDir 'paddleocr_vl.out.log'
$err = Join-Path $LogDir 'paddleocr_vl.err.log'

Start-Process -FilePath $exe `
    -ArgumentList '--serve', '--pipeline', 'PaddleOCR-VL-1.6', '--host', '0.0.0.0', '--port', '8081' `
    -WorkingDirectory $BaseDir -WindowStyle Hidden `
    -RedirectStandardOutput $out -RedirectStandardError $err

Write-Log 'launched, waiting port 8081 (timeout 600s)...'
# 首启需下载约 2GB 的 0.9B 权重，留足等待时间
$deadline = (Get-Date).AddSeconds(600)
while ((Get-Date) -lt $deadline) {
    if (Get-NetTCPConnection -LocalPort 8081 -State Listen -ErrorAction SilentlyContinue) {
        Write-Log 'UP on 8081.'
        exit 0
    }
    Start-Sleep -Seconds 3
}
Write-Log 'ERROR: port 8081 not listening within 600s (see paddleocr_vl.err.log).'
exit 1

