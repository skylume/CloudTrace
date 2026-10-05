#Requires -Version 5.1
<#
.SYNOPSIS
    检查核心包的测试覆盖率是否达标。

.DESCRIPTION
    输入是 `go test -coverprofile` 产生的 profile，按包聚合后与阈值比较。

    设计要点：
      - 只对「已经存在」的核心包做硬性门禁。里程碑还没做到那里时，
        该包不存在，就不应该让流水线因为一个不存在的包变红。
      - 核心包存在但一行测试都没有 → 视为 0%，直接失败（这正是要防的情况）。

.PARAMETER CoverProfile
    coverage profile 路径。

.PARAMETER Threshold
    覆盖率阈值（百分比），缺省 70。

.PARAMETER Package
    需要门禁的核心包导入路径，可多个。

.EXAMPLE
    ./scripts/check-coverage.ps1 -CoverProfile coverage.out -Threshold 70 -Package cloudtrace/internal/config
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$CoverProfile,

    [double]$Threshold = 70,

    [string[]]$Package = @()
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$LASTEXITCODE = 0

if (-not (Test-Path $CoverProfile)) {
    # 把「找的是哪个绝对路径、当前目录在哪」一起报出来。
    #
    # 上一次这个检查在 CI 上失败时，日志里只有一句「文件不存在」，而产出文件的
    # 那一步看起来一切正常——没有这两条信息就只能靠猜。
    $where = (Get-Location).Path
    $resolved = Join-Path $where $CoverProfile
    throw "覆盖率文件不存在：$CoverProfile（在 $where 下解析为 $resolved）"
}

# ---- 1. 按包聚合 --------------------------------------------------------
# `go tool cover -func` 每行形如：
#   cloudtrace/internal/config/config.go:120:  Default  100.0%
#
# 注意 -func= 的值必须先拼成字符串再传：写成 `-func=$CoverProfile` 时
# PowerShell 不会做变量展开，go 会收到字面量 "$CoverProfile" 并报文件不存在。
$coverArg = "-func=$CoverProfile"
$rows = & go tool cover $coverArg
if ($LASTEXITCODE -ne 0) {
    throw 'go tool cover 执行失败'
}

$byPkg = @{}
foreach ($line in $rows) {
    if ($line -notmatch '^(?<file>[^:]+):\d+:\s+(?<fn>\S+)\s+(?<pct>[\d.]+)%\s*$') { continue }
    $file = $Matches['file']
    $pct = [double]$Matches['pct']

    # 文件名形如 cloudtrace/internal/config/config.go，去掉最后一段即包路径。
    $pkgPath = ($file -replace '\\', '/') -replace '/[^/]+$', ''
    if (-not $byPkg.ContainsKey($pkgPath)) {
        $byPkg[$pkgPath] = [System.Collections.Generic.List[double]]::new()
    }
    $byPkg[$pkgPath].Add($pct)
}

if ($byPkg.Count -eq 0) {
    Write-Warning '覆盖率文件中没有任何可统计的条目'
}

# ---- 2. 输出总览 --------------------------------------------------------
Write-Host '==> 各包覆盖率'
$sorted = $byPkg.GetEnumerator() | Sort-Object Name
foreach ($entry in $sorted) {
    $avg = ($entry.Value | Measure-Object -Average).Average
    Write-Host ("    {0,-45} {1,6:N1}%" -f $entry.Name, $avg)
}

# ---- 3. 门禁 ------------------------------------------------------------
if ($Package.Count -eq 0) {
    Write-Host '==> 未指定核心包，跳过门禁'
    exit 0
}

$existing = @(& go list ./...)
$failures = @()

foreach ($want in $Package) {
    if ($existing -notcontains $want) {
        Write-Host "    [跳过] $want 尚未实现"
        continue
    }
    if (-not $byPkg.ContainsKey($want)) {
        $failures += "$want（存在但没有任何测试覆盖）"
        continue
    }
    $avg = ($byPkg[$want] | Measure-Object -Average).Average
    if ($avg -lt $Threshold) {
        $failures += ("{0}（{1:N1}% < {2}%）" -f $want, $avg, $Threshold)
    }
}

if ($failures.Count -gt 0) {
    Write-Host ''
    Write-Host '覆盖率门禁未通过：' -ForegroundColor Red
    foreach ($f in $failures) { Write-Host "    - $f" }
    exit 1
}

Write-Host ''
Write-Host "==> 覆盖率门禁通过（阈值 $Threshold%）"
