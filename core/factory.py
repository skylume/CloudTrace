#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import ipaddress
from typing import Dict, List, Optional, Tuple

from core.scanner import IPv4Scanner, IPv6Scanner, ImportedScanner, SpeedTestTask


def parse_cidr_lines(lines: List[str], ip_version: int) -> Tuple[List[str], List[Tuple[int, str]]]:
    """校验 CIDR 行列表。返回 (有效CIDR, [(行号, 原文)...无效])。"""
    valid: List[str] = []
    errors: List[Tuple[int, str]] = []
    for i, line in enumerate(lines, 1):
        line = (line or "").strip()
        if not line:
            continue
        try:
            net = ipaddress.ip_network(line, strict=False)
            if net.version == ip_version:
                valid.append(line)
        except ValueError:
            errors.append((i, line))
    return valid, errors


def create_scanner(params: Dict) -> object:
    """按统一参数字典构建扫描器（Qt 与 HTTP API 共用）。

    params: {
        ip_version, source_mode(仅官方/仅自定义/官方+自定义),
        cidrs[](网段，采样), entries[](逐条直测节点),
        port, workers, threshold, sample_max, ping_times(0=自动),
        scan_mode(tcping/httping), pre_filter_ports, remote_fetch(callable|None)
    }
    """
    from core.utils import to_int
    common = dict(
        port=to_int(params.get("port", 443), 443, 1, 65535),
        max_workers=to_int(params.get("workers", 200), 200, 1, 2000),
        latency_threshold=to_int(params.get("threshold", 230), 230, 1, 60000),
        sample_max=to_int(params.get("sample_max", 5000), 5000, 1, 200000),
        scan_mode=params.get("scan_mode", "tcping"),
        pre_filter_ports=params.get("pre_filter_ports") or [],
        remote_fetch=params.get("remote_fetch"),
    )
    ping_times = to_int(params.get("ping_times", 0), 0, 0, 20)
    if ping_times > 0:
        common["ping_times"] = ping_times

    source_mode = params.get("source_mode", "仅官方")
    if source_mode not in ("仅官方", "仅自定义", "官方+自定义"):
        source_mode = "仅官方"
    ip_version = to_int(params.get("ip_version", 4), 4)
    cidrs = [c for c in (params.get("cidrs") or []) if c]
    entries = list(params.get("entries") or [])

    if source_mode == "仅官方":
        cls = IPv4Scanner if ip_version == 4 else IPv6Scanner
        return cls(**common)

    if not cidrs and entries:
        # 纯直测列表：不采样，允许 v4/v6 混合
        return ImportedScanner(entries=entries, **common)

    custom = {"mode": source_mode, "list": cidrs}
    cls = IPv4Scanner if ip_version == 4 else IPv6Scanner
    return cls(custom_cidrs=custom, custom_entries=entries, **common)


def create_speed_task(scan_results: List[Dict], opts: Dict, settings: Dict) -> SpeedTestTask:
    """按统一参数字典构建测速任务（Qt 与 HTTP API 共用）。

    opts: {
        region_code, selected_ips[], count, current_port,
        speed_url, min_speed, label
    }
    settings: 应用设置 dict（评分权重/验证开关/间隔/并发/结果上限/分地区TopN）
    """
    from core.utils import to_int, to_float
    return SpeedTestTask(
        scan_results,
        region_code=opts.get("region_code"),
        max_test_count=to_int(opts.get("count", 10), 10, 1, 500),
        current_port=to_int(opts.get("current_port", 443), 443, 1, 65535),
        speed_url=opts.get("speed_url") or "auto",
        min_speed=to_float(opts.get("min_speed") or 0, 0.0),
        selected_ips=opts.get("selected_ips"),
        verify_nodes=bool(settings.get("verify_nodes", True)),
        score_weights={
            "speed": to_float(settings.get("score_speed_weight", 3.0), 3.0),
            "latency": to_float(settings.get("score_latency_weight", 3.0), 3.0),
        },
        download_interval=to_int(settings.get("download_interval", 3), 3, 0, 60),
        label=opts.get("label"),
        speed_workers=to_int(settings.get("speed_workers", 1), 1, 1, 16),
        result_limit=to_int(settings.get("speed_result_limit", 0), 0, 0, 10000),
        per_region_topn=to_int(settings.get("per_region_topn", 0), 0, 0, 100),
    )
