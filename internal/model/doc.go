// Package model 定义跨包共享的数据模型。
//
// 设计约束：
//   - 本包只依赖标准库，禁止反向依赖 scan / speed / server 等上层包；
//   - 字段名即前后端契约字段名，前后端同序同名，不做转换；
//   - 本包不得持有任何包级可变状态。
//
// 当前定义了任务状态快照（TaskState / Funnel）、扫描参数快照
// （ScanParams）、参数来源三态（ParamOrigin / ParamOrigins）、单个 IP 的
// 结果记录（IPRecord）与统计摘要（Summary）；档位（Preset）与历史记录
// 随后续功能一并补齐。
package model
