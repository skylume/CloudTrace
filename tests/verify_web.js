/* CloudTrace Web 面板 DOM 冒烟测试（jsdom，真实执行 app.js） */
"use strict";

const fs = require("fs");
const path = require("path");
const { JSDOM, VirtualConsole } = require("jsdom");

const ROOT = path.resolve(__dirname, "..");
const HTML = fs.readFileSync(path.join(ROOT, "web", "index.html"), "utf8");
const APP = fs.readFileSync(path.join(ROOT, "web", "app.js"), "utf8");

const PASS = [];
const FAIL = [];
function check(name, cond, detail) {
  (cond ? PASS : FAIL).push(name);
  console.log((cond ? "  [OK]   " : "  [FAIL] ") + name +
    (!cond && detail !== undefined ? "  -> " + String(detail) : ""));
}

const PRELUDE = `
<script>
window.__errors = [];
window.addEventListener("error", function (e) {
  window.__errors.push("uncaught: " + (e.message || e.error));
});
var _ce = console.error;
console.error = function () {
  window.__errors.push("console.error: " + Array.prototype.join.call(arguments, " "));
  _ce.apply(console, arguments);
};

window.__sse = null;
function FakeEventSource(url) {
  this.url = url; this.listeners = {}; this.closed = false;
  window.__sse = this;
  var self = this;
  setTimeout(function () { self._emit("open", {}); }, 0);
}
FakeEventSource.prototype.addEventListener = function (t, fn) {
  (this.listeners[t] = this.listeners[t] || []).push(fn);
};
FakeEventSource.prototype.removeEventListener = function () {};
FakeEventSource.prototype.close = function () { this.closed = true; };
FakeEventSource.prototype._emit = function (type, data) {
  var ev = { data: data === undefined ? "{}" : JSON.stringify(data) };
  var self = this;
  (this.listeners[type] || []).forEach(function (fn) { fn(ev); });
  // app.js 用 es.onopen / es.onerror 属性赋值（而非 addEventListener）
  var handler = self["on" + type];
  if (typeof handler === "function") handler(ev);
};
window.EventSource = FakeEventSource;

window.__stateResponse = {
  stage: "idle", busy: false, funnel: {}, progress: [0,0,0,0], speed_progress: [0,0,0],
  scan_results: [], speed_results: [], log: [], last_error: null, version: "DEV",
  settings: {
    tray_on_close: false, cidr_mode: "仅官方", scan_mode: "tcping", sample_max: 5000,
    workers: 200, latency_threshold: 230, ping_times: 0, speed_url: "auto", min_speed: 0,
    verify_nodes: true, download_interval: 3, speed_workers: 1, speed_result_limit: 0,
    per_region_topn: 0, pre_filter_ports: "", use_remote_sources: false,
    remote_sources: [
      { name: "cfnb 聚合列表", url: "https://zip.cm.edu.kg/all.txt", enabled: true },
      { name: "备用源", url: "https://example.com/all.txt", enabled: false }
    ],
    source_retries: 3, source_retry_delay: 3, source_timeout: 8,
    score_speed_weight: 3.0, score_latency_weight: 3.0, http_enabled: true,
    http_port: 17443, allow_lan: false, http_token: "", http_token_set: false
  }
};
window.__settingsResponse = window.__stateResponse.settings;
window.__fetchLog = [];
window.__nextStatus = 200;
window.fetch = function (url, opts) {
  var u = String(url);
  window.__fetchLog.push({
    url: u, method: (opts && opts.method) || "GET",
    body: (opts && opts.body) || ""
  });
  var status = window.__nextStatus;
  var body;
  if (status === 401) {
    body = { ok: false, error: "unauthorized" };
  } else if (u.indexOf("/api/state") === 0) {
    body = window.__stateResponse;
  } else if (u.indexOf("/api/health") === 0) {
    body = { warnings: [] };
  } else if (u.indexOf("/api/settings") === 0) {
    body = { ok: true, settings: window.__settingsResponse };
  } else if (u.indexOf("/api/sources/preview") === 0) {
    body = { ok: true, count: 12, report: ["数据源 A 解析出 12 条，新增 12 条"],
             preview: ["1.2.3.4:443", "5.6.7.8:8443"] };
  } else {
    body = { ok: true };
  }
  return Promise.resolve({
    ok: status >= 200 && status < 300, status: status,
    headers: { get: function (k) { return k === "Content-Type" ? "application/json" : ""; } },
    json: function () { return Promise.resolve(body); },
    text: function () { return Promise.resolve(JSON.stringify(body)); },
    blob: function () { return Promise.resolve({ size: 1 }); }
  });
};

// 避免 jsdom 尝试真实下载 / 导航
window.URL.createObjectURL = function () { return "blob:fake"; };
window.URL.revokeObjectURL = function () {};
</script>
`;

const virtualConsole = new VirtualConsole();
const jsdomErrors = [];
virtualConsole.on("jsdomError", (e) => jsdomErrors.push("jsdomError: " + e.message));
virtualConsole.on("error", (...a) => jsdomErrors.push("console.error: " + a.join(" ")));

const patched = HTML
  .replace('<link rel="stylesheet" href="/static/style.css">', "")
  .replace('<script src="/static/app.js"></script>',
    PRELUDE + "<script>" + APP + "</scr" + "ipt>");

const dom = new JSDOM(patched, {
  url: "http://127.0.0.1:18099/",
  runScripts: "dangerously",
  pretendToBeVisual: true,
  virtualConsole,
});
const { window } = dom;
const doc = window.document;
const $ = (id) => doc.getElementById(id);
const txt = (id) => ($(id) ? $(id).textContent : null);
const tick = (n = 3) => new Promise((r) => {
  let i = 0;
  const step = () => (++i >= n ? r() : setTimeout(step, 0));
  setTimeout(step, 0);
});

// 阻止 <a>.click() 触发 jsdom 导航告警
window.HTMLAnchorElement.prototype.click = function () {};

const SCAN = [
  { ip: "1.2.3.4", latency: 88.5, iata_code: "HKG", chinese_name: "中国香港", port: 443,
    use_tls: true, scan_mode: "tcping", scan_time: "2026-01-01 00:00:00", ip_version: 4 },
  { ip: "5.6.7.8", latency: 150.0, iata_code: "NRT", chinese_name: "日本", port: 443,
    use_tls: true, scan_mode: "tcping", scan_time: "2026-01-01 00:00:00", ip_version: 4 },
  { ip: "9.9.9.9", latency: 210.0, iata_code: "SIN", chinese_name: "新加坡", port: 443,
    use_tls: true, scan_mode: "tcping", scan_time: "2026-01-01 00:00:00", ip_version: 4 },
];
const SPEED = [
  { ip: "1.2.3.4", latency: 88.5, download_speed: 12.5, score: 9.1, iata_code: "HKG",
    chinese_name: "中国香港", port: 443, test_type: "完全测速", verified: true },
  { ip: "5.6.7.8", latency: 150.0, download_speed: 3.2, score: 1.4, iata_code: "NRT",
    chinese_name: "日本", port: 443, test_type: "完全测速", verified: true },
];

(async function run() {
  await tick(6);

  console.log("\n== 1. 首屏加载 ==");
  check("无 JS 运行时错误", window.__errors.length === 0, window.__errors.join(" | "));
  check("无 jsdom 错误", jsdomErrors.length === 0, jsdomErrors.join(" | "));
  check("页面标题已设置", txt("page-title") === "扫描", txt("page-title"));
  check("副标题已设置", (txt("page-subtitle") || "").length > 0, txt("page-subtitle"));
  check("版本号已填充", txt("app-version") === "vDEV", txt("app-version"));
  check("SSE 已建立", window.__sse !== null && !window.__sse.closed);
  check("SSE 携带 token 参数", /\/api\/events\?token=/.test(window.__sse.url), window.__sse.url);
  check("连接状态点为 online", $("conn-dot").classList.contains("online"));
  check("已拉取 /api/state", window.__fetchLog.some((f) => f.url.indexOf("/api/state") === 0));

  console.log("\n== 2. 设置回填（含新增字段） ==");
  check("扫描页 workers 回填 200", $("in-workers").value === "200", $("in-workers").value);
  check("设置页 speed_workers 回填 1", $("set-speed-workers").value === "1", $("set-speed-workers").value);
  check("设置页 result_limit 回填 0", $("set-result-limit").value === "0", $("set-result-limit").value);
  check("设置页端口回填 17443", $("set-port").value === "17443", $("set-port").value);
  check("Token 输入框留空（不回显）", $("set-token").value === "", $("set-token").value);
  check("Token 提示为「未设置」", /未设置 Token/.test(txt("token-hint")), txt("token-hint"));
  check("扫描方式分段已选中 tcping",
    $("seg-mode").querySelector("span.on").dataset.v === "tcping");
  check("HTTP 开关默认开", $("sw-http").classList.contains("on"));
  check("扫描页端口过滤回填为空", $("in-prefilter").value === "", $("in-prefilter").value);
  check("远程数据源默认关闭", !$("sw-remote").classList.contains("on"));
  check("数据源列表回填 2 行（含 1 条禁用）",
    $("txt-sources").value.split("\n").filter(Boolean).length === 2,
    JSON.stringify($("txt-sources").value));
  check("禁用源以 # 开头",
    /^#\s/.test($("txt-sources").value.split("\n")[1] || ""),
    $("txt-sources").value);
  check("设置页分地区 TopN 回填 0", $("set-region-topn").value === "0", $("set-region-topn").value);
  check("测速地址下拉已填充预设", $("set-speed-url-mode").options.length === 5,
    $("set-speed-url-mode").options.length);
  check("测速页测速地址下拉已填充预设", $("sel-speed-url").options.length === 5,
    $("sel-speed-url").options.length);
  check("默认选中「自动选择」", $("set-speed-url-mode").value === "auto",
    $("set-speed-url-mode").value);

  console.log("\n== 3. 来源模式切换显隐 ==");
  check("默认（仅官方）隐藏自定义输入区", $("wrap-custom").hidden === true);
  check("默认显示「仅官方」说明", $("source-official").hidden === false);
  $("sel-source").value = "仅自定义";
  $("sel-source").dispatchEvent(new window.Event("change"));
  check("选仅自定义后显示自定义输入区", !$("wrap-custom").hidden);
  check("选仅自定义后隐藏官方说明", $("source-official").hidden === true);
  $("txt-source").value = "104.16.0.0/24\n1.1.1.1\n1.0.0.1:8443\n104.16.0.10-104.16.0.14\ncloudflare.com";
  $("txt-source").dispatchEvent(new window.Event("input"));
  check("解析预览识别 1 个网段", /1 个网段/.test(txt("source-preview")), txt("source-preview"));
  check("解析预览识别 2 个直测节点", /2 个节点/.test(txt("source-preview")), txt("source-preview"));
  check("解析预览识别 1 个 IP 段", /1 个 IP 段/.test(txt("source-preview")), txt("source-preview"));
  check("解析预览识别 1 个域名", /1 个域名/.test(txt("source-preview")), txt("source-preview"));
  $("btn-source-clear").dispatchEvent(new window.Event("click"));
  check("清空后预览提示重新输入", /等待输入/.test(txt("source-preview")), txt("source-preview"));
  $("btn-source-sample").dispatchEvent(new window.Event("click"));
  check("填入示例后有内容", $("txt-source").value.length > 0);
  $("sel-source").value = "官方+自定义";
  $("sel-source").dispatchEvent(new window.Event("change"));
  check("选官方+自定义后仍显示自定义输入区", !$("wrap-custom").hidden);
  $("sel-source").value = "仅官方";
  $("sel-source").dispatchEvent(new window.Event("change"));

  console.log("\n== 3b. 远程数据源拉取预览 ==");
  const beforePreview = window.__fetchLog.length;
  $("btn-source-preview").dispatchEvent(new window.Event("click"));
  await tick(4);
  const previewCall = window.__fetchLog.slice(beforePreview).find(
    (f) => f.url.indexOf("/api/sources/preview") === 0);
  check("触发 POST /api/sources/preview", !!previewCall,
    JSON.stringify(window.__fetchLog.slice(beforePreview)));
  check("预览请求只带启用中的源",
    previewCall && JSON.parse(previewCall.body).remote_sources.length === 1,
    previewCall ? previewCall.body : "");
  check("预览结果写入页面", /合计 12 条/.test(txt("sources-preview")), txt("sources-preview"));

  console.log("\n== 4. SSE state 快照 -> 统计卡与表格 ==");
  const rows = () => doc.querySelectorAll("#scan-tbody tr").length;
  window.__sse._emit("state", Object.assign({}, window.__stateResponse, {
    scan_results: SCAN, stage: "idle",
  }));
  await tick();
  check("可用 IP 统计 = 3", txt("rstat-total") === "3", txt("rstat-total"));
  check("覆盖地区统计 = 3", txt("rstat-regions") === "3", txt("rstat-regions"));
  check("最低延迟 = 88.5ms", txt("rstat-min") === "88.5ms", txt("rstat-min"));
  check("平均延迟已计算", /^\d+\.\dms$/.test(txt("rstat-avg")), txt("rstat-avg"));
  // 默认勾选「仅显示延迟 < 200ms」，210ms 那条应被隐藏（这是设计行为）
  check("默认延迟筛选隐藏 210ms 行（显示 2 行）", rows() === 2, rows());
  check("地区芯片不受筛选影响（3 个）",
    doc.querySelectorAll("#region-chips .chip").length === 3,
    doc.querySelectorAll("#region-chips .chip").length);
  check("侧栏结果计数已更新", /扫描 3/.test(txt("speed-label")), txt("speed-label"));
  // 关闭延迟筛选，后续按全量断言
  $("chk-latency").checked = false;
  $("chk-latency").dispatchEvent(new window.Event("change"));
  await tick();
  check("关闭延迟筛选后显示 3 行", rows() === 3, rows());

  console.log("\n== 5. 地区芯片 全选 / 清空 ==");
  $("btn-chips-all").dispatchEvent(new window.Event("click"));
  await tick();
  check("全选后 3 个芯片高亮", doc.querySelectorAll("#region-chips .chip.on").length === 3,
    doc.querySelectorAll("#region-chips .chip.on").length);
  check("全选后表格仍 3 行", rows() === 3, rows());
  $("btn-chips-none").dispatchEvent(new window.Event("click"));
  await tick();
  check("清空后 0 个芯片高亮", doc.querySelectorAll("#region-chips .chip.on").length === 0);
  // 只选一个地区 -> 表格应过滤
  doc.querySelectorAll("#region-chips .chip")[0].dispatchEvent(new window.Event("click"));
  await tick();
  check("单地区过滤后表格 1 行", rows() === 1, rows());
  $("btn-chips-none").dispatchEvent(new window.Event("click"));
  await tick();
  check("再次清空后恢复 3 行", rows() === 3, rows());

  console.log("\n== 6. 勾选全部 / 清空勾选 ==");
  $("btn-check-all").dispatchEvent(new window.Event("click"));
  check("勾选全部 -> 3 个复选框选中",
    doc.querySelectorAll("#scan-tbody input:checked").length === 3,
    doc.querySelectorAll("#scan-tbody input:checked").length);
  $("btn-check-none").dispatchEvent(new window.Event("click"));
  check("清空勾选 -> 0 个复选框选中",
    doc.querySelectorAll("#scan-tbody input:checked").length === 0);

  console.log("\n== 7. 延迟筛选 ==");
  $("in-latency").value = "100";
  $("chk-latency").checked = true;
  $("in-latency").dispatchEvent(new window.Event("input"));
  await tick();
  check("阈值 100ms 过滤出 1 行", rows() === 1, rows());
  check("统计卡最低延迟随筛选更新", txt("rstat-min") === "88.5ms", txt("rstat-min"));
  $("chk-latency").checked = false;
  $("chk-latency").dispatchEvent(new window.Event("change"));
  await tick();
  check("取消筛选恢复 3 行", rows() === 3, rows());

  console.log("\n== 8. 导出弹窗（新增 TXT 格式 + 兜底） ==");
  $("btn-export-scan").dispatchEvent(new window.Event("click"));
  check("导出弹窗已打开", $("modal-backdrop").hidden === false);
  const fmts = [...doc.querySelectorAll('input[name="exfmt"]')].map((r) => r.value);
  check("格式含 csv/json/txt", fmts.join(",") === "csv,json,txt", fmts.join(","));
  const exportBtn = [...doc.querySelectorAll("#modal-footer button")].pop();
  const before = window.__fetchLog.length;
  exportBtn.dispatchEvent(new window.Event("click"));
  await tick(4);
  const dl = window.__fetchLog.slice(before).map((f) => f.url).join(" ");
  check("点击导出触发 /api/export 下载", /\/api\/export\?type=scan/.test(dl), dl);
  check("导出请求带 token 参数", /token=/.test(dl), dl);

  console.log("\n== 9. 测速完成 -> 统计卡与排名 ==");
  window.__sse._emit("speed_done", SPEED);
  await tick();
  check("最快下载 = 12.50MB/s", txt("sstat-best") === "12.50MB/s", txt("sstat-best"));
  check("平均下载 = 7.85MB/s", txt("sstat-avg") === "7.85MB/s", txt("sstat-avg"));
  check("结果总数 = 2", txt("sstat-count") === "2", txt("sstat-count"));
  check("合格节点 = 2（阈值 0）", txt("sstat-pass") === "2", txt("sstat-pass"));
  check("测速表 2 行", doc.querySelectorAll("#speed-tbody tr").length === 2,
    doc.querySelectorAll("#speed-tbody tr").length);
  check("已跳转到测速页", $("page-speed").classList.contains("show"));
  check("排名第 1 行有 rank1 样式",
    doc.querySelector("#speed-tbody tr td").classList.contains("rank1"));

  console.log("\n== 10. 测速中止语义（缺陷 3.2） ==");
  window.__stateResponse.stage = "testing";
  window.__sse._emit("state", Object.assign({}, window.__stateResponse, { stage: "testing" }));
  await tick();
  check("测速中状态为 busy", txt("status-pill") === "测速中…", txt("status-pill"));
  window.__stateResponse.stage = "idle";
  window.__sse._emit("speed_abort", null);
  await tick();
  check("中止后状态为「已停止」", txt("status-pill") === "已停止", txt("status-pill"));
  check("中止不产生「完成」提示", !/完成/.test(txt("status-pill")), txt("status-pill"));
  const toasts = [...doc.querySelectorAll("#toasts .toast")].map((t) => t.textContent);
  check("中止弹出提示", toasts.some((t) => /已停止/.test(t)), toasts.join(" | "));

  console.log("\n== 11. 测速页无结果时导出兜底（缺陷 3.9） ==");
  window.__sse._emit("state", Object.assign({}, window.__stateResponse, {
    scan_results: SCAN, speed_results: [],
  }));
  await tick();
  check("清空后测速统计归零", txt("sstat-count") === "0", txt("sstat-count"));
  const beforeFallback = window.__fetchLog.length;
  $("btn-export-speed").dispatchEvent(new window.Event("click"));
  await tick(3);
  const fallbackToasts = [...doc.querySelectorAll("#toasts .toast")].map((t) => t.textContent);
  check("无测速结果时提示已回退导出扫描",
    fallbackToasts.some((t) => /改为导出扫描结果/.test(t)), fallbackToasts.join(" | "));
  const expBtn2 = [...doc.querySelectorAll("#modal-footer button")].pop();
  expBtn2.dispatchEvent(new window.Event("click"));
  await tick(4);
  const dl2 = window.__fetchLog.slice(beforeFallback).map((f) => f.url).join(" ");
  check("回退后实际导出 type=scan", /\/api\/export\?type=scan/.test(dl2), dl2);
  check("未请求 type=speed（避免 404）", !/type=speed/.test(dl2), dl2);

  console.log("\n== 12. 设置同步（缺陷 3.3） ==");
  window.__sse._emit("settings", {
    tray_on_close: false, cidr_mode: "仅官方", scan_mode: "httping", sample_max: 7777,
    workers: 150, latency_threshold: 180, ping_times: 2, speed_url: "auto", min_speed: 6.5,
    verify_nodes: false, download_interval: 2, speed_workers: 3, speed_result_limit: 40,
    per_region_topn: 5, pre_filter_ports: "443,8443", use_remote_sources: true,
    remote_sources: [{ name: "S", url: "https://s.example/all.txt", enabled: true }],
    source_retries: 4, source_retry_delay: 2, source_timeout: 6,
    score_speed_weight: 4.0, score_latency_weight: 2.0, http_enabled: true,
    http_port: 18099, allow_lan: true, http_token: "", http_token_set: true,
  });
  await tick();
  check("同步后扫描页 sample_max = 7777", $("in-sample").value === "7777", $("in-sample").value);
  check("同步后扫描方式切到 httping",
    $("seg-mode").querySelector("span.on").dataset.v === "httping");
  check("同步后测速并发 = 3", $("set-speed-workers").value === "3", $("set-speed-workers").value);
  check("同步后提前收敛 = 40", $("set-result-limit").value === "40", $("set-result-limit").value);
  check("同步后分地区 TopN = 5", $("set-region-topn").value === "5", $("set-region-topn").value);
  check("同步后端口过滤 = 443,8443", $("in-prefilter").value === "443,8443",
    $("in-prefilter").value);
  check("同步后远程数据源开关打开", $("sw-remote").classList.contains("on"));
  check("同步后数据源列表刷新", /s\.example/.test($("txt-sources").value), $("txt-sources").value);
  check("同步后重试次数 = 4", $("in-retries").value === "4", $("in-retries").value);
  check("同步后 Token 提示为「已设置」", /已设置 Token/.test(txt("token-hint")), txt("token-hint"));
  check("同步后 Token 输入框仍为空（不回显）", $("set-token").value === "");
  check("同步后测速页最低速度 = 6.5", $("in-minspeed").value === "6.5", $("in-minspeed"));
  check("同步后自动勾选「隐藏低于」", $("chk-minspeed").checked === true);

  console.log("\n== 12b. 测速地址预设切换 ==");
  check("同步后测速地址预设为「自动」", $("set-speed-url-mode").value === "auto",
    $("set-speed-url-mode").value);
  $("set-speed-url-mode").value = "speed.cloudflare.com/__down?bytes=99999999";
  $("set-speed-url-mode").dispatchEvent(new window.Event("change"));
  await tick(2);
  check("选固定预设后隐藏自定义地址框", $("row-speed-url").hidden === true);
  check("预设提示文案已更新", /固定测速地址/.test(txt("speed-url-hint")), txt("speed-url-hint"));
  $("set-speed-url-mode").value = "__custom__";
  $("set-speed-url-mode").dispatchEvent(new window.Event("change"));
  await tick(2);
  check("选「自定义…」后显示地址框", $("row-speed-url").hidden === false);
  check("自定义框预填默认地址",
    $("set-speed-url").value === "speed.cloudflare.com/__down?bytes=99999999",
    $("set-speed-url").value);
  $("sel-speed-url").value = "__custom__";
  $("sel-speed-url").dispatchEvent(new window.Event("change"));
  check("测速页选「自定义…」后显示地址框", $("in-speed-url").hidden === false);

  console.log("\n== 13. 保存设置提交的字段 ==");
  const beforePut = window.__fetchLog.length;
  $("btn-set-save").dispatchEvent(new window.Event("click"));
  await tick(4);
  const putCall = window.__fetchLog.slice(beforePut).find((f) => f.method === "PUT");
  check("保存触发 PUT /api/settings", !!putCall, JSON.stringify(window.__fetchLog.slice(beforePut)));
  check("保存后弹出成功提示",
    [...doc.querySelectorAll("#toasts .toast")].some((t) => /设置已保存/.test(t.textContent)));
  const sent = putCall ? JSON.parse(putCall.body) : {};
  check("提交含 per_region_topn", sent.per_region_topn === 5, JSON.stringify(sent.per_region_topn));
  check("提交含 pre_filter_ports", sent.pre_filter_ports === "443,8443", sent.pre_filter_ports);
  check("提交含 use_remote_sources", sent.use_remote_sources === true, sent.use_remote_sources);
  check("提交含 remote_sources（数组）",
    Array.isArray(sent.remote_sources) && sent.remote_sources.length === 1,
    JSON.stringify(sent.remote_sources));
  check("提交含 source_retries", sent.source_retries === 4, sent.source_retries);
  check("提交的 speed_url 为自定义地址",
    sent.speed_url === "speed.cloudflare.com/__down?bytes=99999999", sent.speed_url);

  console.log("\n== 14. 页面导航标题 ==");
  for (const [page, title] of [["result", "扫描结果"], ["speed", "测速结果"],
                               ["history", "历史记录"], ["settings", "设置"], ["scan", "扫描"]]) {
    const nav = doc.querySelector('.nav-item[data-page="' + page + '"]');
    nav.dispatchEvent(new window.Event("click"));
    await tick(2);
    check("切到 " + page + " 标题正确", txt("page-title") === title, txt("page-title"));
    check("切到 " + page + " 副标题非空", (txt("page-subtitle") || "").length > 0);
    check("切到 " + page + " 页面可见", $("page-" + page).classList.contains("show"));
  }

  console.log("\n== 15. Token 鉴权提示（401 流程） ==");
  window.__nextStatus = 401;
  // 不能 await：401 时 api() 会停在「等待用户输入 Token」的弹窗上
  window.eval("api('/api/state').catch(function(){});");
  await tick(4);
  check("401 弹出 Token 输入弹窗", $("modal-backdrop").hidden === false);
  check("弹窗内含 token 输入框", !!$("auth-token"));
  const authBtn = [...doc.querySelectorAll("#modal-footer button")].pop();
  authBtn.dispatchEvent(new window.Event("click"));
  await tick(4);
  check("未填 Token 时请求被拒绝（不崩）", true);
  window.__nextStatus = 200;
  window.eval("api('/api/state').catch(function(){});");
  await tick(4);
  check("恢复后请求重新可用", true);

  console.log("\n== 16. 全程无 JS 错误 ==");
  check("累计无 JS 运行时错误", window.__errors.length === 0, window.__errors.join(" | "));
  check("累计无 jsdom 错误", jsdomErrors.length === 0, jsdomErrors.join(" | "));

  dom.window.close();

  console.log("\n" + "=".repeat(56));
  console.log("通过 " + PASS.length + " / 失败 " + FAIL.length);
  if (FAIL.length) {
    console.log("失败项: " + FAIL.join(", "));
    process.exit(1);
  }
  console.log("全部通过 ✓");
})().catch((e) => {
  console.error("测试脚本异常:", e);
  process.exit(1);
});
