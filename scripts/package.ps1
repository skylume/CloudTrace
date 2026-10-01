#Requires -Version 5.1
<#
.SYNOPSIS
    把构建产物打成 zip 分发包。

.DESCRIPTION
    先把待打包内容复制到临时暂存目录，再整体压缩。
    这样能精确控制 zip 内部的目录结构（解压后直接看到文件，而不是多套一层文件夹），
    也能让「同一个 exe + 不同附带目录」这种变体（如 Win7 桌面版的 lite / full）
    共用同一份二进制，只改附带内容。

.PARAMETER Path
    要打进包的文件或目录，可多个。目录会连同其内容一起放入包根。

.PARAMETER Destination
    目标 zip 路径。

.PARAMETER Root
    解析相对路径时使用的基准目录；缺省为当前工作目录。

.EXAMPLE
    ./scripts/package.ps1 -Path dist/cloudtrace-panel-1.0.0.exe -Destination dist/cloudtrace-panel-1.0.0-win-x64.zip
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string[]]$Path,

    [Parameter(Mandatory = $true)]
    [string]$Destination,

    [string]$Root = (Get-Location).Path
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
if ($destDir) {
    New-Item -ItemType Directory -Force -Path $destDir | Out-Null
}

# ---- 2. 暂存到临时目录，保持包根结构 ------------------------------------
$staging = Join-Path ([System.IO.Path]::GetTempPath()) ("cloudtrace-pkg-" + [guid]::NewGuid().ToString('N'))
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

    if (Test-Path $destFull) { Remove-Item -Path $destFull -Force }

    # -Path "$staging/*" 保证内容落在 zip 根，而不是包一层暂存目录名。
    Compress-Archive -Path (Join-Path $staging '*') -DestinationPath $destFull -CompressionLevel Optimal

    if (-not (Test-Path $destFull)) {
        throw "压缩结束但产物不存在：$destFull"
    }

    $sizeMB = [math]::Round((Get-Item $destFull).Length / 1MB, 2)
    Write-Host "==> 已打包：$destFull（$sizeMB MB）"
    Write-Output $destFull
}
finally {
    Remove-Item -Path $staging -Recurse -Force -ErrorAction SilentlyContinue
}
