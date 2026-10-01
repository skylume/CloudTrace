#Requires -Version 5.1
<#
.SYNOPSIS
    构建 CloudTrace 的单个发行版二进制。

.DESCRIPTION
    只负责「编译出 exe」，不负责压缩打包（打包见 package.ps1）。
    本地（make build-panel）与 CI 都调用本脚本，保证两边构建方式完全一致。

    两种发行版共用同一份源码，只有构建标签不同：
      panel   → -tags panel   纯 Go，无 GUI 依赖
      desktop → -tags desktop Wails 原生窗口

    两种工具链：
      modern → PATH 上的官方 Go
      win7   → $env:GOLEGACY_ROOT 下的社区分支工具链（补回 Win7 支持）

.PARAMETER Target
    panel 或 desktop。

.PARAMETER Toolchain
    modern（默认）或 win7。

.PARAMETER Version
    写入 main.version 的版本号；缺省 dev。

.PARAMETER OutDir
    产物输出目录，相对仓库根；缺省 dist。

.PARAMETER SkipWeb
    跳过前端构建，直接沿用已存在的 web/dist。

.OUTPUTS
    标准输出最后一行为产物绝对路径，便于 CI 捕获。
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('panel', 'desktop')]
    [string]$Target,

    [ValidateSet('modern', 'win7')]
    [string]$Toolchain = 'modern',

    [string]$Version = 'dev',

    [string]$OutDir = 'dist',

    [switch]$SkipWeb
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# 严格模式下引用未赋值的 $LASTEXITCODE 会抛错，先给一个初值。
$LASTEXITCODE = 0

$repoRoot = Split-Path -Parent $PSScriptRoot

function Invoke-Step {
    param([string]$Label, [scriptblock]$Body)
    Write-Host "==> $Label"
    & $Body
    if ($LASTEXITCODE -ne 0) {
        throw "$Label 失败（退出码 $LASTEXITCODE）"
    }
}

Push-Location $repoRoot
try {
    # ---- 1. 前端产物 ----------------------------------------------------
    # go:embed 在编译期读取 web/dist，缺了会直接编译失败，所以先保证它在。
    if (-not $SkipWeb) {
        if (Test-Path (Join-Path $repoRoot 'web/package.json')) {
            Push-Location (Join-Path $repoRoot 'web')
            try {
                Invoke-Step '构建前端：npm ci' { npm ci }
                Invoke-Step '构建前端：npm run build' { npm run build }
            }
            finally { Pop-Location }
        }
        else {
            Write-Host '==> web/package.json 不存在，沿用已入库的 web/dist 产物'
        }
    }
    if (-not (Test-Path (Join-Path $repoRoot 'web/dist/index.html'))) {
        throw 'web/dist/index.html 不存在：前端产物缺失，go:embed 会编译失败'
    }

    # ---- 2. 入口存在性 --------------------------------------------------
    # 桌面版入口随 Wails 里程碑落地；缺失时给出明确原因，而不是让编译报一堆错。
    if (-not (Test-Path (Join-Path $repoRoot "cmd/$Target"))) {
        throw "入口 cmd/$Target 不存在：该发行版尚未实现，无法构建"
    }

    # ---- 3. 选择工具链 --------------------------------------------------
    $goExe = 'go'
    if ($Toolchain -eq 'win7') {
        $legacyRoot = $env:GOLEGACY_ROOT
        if ([string]::IsNullOrWhiteSpace($legacyRoot)) {
            throw '未设置 GOLEGACY_ROOT：Win7 线需要社区分支工具链，请先执行 scripts/fetch-toolchain.ps1'
        }
        $goExe = Join-Path $legacyRoot 'bin/go.exe'
        if (-not (Test-Path $goExe)) {
            throw "Win7 工具链缺少 go.exe：$goExe"
        }
    }

    $outDirAbs = Join-Path $repoRoot $OutDir
    New-Item -ItemType Directory -Force -Path $outDirAbs | Out-Null
    $outFile = Join-Path $outDirAbs "cloudtrace-$Target-$Version.exe"

    # 静态链接：产物不依赖 libc，分发时不挑机器。
    $env:CGO_ENABLED = '0'
    $ldflags = "-s -w -X main.version=$Version"

    # ---- 4. 构建 --------------------------------------------------------
    switch ($Target) {
        'panel' {
            Invoke-Step "构建 panel（$Toolchain）" {
                & $goExe build -trimpath -tags panel -ldflags $ldflags -o $outFile ./cmd/panel
            }
        }
        'desktop' {
            # 桌面版必须经 wails CLI：它负责生成绑定代码、准备 WebView2 SDK。
            if (-not (Get-Command wails -ErrorAction SilentlyContinue)) {
                throw '未找到 wails CLI，请先安装：go install github.com/wailsapp/wails/v2/cmd/wails@latest'
            }
            $desktopDir = Join-Path $repoRoot 'cmd/desktop'
            Push-Location $desktopDir
            try {
                Invoke-Step "构建 desktop（$Toolchain）" {
                    wails build -tags desktop -trimpath -ldflags $ldflags
                }
            }
            finally { Pop-Location }

            # wails 的产物落在 cmd/desktop/build/bin，统一收敛到 dist/。
            $built = Get-ChildItem -Path (Join-Path $desktopDir 'build/bin') -Filter '*.exe' -ErrorAction SilentlyContinue |
                Select-Object -First 1
            if (-not $built) {
                throw "wails 构建完成但未在 cmd/desktop/build/bin 找到 exe"
            }
            Copy-Item -Path $built.FullName -Destination $outFile -Force
        }
    }

    if (-not (Test-Path $outFile)) {
        throw "构建结束但产物不存在：$outFile"
    }
    Write-Host "==> 产物：$outFile"
    Write-Output $outFile
}
finally {
    Pop-Location
}
