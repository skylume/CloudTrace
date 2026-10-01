// Package app 负责应用装配与生命周期。
//
// 设计约束：
//   - 依赖注入，**禁止全局单例**：所有依赖由 Services 承载并显式传递；
//   - 本包可以依赖所有 internal/* 业务包，但**不得**依赖 internal/server
//     （server 反向依赖本包），否则形成循环；
//   - 不持有包级可变状态。
package app
