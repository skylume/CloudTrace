#Requires -Version 5.1
<#
.SYNOPSIS
    检查核心包的测试覆盖率是否达标。

.DESCRIPTION
    输入是 `go test -coverprofile` 产生的 profile，按包聚合后与阈值比较。

    统计口径是**语句级**：逐行解析 profile
        path/file.go:startLine.startCol,endLine.endCol numStmt count
    对每个包分别累加 numStmt 作为分母、count > 0 的 numStmt 作为分子，得到
        sum(numStmt, count > 0) / sum(numStmt)
    这与 `go test -cover` 报出的包覆盖率是同一个口径。

    不要用 `go tool cover -func` 的函数百分比再做平均：那等于让 3 行的
    getter 和 200 行的主流程等权。实测会把 internal/history 从语句级的 87.0%
    抬到 92.1%，阈值 70% 下实际放行了只有 65% 的包。

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

# ---- 1. 解析 profile，按包聚合 -----------------------------------------
# 每个数据行形如：
#   cloudtrace/internal/history/index.go:12.34,18.2 5 1
# 字段依次为「文件:起始行.列,结束行.列」「语句数」「命中次数」。
#
# 注意：这里是语句级聚合，不是把文件或函数的百分比做算术平均。profile 里的
# numStmt 才是权重，count 才是这一块有没有被执行到。
$stmtTotal = @{}
$stmtCovered = @{}
$entries = 0
$lineNo = 0

foreach ($line in Get-Content -Path $CoverProfile -Encoding UTF8) {
    $lineNo++
    if ([string]::IsNullOrWhiteSpace($line)) { continue }
    # 第一行是「mode: set/atomic/count」，不参与统计。
    if ($line -match '^mode:\s*\S+$') { continue }

    if ($line -notmatch '^(?<file>.+):(?<sl>\d+)\.(?<sc>\d+),(?<el>\d+)\.(?<ec>\d+)\s+(?<stmt>\d+)\s+(?<count>\d+)\s*$') {
        throw "覆盖率文件第 $lineNo 行无法解析：$line（文件：$CoverProfile）"
    }

    # 文件名形如 cloudtrace/internal/history/index.go，去掉最后一段即包路径。
    $file = $Matches['file'] -replace '\\', '/'
    $pkgPath = $file -replace '/[^/]+$', ''
    $stmt = [int]$Matches['stmt']
    $count = [int]$Matches['count']

    if (-not $stmtTotal.ContainsKey($pkgPath)) {
        $stmtTotal[$pkgPath] = 0
        $stmtCovered[$pkgPath] = 0
    }
    $stmtTotal[$pkgPath] += $stmt
    if ($count -gt 0) {
        $stmtCovered[$pkgPath] += $stmt
    }
    $entries++
}

if ($entries -eq 0) {
    $where = (Get-Location).Path
    $resolved = Join-Path $where $CoverProfile
    throw "覆盖率文件为空或没有任何有效数据行：$CoverProfile（在 $where 下解析为 $resolved）"
}

$byPkg = @{}
foreach ($pkgPath in $stmtTotal.Keys) {
    $total = $stmtTotal[$pkgPath]
    if ($total -gt 0) {
        $byPkg[$pkgPath] = 100.0 * $stmtCovered[$pkgPath] / $total
    }
    else {
        $byPkg[$pkgPath] = 0.0
    }
}

# ---- 2. 输出总览 --------------------------------------------------------
Write-Host '==> 各包覆盖率（语句级）'
$sorted = $byPkg.GetEnumerator() | Sort-Object Name
foreach ($entry in $sorted) {
    Write-Host ("    {0,-45} {1,6:N1}%" -f $entry.Name, $entry.Value)
}

# ---- 3. 门禁 ------------------------------------------------------------
if ($Package.Count -eq 0) {
    Write-Host '==> 未指定核心包，跳过门禁'
    exit 0
}

$existing = @(& go list ./...)
if ($existing.Count -eq 0) {
    Write-Host '==> 覆盖率门禁：go list ./... 没有返回任何包，请在模块根目录运行' -ForegroundColor Red
    exit 1
}

# 传入的包一个都不在模块里，多半是清单写错了（例如整串被当成一个包名传进来）。
# 那种情况下每个 want 都会落到下面的「尚未实现」分支，门禁会静默通过——门禁
# 形同虚设比门禁报错糟糕得多，所以这里直接失败。
$matched = @($Package | Where-Object { $existing -contains $_ })
if ($matched.Count -eq 0) {
    Write-Host '==> 覆盖率门禁：传入的包一个都不在模块内' -ForegroundColor Red
    Write-Host ("    传入：{0}" -f ($Package -join ' '))
    Write-Host ("    模块内共 {0} 个包" -f $existing.Count)
    exit 1
}

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
    $pct = $byPkg[$want]
    if ($pct -lt $Threshold) {
        $failures += ("{0}（{1:N1}% < {2}%）" -f $want, $pct, $Threshold)
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
