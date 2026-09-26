#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""远程节点数据源：拉取 + 自适应解析。

参考 cfnb 的 `ADDITIONAL_SOURCES`（多 URL + 每项 enabled 开关 +
`FETCH_MAX_RETRIES` / `FETCH_RETRY_DELAY` / `FETCH_TIMEOUT`）与
`parse_adaptive()`（JSON / `IP:PORT#备注` / 带地区码 / 中文名 / emoji 国旗全兼容）。

对外接口：
    DEFAULT_SOURCES                   内置默认源（可在设置里改）
    fetch_sources(...)                逐个拉取并解析，返回 (entries, report)
    parse_adaptive(text, port)        从任意文本/JSON 中抽出节点
    parse_source_lines(text)          把设置里的「每行一个 URL」文本转成源列表
    format_sources_text(sources)      反向：源列表 → 文本（供 UI 编辑）
"""

import re
import json
import time
import logging
import ipaddress
from typing import Dict, List, Optional, Tuple

import requests


logger = logging.getLogger("CloudTrace")

DEFAULT_PORT = 443
MAX_SOURCE_BYTES = 8 * 1024 * 1024

# 内置默认源（来自 cfnb 的 config.json；默认「不启用」由上层开关控制）
DEFAULT_SOURCES: List[Dict] = [
    {"name": "cfnb 聚合列表", "url": "https://zip.cm.edu.kg/all.txt", "enabled": True},
    {"name": "countrymerge 聚合列表",
     "url": "https://countrymerge.pages.dev/all.txt", "enabled": True},
]

# 从一行「脏文本」里找端点：支持 [v6]:port / v4:port / 裸 v6
_ENDPOINT_RE = re.compile(
    r'\[([0-9a-fA-F:]{2,45})\](?::(\d{1,5}))?'
    r'|((?:\d{1,3}\.){3}\d{1,3})(?::(\d{1,5}))?'
    r'|((?:[0-9a-fA-F]{1,4}:){2,}[0-9a-fA-F]{1,4})'
)

_TAIL_PORT_RE = re.compile(r'^\s*[:#|,;]\s*(\d{2,5})\b')
_TAIL_SPACE_PORT_RE = re.compile(r'^\s+(\d{2,5})\b')


def _coerce_port(value, default: int = DEFAULT_PORT) -> int:
    try:
        port = int(str(value).strip())
    except (TypeError, ValueError):
        return default
    return port if 1 <= port <= 65535 else default


def _entry_for(ip: str, port, default_port: int) -> Optional[Dict]:
    try:
        addr = ipaddress.ip_address(ip.strip())
    except ValueError:
        return None
    return {
        "ip": str(addr),
        "port": _coerce_port(port, default_port),
        "ip_version": addr.version,
    }


def extract_endpoints(text: str, default_port: int = DEFAULT_PORT) -> List[Dict]:
    """从任意一行文本里抽出 IP 端点（容忍前后杂质、地区码、emoji、备注）。

    例：
        `1.2.3.4:8443#香港`
        `🇭🇰 HKG 1.2.3.4 8443`
        `[2606:4700::1111]:8443`
        `"ip": "1.2.3.4", "port": 2053`（JSON 里也会走这里兜底）
    """
    out: List[Dict] = []
    for match in _ENDPOINT_RE.finditer(text or ""):
        ip = match.group(1) or match.group(3) or match.group(5)
        port = match.group(2) or match.group(4)
        if not ip:
            continue
        if port is None:
            tail = (text or "")[match.end():match.end() + 12]
            tail_match = _TAIL_PORT_RE.match(tail) or _TAIL_SPACE_PORT_RE.match(tail)
            if tail_match:
                port = tail_match.group(1)
        entry = _entry_for(ip, port, default_port)
        if entry:
            out.append(entry)
    return out


def _walk_json(node, out: List[Dict], default_port: int):
    """递归遍历任意 JSON 结构，抽出 ip/port 字段或字符串里的端点。"""
    if isinstance(node, str):
        out.extend(extract_endpoints(node, default_port))
        return
    if isinstance(node, (list, tuple)):
        for item in node:
            _walk_json(item, out, default_port)
        return
    if isinstance(node, dict):
        raw_ip = (node.get("ip") or node.get("address") or node.get("host")
                  or node.get("server") or node.get("node"))
        if isinstance(raw_ip, str) and raw_ip.strip():
            entry = _entry_for(raw_ip, node.get("port") or node.get("Port"), default_port)
            if entry:
                out.append(entry)
                return
        for value in node.values():
            _walk_json(value, out, default_port)


def parse_adaptive(text: str, default_port: int = DEFAULT_PORT) -> Tuple[List[Dict], List[str]]:
    """自适应解析数据源内容 → (entries, errors)。

    先尝试 JSON（兼容 `[{"ip":..,"port":..}]`、`{"data":[...]}` 等任意嵌套），
    失败或没解析出节点时，退回「逐行正则抽取」，因此对纯文本、
    `IP:PORT#备注`、带 emoji 国旗与中文地区名的列表都能吃下。
    """
    entries: List[Dict] = []
    errors: List[str] = []
    if not text or not text.strip():
        return entries, ["内容为空"]

    data = None
    stripped = text.lstrip()
    if stripped[:1] in ("{", "["):
        try:
            data = json.loads(text)
        except Exception:
            data = None
    if data is not None:
        _walk_json(data, entries, default_port)

    if not entries:
        for line in text.splitlines():
            line = line.strip()
            if not line or line.startswith('#'):
                continue
            if '#' in line:
                line = line.split('#', 1)[0]
            entries.extend(extract_endpoints(line, default_port))

    deduped: List[Dict] = []
    seen = set()
    for entry in entries:
        key = (entry["ip"], entry["port"])
        if key in seen:
            continue
        seen.add(key)
        deduped.append(entry)

    if not deduped:
        errors.append("未从内容中解析出任何 IP")
    return deduped, errors


def parse_source_lines(text: str) -> List[Dict]:
    """把设置里的「每行一个源」文本转成源列表。

    每行格式：`URL` 或 `名称 | URL`；以 `#` 开头的行表示禁用。
    """
    sources: List[Dict] = []
    for raw in (text or "").splitlines():
        line = raw.strip()
        if not line:
            continue
        enabled = True
        if line.startswith('#'):
            enabled = False
            line = line[1:].strip()
        if not line:
            continue
        if '|' in line:
            name, _, url = line.partition('|')
            name, url = name.strip(), url.strip()
        else:
            name, url = "", line
        if not url.lower().startswith(("http://", "https://")):
            continue
        sources.append({"name": name or url, "url": url, "enabled": enabled})
    return sources


def format_sources_text(sources) -> str:
    """源列表 → 文本（供 UI 文本框编辑；禁用项前面加 #）。"""
    lines = []
    for src in sources or []:
        if not isinstance(src, dict):
            continue
        url = str(src.get("url") or "").strip()
        if not url:
            continue
        name = str(src.get("name") or "").strip()
        body = f"{name} | {url}" if name and name != url else url
        lines.append(body if src.get("enabled", True) else f"# {body}")
    return "\n".join(lines)


def sanitize_sources(raw) -> List[Dict]:
    """把任意来源（设置文件 / Web 表单）规范化为合法的源列表。"""
    if isinstance(raw, str):
        return parse_source_lines(raw)
    if not isinstance(raw, (list, tuple)):
        return []
    out: List[Dict] = []
    for item in raw:
        if isinstance(item, str):
            out.extend(parse_source_lines(item))
            continue
        if not isinstance(item, dict):
            continue
        url = str(item.get("url") or "").strip()
        if not url.lower().startswith(("http://", "https://")):
            continue
        out.append({
            "name": str(item.get("name") or url).strip() or url,
            "url": url,
            "enabled": bool(item.get("enabled", True)),
        })
    return out


def _fetch_one(url: str, timeout: float, retries: int, delay: float) -> Tuple[Optional[str], Optional[str]]:
    """带重试地拉取单个 URL；返回 (text, error)。"""
    last_error = None
    attempts = max(1, int(retries))
    for attempt in range(1, attempts + 1):
        try:
            resp = requests.get(
                url,
                timeout=timeout,
                headers={"User-Agent": "CloudTrace/1.0"},
            )
            if resp.status_code != 200:
                last_error = f"HTTP {resp.status_code}"
            else:
                resp.encoding = resp.encoding or "utf-8"
                return resp.text[:MAX_SOURCE_BYTES], None
        except Exception as e:
            last_error = str(e)
        if attempt < attempts:
            time.sleep(max(0.0, delay))
    return None, last_error or "未知错误"


def fetch_sources(sources, default_port: int = DEFAULT_PORT,
                  timeout: float = 8.0, retries: int = 3,
                  delay: float = 3.0) -> Tuple[List[Dict], List[str]]:
    """逐个拉取启用的数据源并解析，返回 (entries, report_lines)。

    任一源失败只记录报告，不影响其它源；全部失败则 entries 为空。
    """
    entries: List[Dict] = []
    report: List[str] = []
    seen = set()
    enabled = [s for s in sanitize_sources(sources) if s.get("enabled")]
    if not enabled:
        return entries, ["未启用任何远程数据源"]

    for src in enabled:
        url = src["url"]
        text, error = _fetch_one(url, timeout, retries, delay)
        if text is None:
            report.append(f"数据源 {src['name']} 拉取失败（重试 {retries} 次）: {error}")
            continue
        parsed, errors = parse_adaptive(text, default_port)
        if not parsed:
            report.append(f"数据源 {src['name']} 解析为空: {'; '.join(errors[:2])}")
            continue
        added = 0
        for entry in parsed:
            key = (entry["ip"], entry["port"])
            if key in seen:
                continue
            seen.add(key)
            entries.append(entry)
            added += 1
        report.append(f"数据源 {src['name']} 解析出 {len(parsed)} 条，新增 {added} 条")

    return entries, report


def resolve_source_entries(settings: Dict, default_port: int = DEFAULT_PORT,
                           log=None) -> Tuple[List[Dict], List[str]]:
    """给扫描流程用的便捷入口：读设置 → 拉取 → 返回 (entries, 报告)。

    未开启远程数据源时返回空列表，不产生任何网络请求。
    """
    if not settings.get("use_remote_sources"):
        return [], []
    sources = sanitize_sources(settings.get("remote_sources") or DEFAULT_SOURCES)
    if not sources:
        return [], ["未配置任何远程数据源"]
    entries, report = fetch_sources(
        sources,
        default_port=default_port,
        timeout=_safe_float(settings.get("source_timeout"), 8.0),
        retries=_safe_int(settings.get("source_retries"), 3),
        delay=_safe_float(settings.get("source_retry_delay"), 3.0),
    )
    if log:
        for line in report:
            log(line)
    return entries, report


def _safe_int(value, default: int) -> int:
    try:
        return int(value)
    except (TypeError, ValueError):
        return default


def _safe_float(value, default: float) -> float:
    try:
        return float(value)
    except (TypeError, ValueError):
        return default


def make_fetcher(settings: Dict, default_port: int = DEFAULT_PORT):
    """构造一个无参回调，交给扫描器在工作线程里调用。

    返回 None 表示未启用远程数据源，扫描器会跳过这一步。
    """
    if not settings.get("use_remote_sources"):
        return None

    snapshot = dict(settings)

    def _fetch():
        return resolve_source_entries(snapshot, default_port)

    return _fetch
