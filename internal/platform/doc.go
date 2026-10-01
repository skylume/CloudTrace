// Package platform 收敛所有与操作系统相关的实现（build tags 分文件）。
//
// 约定：本包只做「平台能力」，不含任何业务逻辑，也不得依赖
// scan / speed / server 等上层包。所有函数在失败时只返回错误，
// **不得 panic、不得退出进程** —— 平台能力缺失应当降级而不是中断服务。
package platform
