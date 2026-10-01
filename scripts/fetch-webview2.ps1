#Requires -Version 5.1
<#
.SYNOPSIS
    准备固定版本的 WebView2 运行时，用于 Win7 桌面版的 full 变体。

.DESCRIPTION
    Win7 桌面版要求固定版本 WebView2（109.0.1518.140，最后一个支持 Win7 的版本），
    因为它不再随系统更新，行为可预测。

    微软只提供 GUID 形式的临时下载地址，不具备可复现性，因此本脚本不硬编码地址，
    而是要求调用方显式给出 -Url。请把地址配置为仓库变量 WEBVIEW2_CAB_URL，
    Release 工作流会读它；未配置时 full 变体会被跳过（不阻断其余产物）。

    运行时包是 .cab，需用 Windows 自带的 expand.exe 解包。

.PARAMETER Url
    .cab 下载地址。必填。

.PARAMETER Version
    期望的版本号，用于解包后校验，避免拿错版本。

.PARAMETER Dest
    解包目标目录（脚本会把它整理成 Dest/ 下的运行时文件）。
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Url,

    [string]$Version = '109.0.1518.140',

    [Parameter(Mandatory = $true)]
    [string]$Dest
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

Write-Host "==> 下载 WebView2 固定版本运行时：$Url"
New-Item -ItemType Directory -Force -Path $Dest | Out-Null
$cabPath = Join-Path ([System.IO.Path]::GetTempPath()) "webview2-$Version.cab"

$prevProgress = $ProgressPreference
$ProgressPreference = 'SilentlyContinue'
try {
    Invoke-WebRequest -Uri $Url -OutFile $cabPath -UseBasicParsing
}
finally {
    $ProgressPreference = $prevProgress
}

if (-not (Test-Path $cabPath)) {
    throw "下载失败：$Url"
}
Write-Host ("==> 已下载 {0} MB" -f [math]::Round((Get-Item $cabPath).Length / 1MB, 2))

Write-Host "==> 解包 .cab 到：$Dest"
# expand.exe 是 Windows 自带工具；-F:* 表示解出包内全部文件。
& expand.exe "$cabPath" -F:* "$Dest" | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw "expand.exe 解包失败（退出码 $LASTEXITCODE）"
}
Remove-Item -Path $cabPath -Force

# 校验：解包结果里应能看到主程序，且版本号对得上。
$runtimeExe = Get-ChildItem -Path $Dest -Recurse -Filter 'msedgewebview2.exe' -ErrorAction SilentlyContinue |
    Select-Object -First 1
if (-not $runtimeExe) {
    throw "解包后未找到 msedgewebview2.exe，包结构可能已变化：$Dest"
}

$fileVersion = (Get-Item $runtimeExe.FullName).VersionInfo.FileVersion
Write-Host "==> 运行时版本：$fileVersion"
if ($fileVersion -and ($fileVersion -notlike "$Version*")) {
    throw "版本不符：期望 $Version，实际 $fileVersion"
}

Write-Output $Dest
