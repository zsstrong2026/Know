# MinerU 服务栈自启脚本（登录触发，与 Docker Desktop 自启配套）
# 拉起顺序：llama-server@18899 → mineru-api@8001 → v1 适配器@8002（WeKnora 桥接）
# 幂等：端口已在监听则跳过对应服务，可安全重复执行。

$ErrorActionPreference = 'Continue'
$LogDir  = Join-Path $PSScriptRoot 'logs'
$LogFile = Join-Path $LogDir 'mineru_stack.log'
New-Item -ItemType Directory -Force -Path $LogDir | Out-Null

function Write-Log([string]$msg) {
    $line = "[{0}] {1}" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $msg
    Add-Content -Path $LogFile -Value $line -Encoding UTF8
}

function Test-Port([int]$Port) {
    return [bool](Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue)
}

# 轮询等待端口进入监听，返回是否成功
function Wait-Port([int]$Port, [int]$TimeoutSec) {
    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        if (Test-Port $Port) { return $true }
        Start-Sleep -Seconds 2
    }
    return (Test-Port $Port)
}

# 通用拉起：隐藏窗口 + stdout/stderr 重定向到独立日志，启动后等待端口就绪
function Start-StackService([string]$Name, [string]$Exe, [string]$Arguments, [int]$Port, [string]$WorkDir, [int]$WaitSec) {
    if (Test-Port $Port) {
        Write-Log "$Name : port $Port already listening, skip."
        return $true
    }
    $out = Join-Path $LogDir "$Name.out.log"
    $err = Join-Path $LogDir "$Name.err.log"
    $params = @{
        FilePath               = $Exe
        ArgumentList           = $Arguments
        WindowStyle            = 'Hidden'
        RedirectStandardOutput = $out
        RedirectStandardError  = $err
    }
    if ($WorkDir) { $params.WorkingDirectory = $WorkDir }
    Start-Process @params
    Write-Log "$Name : launched, waiting port $Port (timeout ${WaitSec}s)..."
    if (Wait-Port $Port $WaitSec) {
        Write-Log "$Name : UP on $Port."
        return $true
    }
    Write-Log "$Name : ERROR, not listening on $Port within ${WaitSec}s (see $err)."
    return $false
}

Write-Log '===== MinerU stack autostart begin ====='

# --- 0) 等 Docker 引擎就绪（Docker Desktop 同为登录自启，引擎起来需要时间）---
# MinerU 本身不依赖 Docker，超时仅记录不阻断：先起 MinerU 可让 WeKnora 容器就绪后立即可用。
$dockerReady = $false
$deadline = (Get-Date).AddMinutes(5)
while ((Get-Date) -lt $deadline) {
    docker info *> $null
    if ($LASTEXITCODE -eq 0) { $dockerReady = $true; break }
    Start-Sleep -Seconds 5
}
if ($dockerReady) { Write-Log 'Docker engine ready.' } else { Write-Log 'WARN: Docker not ready within 5min, start MinerU anyway.' }

# --- 1) llama-server @18899（VLM 推理，OpenAI 兼容）---
# 直接启动包内 llama-server.exe，不走 mineru-openai-server 包装：Windows 下其 os.execv
# 转发的命令行会丢失引号，grammar 值 "root ::= .*" 被拆散导致启动失败。
# 以下参数复刻包装层默认值：-ngl 99 / --parallel 4 / --special / --no-kv-unified /
# --cache-ram 0 / grammar，--ctx-size = 训练上下文 8192 × 4 并发（GGUF 元数据实测）。
$ggufDir = 'E:\AI-Models\mineru\MinerU2.5-Pro-2605-1.2B-GGUF'
$llamaExe = 'C:\mineru-venv\Lib\site-packages\mineru_llama_cpp\bin\llama-server.exe'
$llamaArgs = '-m "{0}\MinerU2.5-Pro-2605-1.2B-Q8_0.gguf" --mmproj "{0}\mmproj-MinerU2.5-Pro-2605-1.2B-Q8_0.gguf" --port 18899 --grammar "root ::= .*" --special --parallel 4 --n-gpu-layers 99 --no-kv-unified --cache-ram 0 --ctx-size 32768 --alias MinerU2.5-Pro-2605-1.2B-GGUF' -f $ggufDir
$ok1 = Start-StackService -Name 'llama_server' -Exe $llamaExe -Arguments $llamaArgs -Port 18899 -WaitSec 300

# --- 2) mineru-api @8001（V2 API，WeKnora 实际解析后端）---
# 环境变量在此进程设置，Start-Process 的子进程会继承。
# BASE_DIR/LOCK_DIR 指向 C 盘副本：E 盘模型树对沙箱只读，锁目录与模型补下载必须落在可写位置。
$env:MINERU_MODEL_BASE_DIR = 'C:\mineru-venv\models'
$env:MINERU_MODEL_LOCK_DIR = 'C:\mineru-venv\models\.locks'
$env:MINERU_MODEL_SOURCE = 'modelscope'
$env:HF_HOME = 'd:\知识库\Know\.cache\hf'
$env:MODELSCOPE_CACHE = 'd:\知识库\Know\.cache\ms-real'
# 上传目录必须是绝对可写路径：MinerU 的 FileStore 会在该目录下创建 blobs，
# 若进程工作目录不可写（计划任务/登录触发时 cwd 常为 C:\Windows\System32）
# 会直接 PermissionError 启动即崩，表现为 8001 起不来、WeKnora 判定 MinerU 不可用。
$uploadDir = 'C:\mineru-venv\uploads'
New-Item -ItemType Directory -Force -Path $uploadDir | Out-Null
# 注意：不要加 --concurrency >1。MinerU 4.0.11 单机部署下开启后 /v1/uploads
# 与 /v1/parse/jobs 会直接返回 502（无响应体），解析全部失败；并发只能靠
# 多实例 + 上层负载均衡，不能用该参数。
$apiArgs = '--host 0.0.0.0 --port 8001 --tier standard --vlm-server-url http://127.0.0.1:18899 --upload-dir "{0}"' -f $uploadDir
$ok2 = Start-StackService -Name 'mineru_api' -Exe 'C:\mineru-venv\Scripts\mineru-api.exe' -Arguments $apiArgs -Port 8001 -WorkDir $PSScriptRoot -WaitSec 240

# --- 3) V1→V2 适配器 @8002（WeKnora 面板填 http://host.docker.internal:8002）---
$adapterPath = Join-Path $PSScriptRoot 'mineru_v1_adapter.py'
$ok3 = Start-StackService -Name 'v1_adapter' -Exe 'C:\mineru-venv\Scripts\python.exe' -Arguments "`"$adapterPath`"" -Port 8002 -WorkDir $PSScriptRoot -WaitSec 120

Write-Log ("===== autostart done: llama={0} api={1} adapter={2} =====" -f $ok1, $ok2, $ok3)
