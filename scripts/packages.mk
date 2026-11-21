# CloudTrace 包清单 —— 唯一事实来源
#
# 这份清单以前在 Makefile 和 scripts/ci-check.ps1 里各存一份，注释里写着
# 「改动时两边要一起改」——而人总会漏掉一处。现在 Makefile 用 include 取它，
# ci-check.ps1 用 Read-PackageManifest 解析它：改清单只改这里。
#
# 格式约束（两个消费者都依赖）：`名字 := 值`，值是以空格分隔的一组路径，
# 注释行以 # 开头；不要写变量引用或 make 函数，PowerShell 那边只做文本解析。
# 长列表（如 COVERAGE_PACKAGES）必须保持**单行**：追加条目就在行尾补一个空格
# 加完整路径，不要换行续写，也不要加引号或注释。
#
# 为什么显式列包路径而不是 `./...`：web/node_modules 里有个 npm 包（flatted）
# 夹带了 Go 源码，`./...` 会把它当成本模块的包一起编译、一起算覆盖率——
# 它和这个项目毫无关系，哪天它编译不过就会连带把 CI 弄红。

# go vet / go build / go test 的包范围。**新增顶层目录时加进这里。**
PACKAGES := ./assets/... ./cmd/... ./internal/...

# gofmt 检查（ci-check.ps1）与自动格式化（make fmt）的目录范围。
GOFMT_PATHS := ./cmd ./internal ./web

# 覆盖率门禁针对的核心包（导入路径）。只对已存在的包做硬性门禁，
# 不存在即跳过——详见 check-coverage.ps1。
#
# 覆盖率按**语句级**口径统计：解析 coverprofile 每行的 numStmt 与 count，
# 累加得到「已覆盖语句数 / 语句总数」，与 `go test -cover` 报出的百分比同源。
# 它不是把函数百分比做平均，所以这里的包名不需要按规模加权。
#
# 追加方式：在本行末尾补一个空格 + 完整导入路径（例如
# cloudtrace/internal/xxx），保持单行；ci-check.ps1 只按空白切分后逐个比对。
COVERAGE_PACKAGES := cloudtrace/internal/probe cloudtrace/internal/source cloudtrace/internal/scan cloudtrace/internal/speed cloudtrace/internal/history cloudtrace/internal/score cloudtrace/internal/geo
