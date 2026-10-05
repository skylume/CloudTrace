#Requires -Version 5.1
<#
.SYNOPSIS
    把构建产物打成 zip 分发包。

.DESCRIPTION
    先把待打包内容复制到暂存目录，再整体压缩。
    这样能精确控制 zip 内部的目录结构（解压后直接看到文件，而不是多套一层文件夹），
    也能让「同一个 exe + 不同附带目录」这种变体（如 Win7 桌面版的 lite / full）
    共用同一份二进制，只改附带内容。

    暂存目录默认落在系统临时目录并在结束后删掉；给了 -StageDir 则落在那里并保留
    ——CI 上传的就是这份暂存内容，而不是 zip 本身（artifact 下载下来本来就会被
    GitHub 再包一层，上传 zip 会变成「解压两次才看到 exe」）。

.PARAMETER Path
    要打进包的文件或目录，可多个。目录会连同其内容一起放入包根。

.PARAMETER Destination
    目标 zip 路径。

.PARAMETER Root
    解析相对路径时使用的基准目录；缺省为当前工作目录。

.PARAMETER StageDir
    暂存目录的父目录。给了它就把暂存目录建在这里并保留（CI 要上传它）；
    不给则用系统临时目录，结束后清理。

.PARAMETER StageOnly
    只准备暂存目录，不产出 zip。CI 的构建 job 用它：zip 由汇总发布那一步统一生成，
    每个 job 各压一遍等于把几百兆的运行时压两次。

.OUTPUTS
    标准输出最后一行是 zip 的绝对路径（-StageOnly 时是暂存目录的绝对路径）。
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string[]]$Path,

    [Parameter(Mandatory = $true)]
    [string]$Destination,

    [string]$Root = (Get-Location).Path,

    [string]$StageDir,

    [switch]$StageOnly
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Resolve-Item {
    param([string]$Item)
    if ([System.IO.Path]::IsPathRooted($Item)) { return $Item }
    return (Join-Path $Root $Item)
}

# ---- 1. 先校验全部来源都存在，避免压到一半才发现缺文件 ------------------
$resolved = @()
foreach ($item in $Path) {
    $full = Resolve-Item $item
    if (-not (Test-Path $full)) {
        throw "待打包项不存在：$full"
    }
    $resolved += (Get-Item $full)
}

$destFull = Resolve-Item $Destination
$destDir = Split-Path -Parent $destFull
if ($destDir -and -not $StageOnly) {
    New-Item -ItemType Directory -Force -Path $destDir | Out-Null
}

# ---- 2. 暂存到临时目录，保持包根结构 ------------------------------------
$keepStaging = -not [string]::IsNullOrWhiteSpace($StageDir)
if ($keepStaging) {
    $stageRoot = Resolve-Item $StageDir
    New-Item -ItemType Directory -Force -Path $stageRoot | Out-Null
    # 暂存目录名与包名一致：CI 上传它之后，下载到的 zip 名字就是包名。
    $staging = Join-Path $stageRoot ([System.IO.Path]::GetFileNameWithoutExtension($destFull))
    if (Test-Path $staging) { Remove-Item -Path $staging -Recurse -Force }
}
else {
    $staging = Join-Path ([System.IO.Path]::GetTempPath()) ("cloudtrace-pkg-" + [guid]::NewGuid().ToString('N'))
}
New-Item -ItemType Directory -Force -Path $staging | Out-Null

try {
    foreach ($item in $resolved) {
        if ($item.PSIsContainer) {
            Copy-Item -Path $item.FullName -Destination $staging -Recurse -Force
        }
        else {
            Copy-Item -Path $item.FullName -Destination $staging -Force
        }
    }

    if ($StageOnly) {
        Write-Host "==> 已暂存：$staging"
        Write-Output $staging
        return
    }

    if (Test-Path $destFull) { Remove-Item -Path $destFull -Force }

    # 用 ZipFile 而不是 Compress-Archive：后者在 PowerShell 5.1 上对几百兆的目录
    # 慢得多，而 Win7 桌面版的 full 包光运行时就有 440MB。
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    # 第三个参数是压缩级别，第四个 false 表示不要把暂存目录本身当成顶层条目。
    [System.IO.Compression.ZipFile]::CreateFromDirectory(
        $staging,
        $destFull,
        [System.IO.Compression.CompressionLevel]::Optimal,
        $false
    )

    if (-not (Test-Path $destFull)) {
        throw "压缩结束但产物不存在：$destFull"
    }

    $sizeMB = [math]::Round((Get-Item $destFull).Length / 1MB, 2)
    Write-Host "==> 已打包：$destFull（$sizeMB MB）"
    Write-Output $destFull
}
finally {
    # 保留的暂存目录是产物本身（CI 要上传它），不能删。
    if (-not $keepStaging) {
        Remove-Item -Path $staging -Recurse -Force -ErrorAction SilentlyContinue
    }
}
