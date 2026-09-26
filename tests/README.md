# CloudTrace 验证脚本

四个**完全离线**的验证脚本，覆盖核心逻辑、HTTP 服务层、桌面 UI 与 Web 面板。
运行过程中不会访问网络，也不会改动仓库文件（`settings.json` 与历史目录
在测试前后会被原样还原）。

## 运行

```bash
# 一次性跑完（推荐）
python tests/run_all.py

# 或单独运行
python tests/verify_core.py     # 核心逻辑单测（97 项）
python tests/verify_api.py      # aiohttp 接口 / SSE / 鉴权 / 导出（48 项）
python tests/verify_ui.py       # PySide6 离屏冒烟（46 项）
node  tests/verify_web.js       # Web 面板 DOM 冒烟（91 项，需 jsdom）
```

- `verify_ui.py` 会自动设置 `QT_QPA_PLATFORM=offscreen`，无需图形界面；
  同时设置 `CLOUDTRACE_ALLOW_MULTI=1` 以跳过单实例锁。
- `verify_web.js` 需要 `jsdom`。默认从 WorkBuddy 托管的 node 工作区加载；
  若装在别处，用 `CLOUDTRACE_NODE_MODULES` 或 `NODE_PATH` 指向 `node_modules` 所在目录。

## 覆盖范围

### `verify_core.py` — 核心逻辑
- `to_int / to_float / to_bool` 类型兜底与钳制，含 NaN / ±Inf（缺陷 3.8）
- 原子写文件与安全读取（防半截 JSON）
- `parse_speed_url` 的 scheme / 端口 / TLS 推断
- `parse_ip_list` 非标导入：`ip:port`、`[v6]:port`、空格端口、`#` 注释、去重、非法行报错（缺陷 3.7）
- `parse_cidr_lines` 版本过滤、`port_uses_tls` 端口判定
- 导出渲染 CSV / JSON / TXT（含 IPv6 方括号、字段裁剪、合格筛选）
- `region_stats` 聚合
- `sanitize_settings` 设置规范化（缺陷 3.11）
- `validate_settings` 配置体检
- 历史保存 / 读取 / 列表 / 删除往返
- `SpeedTestTask` 中止语义：`run()` 返回 `None` 而非 `[]`（缺陷 3.2）
- `create_scanner` 参数兜底

### `verify_api.py` — 服务层
- `/api/state` 不回传明文 Token，只给 `http_token_set`
- `PUT /api/settings` 就地更新共享对象、坏类型被规范化、只读字段被忽略
- `POST /api/settings/reset` 恢复默认
- Token 鉴权：无 / 错误 → 401，正确（Header 或 query）→ 200
- 导出：空结果 404、非法格式 400、CSV 带 BOM、TXT 输出 `ip:port`、JSON 字段裁剪
- 扫描 / 测速参数校验（`ip_version`、`source_mode`、CIDR、非标列表）
- 历史接口参数校验与路径穿越防护
- SSE `settings` 事件**不携带明文 Token**
- 静态资源可达

### `verify_ui.py` — 桌面 UI（离屏）
- `WorkerBridge` 9 条订阅全部登记、`detach()` 后归零（缺陷 3.1）
- `EV_SPEED_ABORT / EV_SCAN_DONE(None)` 正确映射为中止信号
- `RegionChips` 重建无重影（缺陷 3.6）
- `FunnelBar` 步骤顺序正确、重建无错位（回归测试）
- `CustomMessageBox` 自适应尺寸（长文本不被裁切）
- `HistorySelectDialog` 双击信号重载
- `ExportDialog` 新增 TXT 格式
- 各页面构建、设置回填、主窗口页面切换
- 设置广播后桌面表单同步（缺陷 3.3）
- 测速中止后状态为「已停止」而非「完成」（缺陷 3.2）
- 结果表勾选全部 / 清空
- 单实例锁

### `verify_web.js` — Web 面板（jsdom 真实执行 `app.js`）
- 首屏加载、SSE 建立、无 JS 运行时错误
- 设置回填（含 `speed_workers` / `speed_result_limit`，Token 不回显）
- 来源模式切换的显隐、默认延迟筛选行为
- SSE `state` 快照 → 统计卡与表格渲染
- 地区芯片全选/清空、勾选全部/清空、延迟筛选联动
- 导出弹窗（csv/json/txt）与**测速页无结果时的导出兜底**（缺陷 3.9）
- `speed_done` / `speed_abort` 的中止语义（缺陷 3.2）
- SSE `settings` → 表单同步（缺陷 3.3）
- 页面导航标题、401 鉴权流程

## 其他

`render_screenshots.py` 用于重新生成 `Screenshots/` 下的界面预览图：

```bash
python tests/render_screenshots.py
```

它用「原生平台 + `WA_DontShowOnScreen`」渲染，不弹窗但能拿到系统真实字体
（含 emoji）。若强制用 `CLOUDTRACE_SHOT_OFFSCREEN=1` 走 offscreen 平台，
文字仍可读但 emoji 会变成方块——这是该后端字体库为空的固有限制。
