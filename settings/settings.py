#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import os
import json
import logging
import threading
from typing import Any, Dict, Optional

from core.constants import APP_DIR
from core.importer import parse_port_list
from core.sources import DEFAULT_SOURCES, sanitize_sources
from core.utils import to_bool, to_float, to_int, atomic_write_json, atomic_write_text


logger = logging.getLogger("CloudTrace")

SAVE_DIR = os.path.join(APP_DIR, "CloudTrace_history")

SETTINGS_FILE = os.path.join(APP_DIR, "settings.json")
CUSTOM_CIDRS_FILE = os.path.join(APP_DIR, "custom_cidrs.txt")

# 「IP 来源」模式：自定义框同时接受 CIDR / 单 IP / IP 段 / 域名，
# 由 core.importer.parse_source_text 自动分类（旧的「非标列表」已并入「仅自定义」）
CIDR_MODES = ("仅官方", "仅自定义", "官方+自定义")
LEGACY_CIDR_MODES = {"非标列表": "仅自定义"}

DEFAULT_SETTINGS = {
    "tray_on_close": False,
    "cidr_mode": "仅官方",
    # 扫描
    "scan_mode": "tcping",          # tcping | httping
    "sample_max": 5000,
    "workers": 200,
    "latency_threshold": 230,
    "ping_times": 0,                # 0 = 自动（IPv4 三次 / IPv6 两次）
    "pre_filter_ports": "",         # 空 = 不按端口前置过滤，例如 "443,8443"
    # 远程数据源（参考 cfnb ADDITIONAL_SOURCES）
    "use_remote_sources": False,
    "remote_sources": [dict(s) for s in DEFAULT_SOURCES],
    "source_retries": 3,
    "source_retry_delay": 3.0,
    "source_timeout": 8.0,
    # 测速
    "speed_url": "auto",
    "min_speed": 0,
    "verify_nodes": True,
    "download_interval": 3,
    "speed_workers": 1,             # 1 = 串行（最准）；>1 并发（更快）
    "speed_result_limit": 0,        # 0 = 不提前收敛
    "per_region_topn": 0,           # >0 时每个地区只取延迟最低的 N 个进测速队列
    # 综合评分权重
    "score_speed_weight": 3.0,
    "score_latency_weight": 3.0,
    # HTTP 服务面板
    "http_enabled": True,
    "http_port": 17443,
    "allow_lan": False,
    "http_token": "",
}


# 进程内唯一设置对象（单一数据源）
_SETTINGS_CACHE: Optional[Dict[str, Any]] = None
_lock = threading.RLock()


def sanitize_settings(raw: Any) -> Dict[str, Any]:
    """把任意来源（磁盘 / Web / 表单）的设置规范化成「类型正确、值合法」的 dict。

    这是防止 `int(None)` 一类崩溃的唯一入口：所有读配置的路径都先经过这里。
    """
    src = dict(raw) if isinstance(raw, dict) else {}
    out: Dict[str, Any] = dict(DEFAULT_SETTINGS)
    out.update(src)   # 保留未知键，便于向后兼容

    cidr_mode = out.get("cidr_mode")
    if cidr_mode in LEGACY_CIDR_MODES:
        cidr_mode = LEGACY_CIDR_MODES[cidr_mode]
    out["cidr_mode"] = cidr_mode if cidr_mode in CIDR_MODES else "仅官方"
    out["scan_mode"] = "httping" if out.get("scan_mode") == "httping" else "tcping"

    out["tray_on_close"] = to_bool(out.get("tray_on_close"), False)
    out["sample_max"] = to_int(out.get("sample_max"), 5000, 100, 200000)
    out["workers"] = to_int(out.get("workers"), 200, 1, 2000)
    out["latency_threshold"] = to_int(out.get("latency_threshold"), 230, 1, 60000)
    out["ping_times"] = to_int(out.get("ping_times"), 0, 0, 20)
    out["pre_filter_ports"] = ",".join(
        str(p) for p in parse_port_list(out.get("pre_filter_ports")))

    # 远程数据源
    out["use_remote_sources"] = to_bool(out.get("use_remote_sources"), False)
    sources = sanitize_sources(out.get("remote_sources"))
    out["remote_sources"] = sources if sources else [dict(s) for s in DEFAULT_SOURCES]
    out["source_retries"] = to_int(out.get("source_retries"), 3, 1, 10)
    out["source_retry_delay"] = to_float(out.get("source_retry_delay"), 3.0, 0.0, 60.0)
    out["source_timeout"] = to_float(out.get("source_timeout"), 8.0, 1.0, 120.0)

    out["speed_url"] = str(out.get("speed_url") or "auto").strip() or "auto"
    out["min_speed"] = to_float(out.get("min_speed"), 0.0, 0.0, 10000.0)
    out["verify_nodes"] = to_bool(out.get("verify_nodes"), True)
    out["download_interval"] = to_int(out.get("download_interval"), 3, 0, 60)
    out["speed_workers"] = to_int(out.get("speed_workers"), 1, 1, 16)
    out["speed_result_limit"] = to_int(out.get("speed_result_limit"), 0, 0, 10000)
    out["per_region_topn"] = to_int(out.get("per_region_topn"), 0, 0, 100)

    out["score_speed_weight"] = to_float(out.get("score_speed_weight"), 3.0, 0.0, 1000.0)
    out["score_latency_weight"] = to_float(out.get("score_latency_weight"), 3.0, 0.0, 1000.0)

    out["http_enabled"] = to_bool(out.get("http_enabled"), True)
    out["http_port"] = to_int(out.get("http_port"), 17443, 1, 65535)
    out["allow_lan"] = to_bool(out.get("allow_lan"), False)
    out["http_token"] = str(out.get("http_token") or "")
    return out


def _read_disk() -> Dict[str, Any]:
    try:
        if os.path.exists(SETTINGS_FILE):
            with open(SETTINGS_FILE, 'r', encoding='utf-8') as f:
                data = json.load(f)
            if isinstance(data, dict):
                return data
    except Exception:
        logger.warning("读取设置失败，使用默认值", exc_info=True)
    return {}


def get_settings() -> Dict[str, Any]:
    """返回进程内共享的设置对象（所有调用方拿到的是同一个 dict）。"""
    global _SETTINGS_CACHE
    with _lock:
        if _SETTINGS_CACHE is None:
            _SETTINGS_CACHE = sanitize_settings(_read_disk())
        return _SETTINGS_CACHE


def load_settings() -> Dict[str, Any]:
    """向后兼容入口：等价于 get_settings()（共享同一对象）。"""
    return get_settings()


def reload_settings() -> Dict[str, Any]:
    """强制从磁盘重新加载（覆盖共享对象的内容，保持对象身份不变）。"""
    global _SETTINGS_CACHE
    with _lock:
        fresh = sanitize_settings(_read_disk())
        if _SETTINGS_CACHE is None:
            _SETTINGS_CACHE = fresh
        else:
            _SETTINGS_CACHE.clear()
            _SETTINGS_CACHE.update(fresh)
        return _SETTINGS_CACHE


def apply_settings(patch: Dict[str, Any]) -> Dict[str, Any]:
    """就地合并补丁 → 规范化 → 落盘，返回共享对象。

    这是「Web 面板改设置能立刻生效、且不会被桌面端旧表单覆盖」的关键：
    所有写入都作用在同一个 dict 上，而不是各自 load 出一份新副本。
    """
    with _lock:
        current = get_settings()
        if isinstance(patch, dict):
            current.update(patch)
        return save_settings(current)


def reset_settings() -> Dict[str, Any]:
    """恢复默认设置（就地重置共享对象）。"""
    with _lock:
        current = get_settings()
        current.clear()
        current.update(sanitize_settings(dict(DEFAULT_SETTINGS)))
        return save_settings(current)


def save_settings(settings: Dict[str, Any]) -> Dict[str, Any]:
    """规范化并原子落盘；若传入的就是共享对象，会同步被规范化。"""
    clean = sanitize_settings(settings)
    if isinstance(settings, dict) and settings is not clean:
        settings.clear()
        settings.update(clean)
    try:
        atomic_write_json(SETTINGS_FILE, clean)
    except Exception as e:
        logger.error("保存设置失败: %s", e)
    return settings if isinstance(settings, dict) else clean


def load_custom_cidrs() -> str:
    try:
        if os.path.exists(CUSTOM_CIDRS_FILE):
            with open(CUSTOM_CIDRS_FILE, 'r', encoding='utf-8') as f:
                return f.read()
    except Exception:
        pass
    return ""


def save_custom_cidrs(text: str):
    try:
        atomic_write_text(CUSTOM_CIDRS_FILE, text)
    except Exception as e:
        logger.error("保存自定义CIDR失败: %s", e)
