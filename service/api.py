#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import os
import json
import hmac
import asyncio
import logging
from datetime import datetime
from typing import Any, Dict, Optional

from aiohttp import web

from core.constants import get_version, resource_path
from core.analytics import region_stats
from core.export import render_export, EXPORT_FORMATS
from core.factory import create_scanner, create_speed_task
from core.importer import parse_source_text, parse_port_list
from core.sources import fetch_sources, sanitize_sources, resolve_source_entries
from core.utils import to_int, to_float
from settings import (
    get_settings, apply_settings, reset_settings, CIDR_MODES, LEGACY_CIDR_MODES,
    get_history_list, load_results_from_file, delete_history, SAVE_DIR,
)
from service.task_manager import task_manager
from service.events import (
    EV_LOG, EV_PROGRESS, EV_FUNNEL, EV_STATE,
    EV_SCAN_DONE, EV_SPEED_PROGRESS, EV_SPEED_DONE, EV_SPEED_ABORT, EV_SETTINGS,
)
from service.health import validate_settings


logger = logging.getLogger("CloudTrace")

SSE_EVENTS = (EV_LOG, EV_PROGRESS, EV_FUNNEL, EV_STATE,
              EV_SCAN_DONE, EV_SPEED_PROGRESS, EV_SPEED_DONE, EV_SPEED_ABORT,
              EV_SETTINGS)

# 客户端只读、不可通过 PUT 覆盖的派生字段
_READONLY_SETTING_KEYS = {"http_token_set"}


def _json(data, status: int = 200) -> web.Response:
    return web.json_response(data, status=status, dumps=lambda o: json.dumps(o, ensure_ascii=False, default=str))


def _err(message: str, status: int = 400) -> web.Response:
    return _json({"ok": False, "error": message}, status)


def public_settings(settings: Dict[str, Any], include_token: bool = False) -> Dict[str, Any]:
    """对外暴露的设置副本：默认剔除 http_token，只给出「是否已设置」标记。"""
    out = dict(settings or {})
    token = str(out.get("http_token") or "")
    out["http_token_set"] = bool(token)
    if not include_token:
        out["http_token"] = ""
    return out


@web.middleware
async def auth_middleware(request: web.Request, handler):
    if request.path.startswith("/api/"):
        token = str(get_settings().get("http_token") or "")
        if token:
            supplied = request.headers.get("X-Token") or request.query.get("token") or ""
            # 恒定时间比较，避免通过响应耗时逐字节猜测 Token
            if not hmac.compare_digest(str(supplied), token):
                return _err("unauthorized: token 无效", 401)
    return await handler(request)


def _web_dir() -> Optional[str]:
    path = resource_path("web")
    if os.path.isdir(path):
        return path
    return None


def _safe_history_path(filepath: str) -> Optional[str]:
    real = os.path.realpath(filepath)
    base = os.path.realpath(SAVE_DIR)
    if real.startswith(base + os.sep) and os.path.isfile(real):
        return real
    return None


# ================== 状态 ==================
async def h_state(request):
    snap = task_manager.snapshot()
    snap["version"] = get_version()
    snap["settings"] = public_settings(get_settings())
    return _json(snap)


async def h_regions(request):
    snap = task_manager.snapshot()
    stats = region_stats(snap["scan_results"])
    return _json({"regions": stats})


async def h_health(request):
    warnings = validate_settings(get_settings())
    return _json({"warnings": warnings})


# ================== 任务控制 ==================
async def h_scan_start(request):
    try:
        body = await request.json()
    except Exception:
        return _err("请求体必须是 JSON")
    if not isinstance(body, dict):
        return _err("请求体必须是对象")

    if task_manager.busy:
        return _err("已有任务正在运行", 409)

    source_mode = body.get("source_mode", "仅官方")
    # 兼容旧版「非标列表」：语义等价于「仅自定义」（自定义框本来就接受 IP 列表）
    source_mode = LEGACY_CIDR_MODES.get(source_mode, source_mode)
    if source_mode not in CIDR_MODES:
        return _err("source_mode 取值无效")
    ip_version = to_int(body.get("ip_version", 4), 4)
    if ip_version not in (4, 6):
        return _err("ip_version 必须是 4 或 6")

    scan_mode = body.get("scan_mode", "tcping")
    if scan_mode not in ("tcping", "httping"):
        return _err("scan_mode 必须是 tcping 或 httping")

    port = to_int(body.get("port", 443), 443)
    if not (1 <= port <= 65535):
        return _err("端口无效")

    settings = get_settings()
    use_remote = body.get("use_remote_sources")
    use_remote = bool(settings.get("use_remote_sources")) if use_remote is None else bool(use_remote)
    pre_filter = parse_port_list(body.get("pre_filter_ports")
                                 if body.get("pre_filter_ports") is not None
                                 else settings.get("pre_filter_ports"))

    params = {
        "ip_version": ip_version,
        "source_mode": source_mode,
        "cidrs": [],
        "entries": [],
        "port": port,
        "workers": to_int(body.get("workers", 200), 200, 1, 2000),
        "threshold": to_int(body.get("threshold", 230), 230, 1, 60000),
        "sample_max": to_int(body.get("sample_max", 5000), 5000, 1, 200000),
        "ping_times": to_int(body.get("ping_times", 0), 0, 0, 20),
        "scan_mode": scan_mode,
        "pre_filter_ports": pre_filter,
    }

    if source_mode != "仅官方":
        text = str(body.get("source_text") or body.get("import_text") or "").strip()
        if not text and body.get("cidrs"):
            # 兼容只传 cidrs 数组的旧客户端
            text = "\n".join(str(c) for c in body["cidrs"])
        cidrs, entries, errors, stats = parse_source_text(text, port, ip_version)
        if errors:
            return _err("来源解析失败: " + "; ".join(errors[:5]))
        if not cidrs and not entries and not use_remote:
            if stats.get("skipped"):
                return _err(
                    f"来源中的条目与所选 IP 版本(IPv{ip_version})不符，请检查 IP 版本或来源内容")
            return _err("自定义来源为空：请填写 CIDR / IP / IP 段 / 域名，或启用远程数据源")
        params["cidrs"] = cidrs
        params["entries"] = entries

    if use_remote:
        params["remote_fetch"] = lambda: resolve_source_entries(dict(get_settings()), port)

    scanner = create_scanner(params)
    if not task_manager.start_scan(scanner, scanner.ip_version):
        return _err("任务启动失败：已有任务正在运行", 409)
    return _json({"ok": True, "stage": task_manager.stage})


async def h_sources_preview(request):
    """拉取一次远程数据源并返回统计（供 UI「拉取预览」按钮使用）。"""
    try:
        body = await request.json()
    except Exception:
        body = {}
    if not isinstance(body, dict):
        body = {}

    settings = get_settings()
    raw = body.get("remote_sources")
    sources = sanitize_sources(raw) if raw is not None else sanitize_sources(settings.get("remote_sources"))
    if not sources:
        return _json({"ok": False, "error": "未配置任何远程数据源", "report": []})
    if not [s for s in sources if s.get("enabled")]:
        return _json({"ok": False, "error": "没有任何一个源处于启用状态", "report": []})

    port = to_int(body.get("port", settings.get("port", 443)), 443, 1, 65535)
    timeout = to_float(body.get("source_timeout", settings.get("source_timeout", 8.0)), 8.0, 1.0, 120.0)
    retries = to_int(body.get("source_retries", settings.get("source_retries", 3)), 3, 1, 10)
    delay = to_float(body.get("source_retry_delay", settings.get("source_retry_delay", 3.0)), 3.0, 0.0, 60.0)

    loop = asyncio.get_running_loop()
    try:
        entries, report = await loop.run_in_executor(
            None,
            lambda: fetch_sources(sources, port, timeout, retries, delay),
        )
    except Exception as e:
        return _err(f"拉取失败: {e}", 500)

    preview = [f"{e['ip']}:{e['port']}" for e in entries[:20]]
    return _json({
        "ok": True,
        "count": len(entries),
        "report": report,
        "preview": preview,
    })


async def h_speed_start(request):
    try:
        body = await request.json()
    except Exception:
        return _err("请求体必须是 JSON")
    if not isinstance(body, dict):
        return _err("请求体必须是对象")

    if task_manager.busy:
        return _err("已有任务正在运行", 409)

    snap = task_manager.snapshot()
    scan_results = snap["scan_results"]
    if not scan_results:
        return _err("没有可用的扫描结果，请先扫描或加载历史", 409)

    settings = get_settings()
    scope = body.get("scope", "all")
    if scope not in ("all", "region", "selected"):
        return _err("scope 必须是 all / region / selected")

    opts = {
        "region_code": None,
        "selected_ips": None,
        "count": to_int(body.get("count", 10), 10, 1, 500),
        "current_port": to_int(scan_results[0].get("port", 443), 443, 1, 65535),
        "speed_url": body.get("speed_url") or settings.get("speed_url", "auto"),
        "min_speed": to_float(body.get("min_speed", settings.get("min_speed", 0)), 0.0, 0.0, 10000.0),
        "label": None,
    }

    if scope == "region":
        codes = {str(c).upper() for c in (body.get("codes") or [])}
        if not codes:
            return _err("codes 不能为空")
        matched = [r for r in scan_results if (r.get("iata_code") or "").upper() in codes]
        if not matched:
            return _err("所选地区没有匹配的 IP")
        opts["selected_ips"] = matched
        opts["label"] = "地区测速"
    elif scope == "selected":
        ips = [str(x) for x in (body.get("ips") or [])]
        if not ips:
            return _err("ips 不能为空")
        by_ip = {r.get("ip"): r for r in scan_results}
        selected = []
        for ip in ips:
            if ip in by_ip:
                selected.append(by_ip[ip])
            else:
                selected.append({"ip": ip, "latency": 0, "iata_code": None,
                                 "chinese_name": "未知地区", "port": opts["current_port"]})
        opts["selected_ips"] = selected
        opts["label"] = "单点测速" if len(selected) == 1 else "自选测速"

    task = create_speed_task(scan_results, opts, settings)
    if not task_manager.start_speed_test(task):
        return _err("任务启动失败：已有任务正在运行", 409)
    return _json({"ok": True, "stage": task_manager.stage})


async def h_stop(request):
    if not task_manager.busy:
        return _json({"ok": True, "message": "当前没有运行中的任务"})
    task_manager.stop()
    return _json({"ok": True, "message": "已请求停止"})


# ================== SSE ==================
async def h_events(request: web.Request):
    app = request.app
    loop = asyncio.get_event_loop()
    queue: asyncio.Queue = asyncio.Queue(maxsize=500)

    def push(event: str, payload):
        def _put():
            try:
                queue.put_nowait((event, payload))
            except asyncio.QueueFull:
                pass
        try:
            loop.call_soon_threadsafe(_put)
        except RuntimeError:
            pass

    unsubs = [task_manager.bus.subscribe(ev, lambda p, e=ev: push(e, p)) for ev in SSE_EVENTS]
    push(EV_STATE, task_manager.snapshot())

    resp = web.StreamResponse(
        headers={"Content-Type": "text/event-stream",
                 "Cache-Control": "no-cache",
                 "X-Accel-Buffering": "no"})
    await resp.prepare(request)

    stop_event: asyncio.Event = app["sse_stop"]
    try:
        while not stop_event.is_set():
            try:
                event, payload = await asyncio.wait_for(queue.get(), timeout=1.0)
            except asyncio.TimeoutError:
                await resp.write(b": ping\n\n")
                continue
            data = json.dumps(payload, ensure_ascii=False, default=str)
            await resp.write(f"event: {event}\ndata: {data}\n\n".encode("utf-8"))
    except (ConnectionResetError, ConnectionError, BrokenPipeError, asyncio.CancelledError):
        pass
    finally:
        for u in unsubs:
            u()
        try:
            await resp.write(b"event: bye\ndata: {}\n\n")
            await resp.write_eof()
        except Exception:
            pass
    return resp


# ================== 历史 ==================
async def h_history_list(request):
    type_key = request.query.get("type", "scan")
    ipver = to_int(request.query.get("ipver", 4), 4)
    if type_key not in ("scan", "speed"):
        return _err("type 必须是 scan 或 speed")
    if ipver not in (4, 6):
        return _err("ipver 必须是 4 或 6")
    return _json({"history": get_history_list(ipver, type_key)})


async def h_history_load(request):
    try:
        body = await request.json()
    except Exception:
        return _err("请求体必须是 JSON")
    if not isinstance(body, dict):
        return _err("请求体必须是对象")
    filepath = _safe_history_path(str(body.get("filepath", "")))
    if not filepath:
        return _err("文件不存在或不在保存目录内")
    type_key = body.get("type", "scan")
    if type_key not in ("scan", "speed"):
        return _err("type 必须是 scan 或 speed")

    data = load_results_from_file(filepath)
    if data is None or not data.get("results"):
        return _err("文件损坏或结果为空")

    if type_key == "scan":
        task_manager.set_scan_results(data["results"])
    else:
        task_manager.set_speed_results(data["results"])
    return _json({"ok": True, "type": type_key,
                  "save_time": data.get("save_time"), "results": data["results"]})


async def h_history_delete(request):
    filepath = None
    if request.query.get("filepath"):
        filepath = request.query["filepath"]
    else:
        try:
            body = await request.json()
            filepath = str(body.get("filepath", ""))
        except Exception:
            pass
    safe = _safe_history_path(filepath or "")
    if not safe:
        return _err("文件不存在或不在保存目录内")
    if delete_history(safe):
        return _json({"ok": True})
    return _err("删除失败", 500)


async def h_history_export(request):
    """从历史文件直接下载导出。"""
    filepath = _safe_history_path(request.query.get("filepath", ""))
    if not filepath:
        return _err("文件不存在或不在保存目录内")
    type_key = request.query.get("type", "scan")
    if type_key not in ("scan", "speed"):
        return _err("type 必须是 scan 或 speed")
    data = load_results_from_file(filepath)
    if data is None or not data.get("results"):
        return _err("文件损坏或结果为空")
    return _export_response(data["results"], type_key, request)


# ================== 导出 ==================
def _export_response(results, type_key: str, request) -> web.Response:
    fmt = request.query.get("format", "csv")
    if fmt not in EXPORT_FORMATS:
        return _err("format 必须是 csv / json / txt")
    if not results:
        return _err("没有可导出的结果", 404)

    fields = request.query.get("fields")
    field_list = [f for f in fields.split(",") if f] if fields else None
    qualified_only = request.query.get("qualified_only") in ("1", "true", "True")
    min_speed = to_float(request.query.get("min_speed", 0), 0.0)

    content = render_export(results, type_key, fields=field_list,
                            qualified_only=qualified_only, min_speed=min_speed, fmt=fmt)
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    filename = f"cf_{type_key}_{timestamp}.{fmt}"
    if fmt == "csv":
        body = content.encode("utf-8-sig")
        ctype = "text/csv"
    elif fmt == "json":
        body = content.encode("utf-8")
        ctype = "application/json"
    else:
        body = content.encode("utf-8")
        ctype = "text/plain"
    return web.Response(body=body, content_type=ctype, charset="utf-8",
                        headers={"Content-Disposition": f'attachment; filename="{filename}"'})


async def h_export(request):
    type_key = request.query.get("type", "scan")
    if type_key not in ("scan", "speed"):
        return _err("type 必须是 scan 或 speed")
    snap = task_manager.snapshot()
    results = snap["scan_results"] if type_key == "scan" else snap["speed_results"]
    return _export_response(results, type_key, request)


# ================== 设置 ==================
async def h_settings_get(request):
    return _json(public_settings(get_settings()))


async def h_settings_put(request):
    try:
        body = await request.json()
    except Exception:
        return _err("请求体必须是 JSON")
    if not isinstance(body, dict):
        return _err("设置必须是对象")

    patch = {k: v for k, v in body.items() if k not in _READONLY_SETTING_KEYS}
    # 就地更新共享对象 → 桌面端与 Web 面板立刻看到同一份设置
    settings = apply_settings(patch)
    # 广播前剔除 http_token：SSE 只用于回填表单，不应把明文 Token 推给客户端
    task_manager.broadcast_settings(public_settings(settings))
    return _json({"ok": True, "settings": public_settings(settings)})


async def h_settings_reset(request):
    settings = reset_settings()
    task_manager.broadcast_settings(public_settings(settings))
    return _json({"ok": True, "settings": public_settings(settings)})


# ================== 静态 ==================
async def h_index(request):
    web_dir = _web_dir()
    if not web_dir:
        return _err("Web 面板资源缺失（web/ 目录不存在）", 404)
    return web.FileResponse(os.path.join(web_dir, "index.html"))


def create_app() -> web.Application:
    app = web.Application(middlewares=[auth_middleware])
    app["sse_stop"] = asyncio.Event()

    app.router.add_get("/api/state", h_state)
    app.router.add_get("/api/regions", h_regions)
    app.router.add_get("/api/health", h_health)
    app.router.add_post("/api/scan/start", h_scan_start)
    app.router.add_post("/api/sources/preview", h_sources_preview)
    app.router.add_post("/api/speed/start", h_speed_start)
    app.router.add_post("/api/stop", h_stop)
    app.router.add_get("/api/events", h_events)
    app.router.add_get("/api/history", h_history_list)
    app.router.add_post("/api/history/load", h_history_load)
    app.router.add_delete("/api/history", h_history_delete)
    app.router.add_get("/api/history/export", h_history_export)
    app.router.add_get("/api/export", h_export)
    app.router.add_get("/api/settings", h_settings_get)
    app.router.add_put("/api/settings", h_settings_put)
    app.router.add_post("/api/settings/reset", h_settings_reset)

    app.router.add_get("/", h_index)
    web_dir = _web_dir()
    if web_dir:
        app.router.add_static("/static/", web_dir, show_index=False)
    return app
