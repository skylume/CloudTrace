#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import csv
import io
import json
from datetime import datetime
from typing import Dict, List, Optional


# 可导出字段 → CSV 表头
SCAN_FIELDS = {
    'ip': 'IP地址',
    'iata_code': '地区码',
    'chinese_name': '地区',
    'latency': '延迟(ms)',
    'ip_version': 'IP版本',
    'port': '端口',
    'use_tls': 'TLS',
    'scan_mode': '扫描方式',
    'scan_time': '扫描时间',
}

SPEED_FIELDS = {
    'ip': 'IP地址',
    'iata_code': '地区码',
    'chinese_name': '地区',
    'latency': '延迟(ms)',
    'download_speed': '下载速度(MB/s)',
    'score': '综合评分',
    'verified': '可用性验证',
    'port': '端口',
    'use_tls': 'TLS',
    'test_type': '测速类型',
}

EXPORT_FORMATS = ("csv", "json", "txt")


def fields_for(result_type: str) -> Dict[str, str]:
    return dict(SCAN_FIELDS) if result_type == "scan" else dict(SPEED_FIELDS)


def _select(results: List[Dict], result_type: str, fields: Optional[List[str]],
            qualified_only: bool, min_speed: float):
    data = list(results)
    if qualified_only and result_type == "speed":
        data = [r for r in data if (r.get('download_speed') or 0) >= min_speed]
    available = fields_for(result_type)
    selected = [k for k in available if fields is None or k in fields]
    return data, selected


def _ip_port(r: Dict) -> str:
    ip = str(r.get('ip', '')).strip()
    port = r.get('port')
    if ':' in ip and not ip.startswith('['):
        ip = f"[{ip}]"
    return f"{ip}:{port}" if port else ip


def render_txt(results: List[Dict], result_type: str,
               qualified_only: bool = False, min_speed: float = 0.0) -> str:
    """TXT 导出：每行一个 `ip:port`，可直接粘贴进代理客户端。

    测速结果按评分（已排序）输出，扫描结果按延迟输出。
    """
    data, _ = _select(results, result_type, None, qualified_only, min_speed)
    return "\n".join(_ip_port(r) for r in data) + ("\n" if data else "")


def render_export(results: List[Dict], result_type: str,
                  fields: Optional[List[str]] = None,
                  qualified_only: bool = False,
                  min_speed: float = 0.0,
                  fmt: str = "csv") -> str:
    """把结果渲染为 CSV/JSON/TXT 文本（供文件导出与 HTTP 下载共用）。"""
    if fmt == "txt":
        return render_txt(results, result_type, qualified_only, min_speed)

    data, selected = _select(results, result_type, fields, qualified_only, min_speed)
    available = fields_for(result_type)

    if fmt == "json":
        payload = {
            'export_time': datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
            'result_type': result_type,
            'count': len(data),
            'fields': selected,
            'results': [{k: r.get(k) for k in selected} for r in data],
        }
        return json.dumps(payload, ensure_ascii=False, indent=2)

    buf = io.StringIO()
    writer = csv.writer(buf)
    with_rank = (result_type == "speed" and 'ip' in selected)
    header = (["排名"] if with_rank else []) + [available[k] for k in selected]
    writer.writerow(header)
    for i, r in enumerate(data):
        row = [r.get(k, '') for k in selected]
        if with_rank:
            row.insert(0, i + 1)
        writer.writerow(row)
    return buf.getvalue()


def write_export(filepath: str, results: List[Dict], result_type: str,
                 fields: Optional[List[str]] = None,
                 qualified_only: bool = False,
                 min_speed: float = 0.0,
                 fmt: Optional[str] = None) -> int:
    """导出到文件。返回实际导出条数。

    fmt 为 None 时按扩展名判断（csv/json/txt）；显式传入时以 fmt 为准
    （用户在导出对话框里选了格式，但文件名后缀可能不一致）。
    """
    if fmt not in EXPORT_FORMATS:
        lowered = filepath.lower()
        if lowered.endswith('.json'):
            fmt = "json"
        elif lowered.endswith('.txt'):
            fmt = "txt"
        else:
            fmt = "csv"
    data, _ = _select(results, result_type, fields, qualified_only, min_speed)
    content = render_export(results, result_type, fields, qualified_only, min_speed, fmt)
    encoding = "utf-8-sig" if fmt == "csv" else "utf-8"
    from core.utils import atomic_write_text
    atomic_write_text(filepath, content, encoding=encoding)
    return len(data)
