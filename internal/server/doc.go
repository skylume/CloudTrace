// Package server 提供唯一的前后端契约入口。
//
// 核心设计：
//
//	server.New(cfg, svc) → 一个标准 http.Handler（静态资源 + /api + /ws）
//
// 面板版把它交给 http.ListenAndServe；桌面版把它同时交给 Wails 的
// AssetServer 与一个本地 ListenAndServe。因此**桌面窗口与浏览器命中
// 同一个 handler，天然同源、零 CORS**，前端无需区分运行环境。
//
// 分层：本包依赖 internal/app、internal/config、internal/event、
// internal/model 以及根包 web（内嵌前端产物）；反向依赖被禁止。
package server

// ProtocolVersion 是前后端协议的版本号。
//
// 出现不兼容变更时递增，并在 `state` 事件中下发；
// 前端发现版本不匹配时应提示刷新页面。
const ProtocolVersion = 1
