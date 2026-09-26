/* CloudTrace Web 面板逻辑（与桌面版共享同一任务状态） */
"use strict";

/* ================= 基础 ================= */
let token = sessionStorage.getItem("ct_token") || "";
let state = {
  stage: "idle", busy: false, funnel: {}, progress: [0, 0, 0, 0],
  speed_progress: [0, 0, 0], scan_results: [], speed_results: [],
  log: [], last_error: null, version: "?", settings: {},
};
let scanResults = [];
let speedResults = [];
let selectedChips = new Set();
/* 结果表勾选集合：renderResult 用 innerHTML 重建表格，必须靠它恢复勾选，
   否则任何一次 state 事件（进度/阶段变化）都会把用户勾选清空。 */
let checkedIps = new Set();
let settingsLoaded = false;
let clearToken = false;        // 用户在设置页点了「清除 Token」
let es = null;
let lastDoneNote = "";

const DEFAULT_SETTINGS = {
  tray_on_close: false, cidr_mode: "仅官方", scan_mode: "tcping", sample_max: 5000,
  workers: 200, latency_threshold: 230, ping_times: 0,
  pre_filter_ports: "", use_remote_sources: false,
  remote_sources: [
    { name: "cfnb 聚合列表", url: "https://zip.cm.edu.kg/all.txt", enabled: true },
    { name: "countrymerge 聚合列表", url: "https://countrymerge.pages.dev/all.txt", enabled: true },
  ],
  source_retries: 3, source_retry_delay: 3.0, source_timeout: 8.0,
  speed_url: "auto", min_speed: 0, verify_nodes: true, download_interval: 3,
  speed_workers: 1, speed_result_limit: 0, per_region_topn: 0,
  score_speed_weight: 3.0, score_latency_weight: 3.0,
  http_enabled: true, http_port: 17443, allow_lan: false,
  http_token: "", http_token_set: false,
};

/* 测速地址预设（与后端 core/speed_url.py 的 SPEED_URL_PRESETS 保持一致） */
const CF_SPEED_URL = "speed.cloudflare.com/__down?bytes=99999999";
const CUSTOM_SPEED_URL = "__custom__";
const SPEED_URL_PRESETS = [
  { label: "自动选择（按出口 ISP 智能切换）", value: "auto" },
  { label: "Cloudflare 官方", value: CF_SPEED_URL },
  { label: "移动友好 (cf.090227.xyz)", value: "cf.090227.xyz/__down?bytes=99999999" },
  { label: "移动专属 (speed.okl.abrdns.com)", value: "speed.okl.abrdns.com" },
  { label: "自定义…", value: CUSTOM_SPEED_URL },
];

function fillSpeedUrlSelect(sel) {
  if (!sel) return;
  sel.innerHTML = SPEED_URL_PRESETS
    .map(p => `<option value="${p.value}">${p.label}</option>`).join("");
}

/* 已保存地址 → 预设值（不在预设里则落到「自定义…」） */
function speedUrlToPreset(url) {
  const text = (url || "").trim();
  if (!text || text.toLowerCase() === "auto" || text === "自动选择") return "auto";
  return SPEED_URL_PRESETS.some(p => p.value === text) ? text : CUSTOM_SPEED_URL;
}

const SOURCE_SAMPLE =
  "# CIDR（采样）\n104.16.0.0/24\n" +
  "# 单个 IP / 端口\n1.1.1.1\n1.0.0.1:8443\n" +
  "# IP 段\n104.16.0.10-104.16.0.40\n" +
  "# 域名\ncloudflare.com\n";

const SCAN_FIELDS = {
  ip: "IP地址", iata_code: "地区码", chinese_name: "地区", latency: "延迟(ms)",
  ip_version: "IP版本", port: "端口", use_tls: "TLS", scan_mode: "扫描方式",
  scan_time: "扫描时间",
};
const SPEED_FIELDS = {
  ip: "IP地址", iata_code: "地区码", chinese_name: "地区", latency: "延迟(ms)",
  download_speed: "下载速度(MB/s)", score: "综合评分", verified: "可用性验证",
  port: "端口", use_tls: "TLS", test_type: "测速类型",
};

/* ================= 轻提示 ================= */
function toast(msg, type) {
  const box = document.getElementById("toasts");
  const el = document.createElement("div");
  el.className = "toast" + (type ? " " + type : "");
  el.textContent = msg;
  box.appendChild(el);
  setTimeout(() => {
    el.classList.add("out");
    setTimeout(() => el.remove(), 280);
  }, 2600);
}

/* ================= 网络 ================= */
function askToken() {
  return new Promise(resolve => {
    const body =
      `<div class="sec"><div class="sec-title">该面板已启用 Token 鉴权，请输入访问 Token</div>
       <input type="password" id="auth-token" placeholder="访问 Token"
        style="width:100%;border:1px solid #D5DBE3;border-radius:7px;padding:8px 10px;font-family:inherit;outline:none"></div>`;
    showModal("需要授权", body, [
      { label: "取消", cls: "btn-ghost", onClick: () => resolve("") },
      { label: "确定", cls: "btn-primary", onClick: () => {
          resolve((document.getElementById("auth-token").value || "").trim());
        } },
    ]);
    const inp = document.getElementById("auth-token");
    inp.focus();
    inp.addEventListener("keydown", e => {
      if (e.key === "Enter") { hideModal(); resolve(inp.value.trim()); }
    });
  });
}

async function api(path, opts = {}) {
  const headers = Object.assign({ "Content-Type": "application/json" }, opts.headers || {});
  if (token) headers["X-Token"] = token;
  const resp = await fetch(path, Object.assign({}, opts, { headers }));
  if (resp.status === 401) {
    const t = await askToken();
    if (t) {
      token = t;
      sessionStorage.setItem("ct_token", token);
      connectSSE();
      return api(path, opts);
    }
    throw new Error("未授权");
  }
  if (!resp.ok) {
    let msg = "HTTP " + resp.status;
    try { const j = await resp.json(); if (j.error) msg = j.error; } catch (e) {}
    throw new Error(msg);
  }
  const ct = resp.headers.get("Content-Type") || "";
  if (ct.includes("json")) return resp.json();
  return resp;
}

/* 用 fetch + Blob 下载：能捕获 404/409 并给出提示，
   而不是让浏览器直接跳转到一个空白错误页（原实现的问题）。 */
async function downloadURL(path) {
  const sep = path.includes("?") ? "&" : "?";
  const url = path + sep + "token=" + encodeURIComponent(token);
  let resp;
  try {
    resp = await fetch(url);
  } catch (err) {
    toast("导出失败: " + err.message, "err");
    return;
  }
  if (!resp.ok) {
    let msg = "HTTP " + resp.status;
    try { const j = await resp.json(); if (j.error) msg = j.error; } catch (e) {}
    toast("导出失败: " + msg, "err");
    return;
  }
  const blob = await resp.blob();
  const cd = resp.headers.get("Content-Disposition") || "";
  const m = /filename="?([^";]+)"?/.exec(cd);
  const name = m ? m[1] : "cloudtrace_export";
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(a.href), 4000);
  toast("已开始下载 " + name, "ok");
}

/* ================= 弹窗 ================= */
function showModal(title, bodyHTML, buttons) {
  document.getElementById("modal-title").textContent = title;
  document.getElementById("modal-body").innerHTML = bodyHTML;
  const footer = document.getElementById("modal-footer");
  footer.innerHTML = "";
  (buttons || [{ label: "确定", cls: "btn-primary" }]).forEach(b => {
    const btn = document.createElement("button");
    btn.className = "btn " + (b.cls || "btn-ghost");
    btn.textContent = b.label;
    btn.onclick = () => {
      if (b.onClick && b.onClick() === false) return;
      hideModal();
    };
    footer.appendChild(btn);
  });
  document.getElementById("modal-backdrop").hidden = false;
}
function hideModal() { document.getElementById("modal-backdrop").hidden = true; }
function showInfo(msg) { toast(msg, "ok"); }
function showWarn(msg) { toast(msg, "warn"); }
function showError(msg) { toast(msg, "err"); }
function confirmDialog(title, msg, onYes) {
  showModal(title, "<div>" + msg + "</div>", [
    { label: "取消", cls: "btn-ghost" },
    { label: "确认", cls: "btn-red", onClick: onYes },
  ]);
}
document.getElementById("modal-backdrop").addEventListener("click", e => {
  if (e.target.id === "modal-backdrop") hideModal();
});
document.addEventListener("keydown", e => {
  if (e.key === "Escape" && !document.getElementById("modal-backdrop").hidden) hideModal();
});

/* ================= 页面导航 ================= */
const PAGES = ["scan", "result", "speed", "history", "settings"];
const PAGE_META = {
  scan: ["扫描", "配置参数并扫描 Cloudflare IP 段"],
  result: ["扫描结果", "查看、筛选与批量测速"],
  speed: ["测速结果", "下载实测与综合评分排名"],
  history: ["历史记录", "加载或导出历史存档"],
  settings: ["设置", "面板与扫描/测速默认值"],
};
let currentPage = "scan";

function setPage(name) {
  currentPage = name;
  PAGES.forEach(p => {
    document.getElementById("page-" + p).classList.toggle("show", p === name);
  });
  document.querySelectorAll(".nav-item").forEach(el =>
    el.classList.toggle("active", el.dataset.page === name));
  document.getElementById("page-title").textContent = PAGE_META[name][0];
  document.getElementById("page-subtitle").textContent = PAGE_META[name][1];
  renderCTA();
  if (name === "history") loadHistory();
  if (name === "result") { renderResultStats(); renderResult(); }
  if (name === "speed") { renderSpeedStats(); renderSpeed(); }
}
document.querySelectorAll(".nav-item").forEach(el => {
  el.addEventListener("click", () => setPage(el.dataset.page));
});

const CTA_ACTIONS = {
  scan: () => startScan(),
  result: () => {
    if (selectedChips.size > 0) startSpeed("region");
    else startSpeed("all");
  },
  speed: () => openExport("speed"),
  history: () => setPage("scan"),
  settings: () => saveSettings(),
};
function renderCTA() {
  const btn = document.getElementById("btn-cta");
  const map = {
    scan: ["▶ 开始扫描", "btn-primary"], result: ["🚀 批量测速", "btn-orange"],
    speed: ["⬇ 导出结果", "btn-green"], history: ["🔄 前往扫描", "btn-primary"],
    settings: ["💾 保存设置", "btn-primary"],
  };
  const [label, cls] = map[currentPage];
  btn.textContent = label;
  btn.className = "btn " + cls;
  btn.disabled = busy();
}
document.getElementById("btn-cta").addEventListener("click", () => CTA_ACTIONS[currentPage]());

/* ================= 状态渲染 ================= */
function busy() { return state.stage !== "idle"; }

function renderStatus() {
  const pill = document.getElementById("status-pill");
  const stopBtn = document.getElementById("btn-stop");
  const cta = document.getElementById("btn-cta");
  let text = "就绪", cls = "";
  if (state.stage === "scanning") {
    const [c, t] = state.progress;
    text = t ? `扫描 ${c}/${t}` : "扫描中…"; cls = "run";
  } else if (state.stage === "testing") {
    const [c, t] = state.speed_progress;
    text = t ? `测速 ${c}/${t}` : "测速中…"; cls = "busy";
  } else if (lastDoneNote) {
    text = lastDoneNote; cls = "run";
  }
  if (state.last_error && state.stage === "idle") { text = "错误"; cls = "error"; }
  pill.textContent = text;
  pill.className = "pill " + cls;
  stopBtn.disabled = !busy();
  cta.disabled = busy();

  const p = state.stage === "testing" ? state.speed_progress : state.progress;
  const pct = p[1] ? Math.round(p[0] / p[1] * 100) : 0;
  document.getElementById("progress-bar").style.width =
    (busy() ? pct : (lastDoneNote ? 100 : 0)) + "%";

  document.getElementById("speed-label").textContent =
    state.stage === "scanning"
      ? `速度: ${Number(state.progress[3] || 0).toFixed(0)} IP/s | 成功: ${state.progress[2]}`
      : `结果: 扫描 ${scanResults.length} · 测速 ${speedResults.length}`;
}

function appendLog(msg) {
  const stamp = new Date().toTimeString().slice(0, 8);
  ["scan-log", "speed-log"].forEach(id => {
    const el = document.getElementById(id);
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 30;
    el.textContent += `[${stamp}] ${msg}\n`;
    const lines = el.textContent.split("\n");
    if (lines.length > 2000) el.textContent = lines.slice(-2000).join("\n");
    if (atBottom) el.scrollTop = el.scrollHeight;
  });
}

function renderFunnel(el, steps) {
  el.innerHTML = steps.map((s, i) =>
    (i ? '<span class="arr">→</span>' : "") +
    `<span class="step">${s[0]} <b>${s[1]}</b></span>`).join("") ||
    '<span class="step">等待开始</span>';
}

function funnelSteps(funnel) {
  const steps = [];
  if (funnel && funnel.generated) {
    steps.push(["生成", funnel.generated]);
    steps.push(["延迟达标", funnel.latency_ok || 0]);
    steps.push(["地区解析", funnel.with_iata || 0]);
  }
  steps.push(["可用", scanResults.length]);
  return steps;
}

function setStat(id, val, unit) {
  const el = document.getElementById(id);
  if (!el) return;
  el.innerHTML = val + (unit ? `<i>${unit}</i>` : "");
}

/* ================= SSE ================= */
function setConn(online) {
  const d = document.getElementById("conn-dot");
  d.classList.toggle("online", !!online);
  d.classList.toggle("offline", !online);
  d.title = online ? "实时连接正常" : "实时连接断开，正在重连…";
}

function connectSSE() {
  if (es) { es.close(); es = null; }
  es = new EventSource("/api/events?token=" + encodeURIComponent(token));
  es.onopen = () => setConn(true);
  es.addEventListener("state", e => applyState(JSON.parse(e.data)));
  es.addEventListener("log", e => appendLog(JSON.parse(e.data)));
  es.addEventListener("progress", e => {
    state.progress = JSON.parse(e.data); renderStatus();
    renderFunnel(document.getElementById("scan-funnel"), funnelSteps(state.funnel));
  });
  es.addEventListener("funnel", e => {
    state.funnel = JSON.parse(e.data);
    renderFunnel(document.getElementById("scan-funnel"), funnelSteps(state.funnel));
  });
  es.addEventListener("speed_progress", e => {
    state.speed_progress = JSON.parse(e.data); renderStatus();
  });
  es.addEventListener("scan_done", e => {
    const results = JSON.parse(e.data);
    state.stage = "idle";
    if (results) {
      scanResults = results;
      checkedIps = new Set();
      selectedChips = new Set();
      lastDoneNote = `完成 · ${results.length} IP`;
      renderResultStats();
      renderResult();
      renderStatus();
      setPage("result");
      appendLog(`✅ 扫描完成: ${results.length} 个可用IP，已存入历史`);
      toast(`扫描完成，共 ${results.length} 个可用 IP`, "ok");
    } else {
      lastDoneNote = "已停止";
      renderStatus();
      appendLog("⏹ 扫描已中止（未写入历史）");
      toast("扫描已停止", "warn");
    }
  });
  es.addEventListener("speed_done", e => {
    const results = JSON.parse(e.data);
    state.stage = "idle";
    speedResults = results || [];
    if (speedResults.length) {
      const best = speedResults[0];
      lastDoneNote = `完成 · 最快 ${best.download_speed} MB/s`;
      appendLog(`✅ 测速完成: ${speedResults.length} 条结果，最优 ${best.ip} (${best.download_speed} MB/s, 评分 ${best.score})`);
      toast(`测速完成，${speedResults.length} 条结果`, "ok");
    } else {
      lastDoneNote = "测速完成（无结果）";
      appendLog("测速完成：没有达到阈值的节点");
      toast("测速完成，但没有合格结果", "warn");
    }
    renderSpeedStats();
    renderSpeed();
    renderStatus();
    setPage("speed");
  });
  /* 停止测速是「中止」而非「完成」：不能覆盖提示，也不能写历史 */
  es.addEventListener("speed_abort", () => {
    state.stage = "idle";
    lastDoneNote = "已停止";
    appendLog("⏹ 测速已停止（未写入历史）");
    renderStatus();
    toast("测速已停止", "warn");
  });
  /* 桌面端或另一端改了设置 → 面板同步回填 */
  es.addEventListener("settings", e => {
    if (settingsFormFocused()) return;   // 用户正在编辑，别抢输入焦点
    applySettingsToUI(JSON.parse(e.data));
    toast("设置已同步", "ok");
  });
  es.onerror = () => {
    setConn(false);   // EventSource 会自动重连
  };
}

function applyState(s) {
  Object.assign(state, s);
  document.getElementById("app-version").textContent = "v" + (s.version || state.version || "?");
  // 注意：EV_STATE 可能是「完整快照」，也可能是仅含 stage 的局部事件。
  // 只有快照里确实带上结果数组时才覆盖，否则会把已扫描/已测速的结果清空。
  if (Array.isArray(s.scan_results)) scanResults = s.scan_results;
  if (Array.isArray(s.speed_results)) speedResults = s.speed_results;
  // 芯片选择保留交集
  const codes = new Set(regionCodes());
  selectedChips = new Set([...selectedChips].filter(c => codes.has(c)));
  // 勾选只保留当前结果里仍存在的 IP，避免残留到下一批数据
  const ips = new Set(scanResults.map(r => r.ip));
  checkedIps = new Set([...checkedIps].filter(ip => ips.has(ip)));

  renderResultStats();
  renderResult();
  renderSpeedStats();
  renderSpeed();
  renderStatus();
  renderFunnel(document.getElementById("scan-funnel"), funnelSteps(state.funnel));

  if (!settingsLoaded && s.settings) {
    applySettingsToUI(s.settings);
    settingsLoaded = true;
  }
}

/* ================= 扫描 ================= */
function bindSeg(el, cb) {
  el.querySelectorAll("span").forEach(s => {
    s.addEventListener("click", () => {
      el.querySelectorAll("span").forEach(x => x.classList.remove("on"));
      s.classList.add("on");
      if (cb) cb(s.dataset.v);
    });
  });
}
function segVal(el) { return el.querySelector("span.on").dataset.v; }
function setSeg(el, v) {
  el.querySelectorAll("span").forEach(x => x.classList.toggle("on", x.dataset.v === String(v)));
}

function onSourceChange() {
  const mode = document.getElementById("sel-source").value;
  const isCustom = mode === "仅自定义" || mode === "官方+自定义";
  document.getElementById("wrap-custom").hidden = !isCustom;
  document.getElementById("source-official").hidden = isCustom;
  if (isCustom) updateSourcePreview();
}
document.getElementById("sel-source").addEventListener("change", onSourceChange);

function isIpLiteral(host) {
  if (!host) return false;
  if (host.includes(":")) return /^[0-9a-fA-F:]+$/.test(host) && host.split(":").length >= 3;
  const parts = host.split(".");
  if (parts.length !== 4) return false;
  return parts.every(p => /^\d{1,3}$/.test(p) && Number(p) <= 255);
}

/* 与服务端 core/importer.parse_source_text 的分类规则保持一致的轻量预览 */
function classifySourceLine(line) {
  const head = line.split(/\s+/)[0].replace(/^https?:\/\//i, "");
  const slash = head.indexOf("/");
  if (slash > 0 && /\/\d{1,3}$/.test(head) && /^[0-9a-fA-F:.]+$/.test(head.slice(0, slash))) return "cidr";
  const noPath = head.split("/")[0];
  const rng = noPath.match(/^(.+?)[-~](.+)$/);
  if (rng && /^[0-9a-fA-F:.]+$/.test(rng[1]) && /^[0-9a-fA-F:.]+$/.test(rng[2])) return "range";
  let host = noPath;
  if (host.startsWith("[")) {
    const end = host.indexOf("]");
    host = end > 0 ? host.slice(1, end) : host.slice(1);
  } else if ((host.match(/:/g) || []).length === 1) {
    host = host.split(":")[0];
  }
  if (isIpLiteral(host)) return "entry";
  if (/^[A-Za-z0-9_](?:[A-Za-z0-9_-]*[A-Za-z0-9_])?(\.[A-Za-z0-9_](?:[A-Za-z0-9_-]*[A-Za-z0-9_])?)+$/.test(host)) return "domain";
  return "bad";
}

function updateSourcePreview() {
  const el = document.getElementById("source-preview");
  const text = document.getElementById("txt-source").value.trim();
  if (!text) {
    el.textContent = "等待输入：CIDR / 单个 IP / IP 段 / 域名";
    return;
  }
  const count = { cidr: 0, entry: 0, domain: 0, range: 0, bad: 0 };
  for (const rawLine of text.split("\n")) {
    let line = rawLine.trim();
    if (!line || line.startsWith("#")) continue;
    if (line.includes("#")) line = line.split("#")[0].trim();
    if (!line) continue;
    count[classifySourceLine(line)]++;
  }
  const parts = [];
  if (count.cidr) parts.push(`${count.cidr} 个网段（采样）`);
  if (count.entry) parts.push(`${count.entry} 个节点（直测）`);
  if (count.domain) parts.push(`含 ${count.domain} 个域名`);
  if (count.range) parts.push(`${count.range} 个 IP 段`);
  el.textContent = "解析预览：" + (parts.join("，") || "未识别到任何有效内容")
    + (count.bad ? `　⚠ ${count.bad} 行格式可疑（扫描时会给出详细提示）` : "");
}
document.getElementById("txt-source").addEventListener("input", updateSourcePreview);
document.getElementById("btn-source-sample").addEventListener("click", () => {
  document.getElementById("txt-source").value = SOURCE_SAMPLE;
  updateSourcePreview();
});
document.getElementById("btn-source-clear").addEventListener("click", () => {
  document.getElementById("txt-source").value = "";
  updateSourcePreview();
});

document.getElementById("file-import").addEventListener("change", e => {
  const f = e.target.files[0];
  if (!f) return;
  const reader = new FileReader();
  reader.onload = () => {
    document.getElementById("txt-source").value = reader.result;
    updateSourcePreview();
    toast("已导入 " + f.name, "ok");
  };
  reader.readAsText(f, "utf-8");
});

/* ---- 远程数据源 ---- */
function sourcesToText(list) {
  return (list || []).map(s => {
    const url = (s && s.url) || "";
    if (!url) return "";
    const name = (s && s.name) || "";
    const body = name && name !== url ? `${name} | ${url}` : url;
    return (s && s.enabled === false) ? "# " + body : body;
  }).filter(Boolean).join("\n");
}
function textToSources(text) {
  const out = [];
  for (const raw of (text || "").split("\n")) {
    let line = raw.trim();
    if (!line) continue;
    let enabled = true;
    if (line.startsWith("#")) { enabled = false; line = line.slice(1).trim(); }
    if (!line) continue;
    let name = "", url = line;
    const idx = line.indexOf("|");
    if (idx >= 0) { name = line.slice(0, idx).trim(); url = line.slice(idx + 1).trim(); }
    if (!/^https?:\/\//i.test(url)) continue;
    out.push({ name: name || url, url, enabled });
  }
  return out;
}
document.getElementById("btn-source-preview").addEventListener("click", async () => {
  const btn = document.getElementById("btn-source-preview");
  const out = document.getElementById("sources-preview");
  const sources = textToSources(document.getElementById("txt-sources").value);
  const enabled = sources.filter(s => s.enabled);
  if (!enabled.length) { showWarn("没有启用中的数据源（# 开头表示禁用）"); return; }
  btn.disabled = true;
  out.textContent = "正在拉取，请稍候…";
  try {
    const resp = await api("/api/sources/preview", {
      method: "POST",
      body: JSON.stringify({
        remote_sources: enabled,
        port: intVal("in-port", 443),
        source_retries: intVal("in-retries", 3),
        source_retry_delay: floatVal("in-retry-delay", 3),
        source_timeout: floatVal("in-source-timeout", 8),
      }),
    });
    const lines = (resp.report || []).slice();
    if (resp.count) lines.push(`合计 ${resp.count} 条；示例: ${(resp.preview || []).join(", ")}`);
    out.textContent = lines.join("\n") || "未返回任何内容";
  } catch (err) {
    out.textContent = "拉取失败: " + err.message;
  } finally {
    btn.disabled = false;
  }
});

function intVal(id, fallback) {
  const n = parseInt(document.getElementById(id).value, 10);
  return Number.isFinite(n) ? n : fallback;
}
function floatVal(id, fallback) {
  const n = parseFloat(document.getElementById(id).value);
  return Number.isFinite(n) ? n : fallback;
}

async function startScan() {
  if (busy()) { showWarn("已有任务正在运行"); return; }
  const source = document.getElementById("sel-source").value;
  const remoteOn = switchOn("sw-remote");
  const body = {
    ip_version: parseInt(segVal(document.getElementById("seg-version")), 10),
    source_mode: source,
    port: intVal("in-port", 443),
    workers: intVal("in-workers", 200),
    threshold: intVal("in-threshold", 230),
    sample_max: intVal("in-sample", 5000),
    ping_times: intVal("in-ping", 0),
    scan_mode: segVal(document.getElementById("seg-mode")),
    pre_filter_ports: document.getElementById("in-prefilter").value.trim(),
    use_remote_sources: remoteOn,
  };
  if (source !== "仅官方") {
    const text = document.getElementById("txt-source").value.trim();
    if (!text && !remoteOn) {
      showWarn("自定义来源为空：请填写 CIDR / IP / IP 段 / 域名，或启用远程数据源");
      return;
    }
    body.source_text = text;
  }
  try {
    await api("/api/scan/start", { method: "POST", body: JSON.stringify(body) });
    scanResults = [];
    selectedChips = new Set();
    checkedIps = new Set();
    state.funnel = {};
    state.progress = [0, 0, 0, 0];
    lastDoneNote = "";
    document.getElementById("scan-log").textContent = "";
    document.getElementById("speed-log").textContent = "";
    renderFunnel(document.getElementById("scan-funnel"), []);
    document.getElementById("meta-label").textContent =
      `IPv${body.ip_version} · 端口 ${body.port} · 并发 ${body.workers} · ${body.scan_mode === "httping" ? "HTTPing" : "TCPing"}`
      + (remoteOn ? " · 含远程数据源" : "");
    state.stage = "scanning";
    renderResultStats();
    renderResult();
    renderStatus();
  } catch (err) { showError("启动失败: " + err.message); }
}
document.getElementById("btn-start-scan").addEventListener("click", startScan);

/* ================= 结果页 ================= */
function latencyFactor() {
  const r = scanResults[0];
  if (!r) return 1;
  if ((r.scan_mode || "tcping") !== "httping") return 1;
  const tls = [443, 2053, 2083, 2087, 2096, 8443].includes(parseInt(r.port, 10));
  return tls ? 4.0 : 1.3;
}

function regionCodes() {
  const set = new Set();
  scanResults.forEach(r => {
    const code = (r.iata_code || "").toUpperCase();
    if (code && code !== "UNKNOWN") set.add(code);
  });
  return [...set];
}

function visibleScanResults() {
  let data = scanResults.slice();
  if (selectedChips.size) data = data.filter(r => selectedChips.has((r.iata_code || "").toUpperCase()));
  if (document.getElementById("chk-latency").checked) {
    const limit = floatVal("in-latency", 200) * latencyFactor();
    data = data.filter(r => (r.latency || 0) < limit);
  }
  if (document.getElementById("sel-sort").value === "region") {
    data.sort((a, b) => ((a.iata_code || "zzz").localeCompare(b.iata_code || "zzz")) || (a.latency - b.latency));
  } else {
    data.sort((a, b) => (a.latency || 0) - (b.latency || 0));
  }
  return data;
}

function renderResultStats() {
  const codes = regionCodes();
  setStat("rstat-total", scanResults.length);
  setStat("rstat-regions", codes.length);
  const rows = visibleScanResults();
  if (!rows.length) {
    setStat("rstat-min", "—", "ms");
    setStat("rstat-avg", "—", "ms");
    return;
  }
  let min = Infinity, sum = 0;
  rows.forEach(r => {
    const l = Number(r.latency) || 0;
    if (l < min) min = l;
    sum += l;
  });
  setStat("rstat-min", min.toFixed(1), "ms");
  setStat("rstat-avg", (sum / rows.length).toFixed(1), "ms");
}

function renderResult() {
  // 漏斗 + 摘要
  renderFunnel(document.getElementById("result-funnel"), funnelSteps(state.funnel));
  const mode = scanResults[0] && scanResults[0].scan_mode === "httping" ? "HTTPing" : "TCPing";
  document.getElementById("result-summary").textContent =
    scanResults.length ? `显示 ${visibleScanResults().length} / ${scanResults.length} 个 IP · 模式 ${mode}` : "暂无数据，请先扫描或加载历史";

  // 芯片
  const stat = {};
  scanResults.forEach(r => {
    const code = (r.iata_code || "").toUpperCase();
    if (!code || code === "UNKNOWN") return;
    if (!stat[code]) stat[code] = { count: 0, name: r.chinese_name || code };
    stat[code].count++;
  });
  const chipsEl = document.getElementById("region-chips");
  const entries = Object.entries(stat).sort((a, b) => b[1].count - a[1].count);
  if (!entries.length) {
    chipsEl.innerHTML = '<span class="hint">暂无数据，请先扫描或加载历史</span>';
  } else {
    chipsEl.innerHTML = entries.map(([code, s]) =>
      `<button class="chip${selectedChips.has(code) ? " on" : ""}" data-code="${code}">${s.name} ${code} · <b>${s.count}</b></button>`
    ).join("");
    chipsEl.querySelectorAll(".chip").forEach(el => {
      el.addEventListener("click", () => {
        const code = el.dataset.code;
        if (selectedChips.has(code)) selectedChips.delete(code);
        else selectedChips.add(code);
        renderResultStats();
        renderResult();
      });
    });
  }

  // 表格
  const factor = latencyFactor();
  const rows = visibleScanResults();
  const tbody = document.getElementById("scan-tbody");
  if (!rows.length) {
    tbody.innerHTML = scanResults.length
      ? '<tr><td colspan="6" class="empty">没有符合筛选条件的结果</td></tr>'
      : '<tr><td colspan="6" class="empty">暂无数据</td></tr>';
    return;
  }
  tbody.innerHTML = rows.map(r => {
    const cls = r.latency < 100 * factor ? "lat-g" : (r.latency < 200 * factor ? "lat-o" : "lat-r");
    const region = r.iata_code ? `${r.chinese_name || ""} (${r.iata_code})` : "未知";
    const checked = checkedIps.has(r.ip) ? " checked" : "";
    return `<tr>
      <td class="t-c"><input type="checkbox" data-ip="${r.ip}"${checked}></td>
      <td class="mono">${r.ip}</td>
      <td class="t-c">${region}</td>
      <td class="t-c ${cls}">${Number(r.latency).toFixed(1)} ms</td>
      <td class="t-c">${r.port || ""}</td>
      <td class="t-c">${r.scan_time || ""}</td>
    </tr>`;
  }).join("");
}

/* 事件委托：表格由 innerHTML 重建，行内 checkbox 无法逐个绑定，统一在 tbody 上监听 */
document.getElementById("scan-tbody").addEventListener("change", e => {
  const cb = e.target;
  if (!cb || cb.type !== "checkbox" || !cb.dataset.ip) return;
  if (cb.checked) checkedIps.add(cb.dataset.ip);
  else checkedIps.delete(cb.dataset.ip);
});

["chk-latency", "in-latency", "sel-sort"].forEach(id => {
  document.getElementById(id).addEventListener("change", () => { renderResultStats(); renderResult(); });
});
document.getElementById("in-latency").addEventListener("input", () => { renderResultStats(); renderResult(); });

/* 地区芯片 全选 / 清空 */
document.getElementById("btn-chips-all").addEventListener("click", () => {
  const codes = regionCodes();
  if (!codes.length) { showWarn("暂无可选地区"); return; }
  selectedChips = new Set(codes);
  renderResultStats(); renderResult();
});
document.getElementById("btn-chips-none").addEventListener("click", () => {
  selectedChips = new Set();
  renderResultStats(); renderResult();
});

/* 勾选 全部 / 清空（仅作用于当前可见行） */
document.getElementById("btn-check-all").addEventListener("click", () => {
  const rows = visibleScanResults();
  if (!rows.length) { showWarn("没有可见的结果"); return; }
  rows.forEach(r => checkedIps.add(r.ip));
  renderResult();
});
document.getElementById("btn-check-none").addEventListener("click", () => {
  checkedIps = new Set();
  renderResult();
});

document.getElementById("btn-single").addEventListener("click", () => {
  const checked = [...document.querySelectorAll("#scan-tbody input:checked")].map(c => c.dataset.ip);
  if (!checked.length) { showWarn("请先勾选要测速的 IP"); return; }
  startSpeed("selected", { ips: checked });
});
document.getElementById("btn-region-speed").addEventListener("click", () => {
  if (!selectedChips.size) { showWarn("请先点选地区芯片（可多选）"); return; }
  startSpeed("region", { codes: [...selectedChips] });
});
document.getElementById("btn-full-speed").addEventListener("click", () => startSpeed("all"));
document.getElementById("btn-export-scan").addEventListener("click", () => openExport("scan"));

/* ================= 测速 ================= */
async function startSpeed(scope, extra = {}) {
  if (busy()) { showWarn("已有任务正在运行"); return; }
  if (!scanResults.length) { showWarn("请先扫描或加载扫描结果"); return; }
  const body = Object.assign({ scope, count: intVal("in-count", 10) }, extra);
  const speedSel = document.getElementById("sel-speed-url");
  if (speedSel.value === CUSTOM_SPEED_URL) {
    body.speed_url = document.getElementById("in-speed-url").value.trim() || "auto";
  } else if (speedSel.value) {
    body.speed_url = speedSel.value;
  }
  if (document.getElementById("chk-minspeed").checked) {
    body.min_speed = floatVal("in-minspeed", 0);
  }
  try {
    await api("/api/speed/start", { method: "POST", body: JSON.stringify(body) });
    speedResults = [];
    state.speed_progress = [0, 0, 0];
    lastDoneNote = "";
    document.getElementById("speed-log").textContent = "";
    state.stage = "testing";
    renderSpeedStats();
    renderSpeed();
    renderStatus();
    setPage("speed");
  } catch (err) { showError("启动失败: " + err.message); }
}

document.getElementById("btn-region2").addEventListener("click", () => {
  const region = document.getElementById("in-region").value.trim().toUpperCase();
  if (!region) { showWarn("请输入地区码（如 HKG, NRT, SIN）"); return; }
  startSpeed("region", { codes: [region] });
});
document.getElementById("btn-full2").addEventListener("click", () => startSpeed("all"));
document.getElementById("btn-export-speed").addEventListener("click", () => openExport("speed"));

document.getElementById("sel-speed-url").addEventListener("change", e => {
  document.getElementById("in-speed-url").hidden = e.target.value !== CUSTOM_SPEED_URL;
});
document.getElementById("in-region").addEventListener("input", e => {
  e.target.value = e.target.value.toUpperCase();
});

function visibleSpeedResults() {
  let data = speedResults;
  if (document.getElementById("chk-minspeed").checked) {
    const limit = floatVal("in-minspeed", 0);
    data = data.filter(r => (r.download_speed || 0) >= limit);
  }
  return data;
}
["chk-minspeed", "in-minspeed"].forEach(id => {
  document.getElementById(id).addEventListener("change", () => { renderSpeedStats(); renderSpeed(); });
});

function renderSpeedStats() {
  const total = speedResults.length;
  if (!total) {
    setStat("sstat-best", "—", "MB/s");
    setStat("sstat-avg", "—", "MB/s");
    setStat("sstat-pass", 0);
    setStat("sstat-count", 0);
    return;
  }
  let best = 0, sum = 0;
  speedResults.forEach(r => {
    const s = Number(r.download_speed) || 0;
    if (s > best) best = s;
    sum += s;
  });
  const limit = Number((state.settings && state.settings.min_speed) || 0);
  const pass = speedResults.filter(r => (Number(r.download_speed) || 0) >= limit).length;
  setStat("sstat-best", best.toFixed(2), "MB/s");
  setStat("sstat-avg", (sum / total).toFixed(2), "MB/s");
  setStat("sstat-pass", pass);
  setStat("sstat-count", total);
}

function renderSpeed() {
  const rows = visibleSpeedResults();
  const tbody = document.getElementById("speed-tbody");
  if (!rows.length) {
    tbody.innerHTML = speedResults.length
      ? '<tr><td colspan="8" class="empty">没有符合筛选条件的结果</td></tr>'
      : '<tr><td colspan="8" class="empty">暂无数据</td></tr>';
    return;
  }
  const rankCls = ["rank1", "rank2", "rank3"];
  tbody.innerHTML = rows.map((r, i) => {
    const latCls = r.latency < 100 ? "lat-g" : (r.latency < 200 ? "lat-o" : "lat-r");
    const spdCls = r.download_speed >= 10 ? "lat-g" : (r.download_speed >= 5 ? "lat-o" : "lat-r");
    const verify = r.verified === true ? "✓ " : (r.verified === false ? "✗ " : "");
    const vStyle = r.verified === false ? ' style="color:#B91C1C"' : "";
    return `<tr>
      <td class="t-c ${rankCls[i] || ""}">${i + 1}</td>
      <td class="mono">${r.ip}</td>
      <td class="t-c"${vStyle}>${verify}${r.chinese_name || "未知"}(${r.iata_code || ""})</td>
      <td class="t-c ${latCls}">${Number(r.latency).toFixed(1)} ms</td>
      <td class="t-c ${spdCls}">${Number(r.download_speed).toFixed(2)} MB/s</td>
      <td class="t-c"><b>${Number(r.score || 0).toFixed(1)}</b></td>
      <td class="t-c">${r.port || ""}</td>
      <td class="t-c"><span class="tag">${r.test_type || ""}</span></td>
    </tr>`;
  }).join("");
}

/* ================= 停止 ================= */
document.getElementById("btn-stop").addEventListener("click", () => {
  confirmDialog("确认停止", "确定要停止当前正在运行的任务吗？<br>未完成的进度将会丢失。", async () => {
    try {
      await api("/api/stop", { method: "POST", body: "{}" });
      appendLog("⚠️ 已请求停止任务");
    } catch (err) { showError("停止失败: " + err.message); }
  });
});

/* ================= 历史 ================= */
let histVersion = 4;
async function loadHistory() {
  const scanList = document.getElementById("hist-scan-list");
  const speedList = document.getElementById("hist-speed-list");
  scanList.innerHTML = speedList.innerHTML = '<div class="empty">加载中…</div>';
  for (const [type, el] of [["scan", scanList], ["speed", speedList]]) {
    try {
      const resp = await api(`/api/history?type=${type}&ipver=${histVersion}`);
      if (!resp.history.length) {
        el.innerHTML = `<div class="hint">暂无${type === "scan" ? "扫描" : "测速"}记录</div>`;
        continue;
      }
      el.innerHTML = resp.history.map(h => `
        <div class="hist">
          <span>${type === "scan" ? "📡" : "🚀"}</span>
          <div><div class="t">${h.save_time}</div>
          <div class="m">${h.count} 个 IP · ${h.filename}</div></div>
          <div class="ops">
            <button class="btn btn-primary btn-sm" data-act="load">加载</button>
            <button class="btn btn-ghost btn-sm" data-act="export">导出</button>
            <button class="btn btn-red btn-sm" data-act="del">删除</button>
          </div>
        </div>`).join("");
      el.querySelectorAll(".hist").forEach((rowEl, idx) => {
        const h = resp.history[idx];
        rowEl.querySelector('[data-act="load"]').onclick = () => loadHistoryFile(h.filepath, type);
        rowEl.querySelector('[data-act="export"]').onclick = () =>
          downloadURL(`/api/history/export?filepath=${encodeURIComponent(h.filepath)}&type=${type}&format=csv`);
        rowEl.querySelector('[data-act="del"]').onclick = () =>
          confirmDialog("确认删除", "确定要删除这条历史记录吗？<br>该操作不可恢复。", async () => {
            try {
              await api("/api/history", { method: "DELETE", body: JSON.stringify({ filepath: h.filepath }) });
              loadHistory();
            } catch (err) { showError("删除失败: " + err.message); }
          });
      });
    } catch (err) {
      el.innerHTML = `<div class="hint">加载失败: ${err.message}</div>`;
    }
  }
}
async function loadHistoryFile(filepath, type) {
  try {
    const resp = await api("/api/history/load", {
      method: "POST", body: JSON.stringify({ filepath, type }),
    });
    if (type === "scan") {
      scanResults = resp.results;
      selectedChips = new Set();
      checkedIps = new Set();
      state.funnel = {};
      lastDoneNote = `已加载 ${resp.results.length} IP`;
      renderResultStats();
      renderResult();
      appendLog(`✅ 已加载扫描记录 (${resp.save_time})，共 ${resp.results.length} 个IP`);
      setPage("result");
    } else {
      speedResults = resp.results;
      renderSpeedStats();
      renderSpeed();
      appendLog(`✅ 已加载测速记录 (${resp.save_time})，共 ${resp.results.length} 条`);
      setPage("speed");
    }
    renderStatus();
  } catch (err) { showError("加载失败: " + err.message); }
}
document.getElementById("btn-hist-refresh").addEventListener("click", loadHistory);

/* ================= 导出弹窗 ================= */
function openExport(type) {
  // 修复：测速页在没有测速结果时直接导出会 404。
  // 这里自动回退到另一种有数据的类型，都没有才提示。
  if (type === "speed" && !speedResults.length) {
    if (scanResults.length) { toast("暂无测速结果，已改为导出扫描结果", "warn"); type = "scan"; }
    else { showWarn("没有可导出的结果"); return; }
  }
  if (type === "scan" && !scanResults.length) {
    if (speedResults.length) { toast("暂无扫描结果，已改为导出测速结果", "warn"); type = "speed"; }
    else { showWarn("没有可导出的结果"); return; }
  }

  const FIELDS = type === "speed" ? SPEED_FIELDS : SCAN_FIELDS;
  const keys = Object.keys(FIELDS);
  const fieldsHTML = keys.map(k =>
    `<label class="check"${k === "ip" ? ' style="opacity:.6"' : ""}>
      <input type="checkbox" class="ex-field" value="${k}" checked${k === "ip" ? " disabled" : ""}> ${FIELDS[k]}
    </label>`).join("");

  const body = `
    <div class="sec"><div class="sec-title">格式</div>
      <div class="row">
        <label class="check"><input type="radio" name="exfmt" value="csv" checked> CSV</label>
        <label class="check"><input type="radio" name="exfmt" value="json"> JSON</label>
        <label class="check"><input type="radio" name="exfmt" value="txt"> TXT（ip:port）</label>
      </div></div>
    <div class="sec"><div class="sec-title">导出字段</div><div class="fields">${fieldsHTML}</div></div>
    ${type === "speed" ? `<div class="sec"><div class="sec-title">筛选</div>
      <label class="check"><input type="checkbox" id="ex-qualified"> 仅导出合格结果，低于
      <input type="number" id="ex-minspeed" value="5" min="0" step="0.5" style="width:70px"> MB/s</label></div>` : ""}
  `;
  showModal(`导出${type === "scan" ? "扫描" : "测速"}结果`, body, [
    { label: "取消", cls: "btn-ghost" },
    {
      label: "导出", cls: "btn-primary", onClick: () => {
        const fmt = document.querySelector('input[name="exfmt"]:checked').value;
        const fields = [...document.querySelectorAll(".ex-field:checked")].map(c => c.value);
        let url = `/api/export?type=${type}&format=${fmt}`;
        if (fmt !== "txt" && fields.length && fields.length < keys.length) {
          url += "&fields=" + fields.join(",");
        }
        const q = document.getElementById("ex-qualified");
        if (type === "speed" && q && q.checked) {
          url += "&qualified_only=1&min_speed=" + floatVal("ex-minspeed", 5);
        }
        downloadURL(url);
      },
    },
  ]);
}

/* ================= 设置 ================= */
function bindSwitch(id) {
  const el = document.getElementById(id);
  el.addEventListener("click", () => el.classList.toggle("on"));
  return el;
}
function setSwitch(id, on) { document.getElementById(id).classList.toggle("on", !!on); }
function switchOn(id) { return document.getElementById(id).classList.contains("on"); }
["sw-tray", "sw-verify", "sw-http", "sw-lan", "sw-remote"].forEach(bindSwitch);

function settingsFormFocused() {
  const ae = document.activeElement;
  return currentPage === "settings" && ae && document.getElementById("page-settings").contains(ae);
}

function updateTokenHint() {
  const set = !!document.getElementById("set-token").dataset.set;
  const el = document.getElementById("token-hint");
  if (clearToken) {
    el.textContent = "保存后将清除 Token，面板不再鉴权（任何人可访问）";
  } else if (set) {
    el.textContent = "当前已设置 Token。留空表示不修改，输入新值则覆盖。";
  } else {
    el.textContent = "当前未设置 Token，面板无需鉴权即可访问。";
  }
}

function populateSettings(s) {
  setSwitch("sw-tray", s.tray_on_close);
  document.getElementById("set-scan-mode").value = s.scan_mode;
  document.getElementById("set-sample").value = s.sample_max;
  document.getElementById("set-region-topn").value = s.per_region_topn != null ? s.per_region_topn : 0;
  const speedSel = document.getElementById("set-speed-url-mode");
  const preset = speedUrlToPreset(s.speed_url);
  speedSel.value = preset;
  document.getElementById("row-speed-url").hidden = preset !== CUSTOM_SPEED_URL;
  document.getElementById("set-speed-url").value = preset === CUSTOM_SPEED_URL ? (s.speed_url || "") : "";
  updateSpeedUrlHint();
  setSwitch("sw-verify", s.verify_nodes);
  document.getElementById("set-minspeed").value = s.min_speed;
  document.getElementById("set-interval").value = s.download_interval != null ? s.download_interval : 3;
  document.getElementById("set-speed-workers").value = s.speed_workers != null ? s.speed_workers : 1;
  document.getElementById("set-result-limit").value = s.speed_result_limit != null ? s.speed_result_limit : 0;
  document.getElementById("set-w-speed").value = s.score_speed_weight;
  document.getElementById("set-w-latency").value = s.score_latency_weight;
  setSwitch("sw-http", s.http_enabled);
  document.getElementById("set-port").value = s.http_port;
  setSwitch("sw-lan", s.allow_lan);

  // 远程数据源（扫描页）
  setSwitch("sw-remote", s.use_remote_sources);
  document.getElementById("txt-sources").value = sourcesToText(s.remote_sources);
  document.getElementById("in-retries").value = s.source_retries != null ? s.source_retries : 3;
  document.getElementById("in-retry-delay").value = s.source_retry_delay != null ? s.source_retry_delay : 3;
  document.getElementById("in-source-timeout").value = s.source_timeout != null ? s.source_timeout : 8;
  document.getElementById("in-prefilter").value = s.pre_filter_ports || "";

  // Token 永不回显：只显示「是否已设置」，留空 = 不修改
  const tokEl = document.getElementById("set-token");
  tokEl.value = "";
  tokEl.placeholder = s.http_token_set ? "留空表示不修改" : "留空则不鉴权";
  tokEl.dataset.set = s.http_token_set ? "1" : "";
  clearToken = false;
  updateTokenHint();
  updateHttpHint();
}

document.getElementById("btn-token-clear").addEventListener("click", () => {
  clearToken = true;
  document.getElementById("set-token").value = "";
  updateTokenHint();
});
function updateSpeedUrlHint() {
  const preset = document.getElementById("set-speed-url-mode").value;
  document.getElementById("speed-url-hint").textContent = preset === "auto"
    ? "auto 会先探测出口 ISP：识别到中国移动时从移动友好/移动专属源里随机取一个，否则用 Cloudflare 官方源"
    : (preset === CUSTOM_SPEED_URL
      ? "手填任意下载地址；未写路径时会自动补 /__down?bytes=99999999"
      : "使用固定测速地址（内置预设）");
}

document.getElementById("set-speed-url-mode").addEventListener("change", e => {
  const isCustom = e.target.value === CUSTOM_SPEED_URL;
  document.getElementById("row-speed-url").hidden = !isCustom;
  if (isCustom && !document.getElementById("set-speed-url").value.trim()) {
    document.getElementById("set-speed-url").value = CF_SPEED_URL;
  }
  updateSpeedUrlHint();
});
["sw-http", "sw-lan"].forEach(id =>
  document.getElementById(id).addEventListener("click", updateHttpHint));

function updateHttpHint() {
  const on = switchOn("sw-http");
  const port = intVal("set-port", 17443);
  const lan = switchOn("sw-lan");
  document.getElementById("http-hint").textContent = on
    ? `面板地址: ${lan ? "http://<本机IP>:" + port : location.origin.replace(/:\d+$/, ":" + port)}  ·  HTTP 相关设置重启服务后生效`
    : "HTTP 服务已关闭（重启后不再启动）";
}

function collectSettings() {
  const speedPreset = document.getElementById("set-speed-url-mode").value;
  const out = {
    tray_on_close: switchOn("sw-tray"),
    scan_mode: document.getElementById("set-scan-mode").value,
    sample_max: intVal("set-sample", 5000),
    speed_url: speedPreset === CUSTOM_SPEED_URL
      ? (document.getElementById("set-speed-url").value.trim() || "auto")
      : speedPreset,
    verify_nodes: switchOn("sw-verify"),
    min_speed: floatVal("set-minspeed", 0),
    download_interval: intVal("set-interval", 3),
    speed_workers: intVal("set-speed-workers", 1),
    speed_result_limit: intVal("set-result-limit", 0),
    per_region_topn: intVal("set-region-topn", 0),
    score_speed_weight: floatVal("set-w-speed", 0),
    score_latency_weight: floatVal("set-w-latency", 0),
    http_enabled: switchOn("sw-http"),
    http_port: intVal("set-port", 17443),
    allow_lan: switchOn("sw-lan"),
    // 扫描页的远程数据源与前置过滤也一并持久化（它们属于「设置」而非一次性参数）
    use_remote_sources: switchOn("sw-remote"),
    remote_sources: textToSources(document.getElementById("txt-sources").value),
    source_retries: intVal("in-retries", 3),
    source_retry_delay: floatVal("in-retry-delay", 3),
    source_timeout: floatVal("in-source-timeout", 8),
    pre_filter_ports: document.getElementById("in-prefilter").value.trim(),
  };
  // Token 三态：清除 → ""；输入了新值 → 覆盖；留空且未点清除 → 不提交（后端保持原值）
  const tok = document.getElementById("set-token").value.trim();
  if (clearToken) out.http_token = "";
  else if (tok) out.http_token = tok;
  return out;
}

/* 把设置回填到所有相关表单（首次加载 / SSE 同步 / 保存后刷新共用） */
function applySettingsToUI(s) {
  const full = Object.assign({}, DEFAULT_SETTINGS, s || {});
  populateSettings(full);
  // 扫描页参数
  setSeg(document.getElementById("seg-mode"), full.scan_mode);
  document.getElementById("in-sample").value = full.sample_max;
  document.getElementById("in-workers").value = full.workers;
  document.getElementById("in-threshold").value = full.latency_threshold;
  document.getElementById("in-ping").value = full.ping_times;
  const sel = document.getElementById("sel-source");
  if (full.cidr_mode && [...sel.options].some(o => o.value === full.cidr_mode)) sel.value = full.cidr_mode;
  onSourceChange();
  // 测速页参数
  const speedSel = document.getElementById("sel-speed-url");
  const preset = speedUrlToPreset(full.speed_url);
  speedSel.value = preset;
  document.getElementById("in-speed-url").hidden = preset !== CUSTOM_SPEED_URL;
  document.getElementById("in-speed-url").value = preset === CUSTOM_SPEED_URL ? (full.speed_url || "") : "";
  document.getElementById("in-minspeed").value = full.min_speed;
  document.getElementById("chk-minspeed").checked = Number(full.min_speed) > 0;
  // 供统计卡使用
  state.settings = Object.assign({}, state.settings, full);
  renderSpeedStats();
  renderSpeed();
}

async function saveSettings() {
  try {
    const resp = await api("/api/settings", { method: "PUT", body: JSON.stringify(collectSettings()) });
    applySettingsToUI(resp.settings);
    let warnings = [];
    try { warnings = (await api("/api/health")).warnings || []; } catch (e) {}
    if (warnings.length) {
      showModal("设置已保存", "<div>设置已保存</div><div class='sec-title' style='margin-top:10px'>⚠ 体检提醒</div>" +
        "<ul class='warn-list'>" + warnings.map(w => `<li>${w}</li>`).join("") + "</ul>",
        [{ label: "确定", cls: "btn-primary" }]);
    } else {
      showInfo("设置已保存 ✓");
    }
  } catch (err) { showError("保存失败: " + err.message); }
}
async function restoreSettings() {
  confirmDialog("恢复默认", "确定恢复全部默认设置吗？<br>包含 HTTP 端口与访问 Token。", async () => {
    try {
      const resp = await api("/api/settings/reset", { method: "POST", body: "{}" });
      applySettingsToUI(resp.settings);
      showInfo("已恢复默认设置");
    } catch (err) { showError("操作失败: " + err.message); }
  });
}
async function healthCheck() {
  try {
    const health = await api("/api/health");
    if (health.warnings.length) {
      showModal("配置体检", "<div>发现以下可优化项:</div><ul class='warn-list'>" +
        health.warnings.map(w => `<li>${w}</li>`).join("") + "</ul>",
        [{ label: "确定", cls: "btn-primary" }]);
    } else {
      showInfo("未发现配置问题 ✓");
    }
  } catch (err) { showError("体检失败: " + err.message); }
}
document.getElementById("btn-set-save").addEventListener("click", saveSettings);
document.getElementById("btn-set-restore").addEventListener("click", restoreSettings);
document.getElementById("btn-set-health").addEventListener("click", healthCheck);

/* ================= 启动 ================= */
async function boot() {
  bindSeg(document.getElementById("seg-version"));
  bindSeg(document.getElementById("seg-mode"));
  bindSeg(document.getElementById("seg-hist-ver"), v => { histVersion = parseInt(v, 10); loadHistory(); });
  // 测速地址预设下拉必须在首次回填设置之前建好
  fillSpeedUrlSelect(document.getElementById("sel-speed-url"));
  fillSpeedUrlSelect(document.getElementById("set-speed-url-mode"));
  renderResultStats();
  renderSpeedStats();
  renderStatus();
  try {
    const s = await api("/api/state");
    applyState(s);
  } catch (err) {
    showError("无法获取状态: " + err.message);
  }
  connectSSE();
  renderCTA();
  renderStatus();
  onSourceChange();
}
boot();
