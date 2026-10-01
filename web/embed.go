// Package web 承载内嵌的前端产物。
//
// 前端源码位于 web/src（由 Vite 构建输出到 web/dist），本包把构建产物
// 编译进二进制，因此发布物是单文件、无运行时依赖。
//
// 注意：go:embed 只能嵌入本包目录及其子目录，所以内嵌声明必须放在这里，
// 不能放在 internal/server —— 否则编译期就会报错。
package web

import "embed"

// Dist 是前端构建产物。
//
//go:embed dist
var Dist embed.FS
