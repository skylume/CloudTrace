#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""测速地址：预设 + 解析（含 `auto` 的真实实现：ISP 探测选源）。

对齐 CFData-WEB `speed_url.go` 的三条内置地址：

    cloudflareSpeedURL      speed.cloudflare.com/__down?bytes=99999999
    cmSpeedURL              cf.090227.xyz/__down?bytes=99999999
    mobileDedicatedSpeedURL speed.okl.abrdns.com

`auto` = 先探测出口 ISP（`https://cf.090227.xyz/cf.json` 的 asn / asOrganization），
命中中国移动特征时从 [cmSpeedURL, mobileDedicatedSpeedURL] 里**随机**取一个，
否则回退 Cloudflare 官方源。

与参考实现的一处有意偏差：参考实现把「没有路径」的地址补成 `/`，本实现补成
CF 标准的 `/__down?bytes=99999999`。理由是前者对普通主机只会取到站点首页 HTML，
既不是测速端点、还会让测速结果变成毫无意义的极小值。
"""

import time
import random
import logging
from typing import List, Optional, Tuple

import requests


logger = logging.getLogger("CloudTrace")

# ---------------- 预设地址（下拉选项用） ----------------
AUTO_SPEED_URL = "auto"
AUTO_SPEED_LABELS = ("auto", "自动", "自动选择", "自动测速地址")

CF_SPEED_URL = "speed.cloudflare.com/__down?bytes=99999999"
CM_SPEED_URL = "cf.090227.xyz/__down?bytes=99999999"
MOBILE_DEDICATED_SPEED_URL = "speed.okl.abrdns.com"

DEFAULT_SPEED_BYTES = 99999999
DEFAULT_SPEED_HOST = "speed.cloudflare.com"
DEFAULT_SPEED_PATH = f"/__down?bytes={DEFAULT_SPEED_BYTES}"

# 中国移动友好测速源（与参考实现一致）
CM_SPEED_HOSTS = ("cf.090227.xyz", "speed.okl.abrdns.com")

# 预设测速地址：(显示名, 值)。值 == "__custom__" 表示交给用户手填。
CUSTOM_SPEED_URL = "__custom__"
SPEED_URL_PRESETS: Tuple[Tuple[str, str], ...] = (
    ("自动选择（按出口 ISP 智能切换）", AUTO_SPEED_URL),
    ("Cloudflare 官方", CF_SPEED_URL),
    ("移动友好 (cf.090227.xyz)", CM_SPEED_URL),
    ("移动专属 (speed.okl.abrdns.com)", MOBILE_DEDICATED_SPEED_URL),
    ("自定义…", CUSTOM_SPEED_URL),
)
PRESET_VALUES = tuple(value for _label, value in SPEED_URL_PRESETS)

# ISP 探测端点：返回 {"asn": 9808, "asOrganization": "China Mobile ..."}
ISP_PROBE_URL = "https://cf.090227.xyz/cf.json"
CM_ORG_KEYWORDS = ("cmi", "cmnet", "chinamobile", "china mobile", "cmcc",
                   "mobile communications", "移动")
CM_ASNS = {9808, 24400, 56040, 56041, 56044}

_CACHE_TTL = 600.0
_cache: Optional[Tuple[float, str, str]] = None   # (timestamp, host, path)


def is_auto_speed_url(value: Optional[str]) -> bool:
    """空值或 auto / 自动选择 等写法都视为「自动」。"""
    text = (value or "").strip()
    if not text:
        return True
    return text.lower() in AUTO_SPEED_LABELS or text in AUTO_SPEED_LABELS


def preset_value_to_url(value: Optional[str]) -> str:
    """把下拉框的预设值转成实际地址；自定义/未知值原样返回。"""
    text = (value or "").strip()
    if text == CUSTOM_SPEED_URL or text in ("manual", "手动输入"):
        return ""
    return text


def url_to_preset_value(url: Optional[str]) -> str:
    """把已保存的地址反查为下拉框预设值；不在预设内则返回「自定义」。"""
    text = (url or "").strip()
    if is_auto_speed_url(text):
        return AUTO_SPEED_URL
    return text if text in PRESET_VALUES else CUSTOM_SPEED_URL


def _detect_china_mobile(timeout: float = 4.0) -> bool:
    """探测本机出口是否为中国移动宽带；任何异常都返回 False。"""
    try:
        resp = requests.get(
            ISP_PROBE_URL,
            timeout=timeout,
            headers={"User-Agent": "CloudTrace/1.0"},
        )
        resp.raise_for_status()
        data = resp.json()
    except Exception as e:
        logger.info("ISP 探测失败，回退默认测速源: %s", e)
        return False

    if not isinstance(data, dict):
        return False

    org = str(data.get("asOrganization") or data.get("org") or "").lower()
    if any(kw in org for kw in CM_ORG_KEYWORDS):
        return True

    try:
        asn = int(data.get("asn") or 0)
    except (TypeError, ValueError):
        asn = 0
    return asn in CM_ASNS


def resolve_auto_speed_source(timeout: float = 4.0, force: bool = False) -> Tuple[str, str]:
    """解析 `auto` 测速源，返回 (host, path)。

    移动出口时在两条移动友好源里随机取一个（与参考实现 `pickMobileSpeedURL` 一致）。
    """
    global _cache
    now = time.time()
    if not force and _cache and (now - _cache[0]) < _CACHE_TTL:
        return _cache[1], _cache[2]

    host, path = DEFAULT_SPEED_HOST, DEFAULT_SPEED_PATH
    if _detect_china_mobile(timeout):
        host = random.choice(CM_SPEED_HOSTS)
        logger.info("检测到中国移动出口，测速源切换为 %s", host)

    _cache = (now, host, path)
    return host, path


def parse_speed_url(url: str) -> Tuple[str, str, bool]:
    """把用户填写的测速地址解析为 (host, path, use_tls)。

    - 空 / `auto` / `自动选择` → ISP 探测选源，use_tls=True
    - 显式带 `http://` → use_tls=False；`https://` 或无 scheme → use_tls=True
    - `//host/path` 这种省略 scheme 的写法按 https 处理
    - 没有路径时补 CF 标准测速端点 `/__down?bytes=99999999`
    - 路径里缺 `bytes=` 时补上，保证下载量足够大（默认 99999999 字节）
    """
    raw = (url or "").strip()
    if is_auto_speed_url(raw):
        host, path = resolve_auto_speed_source()
        return host, path, True

    lowered = raw.lower()
    use_tls = True
    if lowered.startswith("//"):
        raw = raw[2:]
    elif lowered.startswith("https://"):
        raw = raw[8:]
    elif lowered.startswith("http://"):
        raw = raw[7:]
        use_tls = False

    host, sep, rest = raw.partition("/")
    host = host.strip()
    path = ("/" + rest) if sep else ""
    path = path.strip()
    if not path or path == "/":
        path = DEFAULT_SPEED_PATH
    if "bytes=" not in path:
        path += ("&" if "?" in path else "?") + f"bytes={DEFAULT_SPEED_BYTES}"
    return (host or DEFAULT_SPEED_HOST), path, use_tls


def list_presets() -> List[dict]:
    """给 UI 用的预设清单（含 value/label），自定义项也一并返回。"""
    return [{"label": label, "value": value} for label, value in SPEED_URL_PRESETS]


def reset_cache():
    """清空 ISP 探测缓存（测试用）。"""
    global _cache
    _cache = None
