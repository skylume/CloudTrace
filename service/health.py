#!/usr/bin/env python3
# -*- coding: utf-8 -*-

from typing import Dict, List

from core.scanner import DEFAULT_SAMPLE_MAX, HTTPING_SCALE_TLS
from core.utils import to_float, to_int


def validate_settings(settings: Dict) -> List[str]:
    """配置体检：返回人类可读的警告清单（空列表 = 无问题）。

    所有取值都经过 to_int/to_float 兜底，settings 里出现 null / 字符串也不会抛异常。
    """
    warnings: List[str] = []

    threshold = to_int(settings.get("latency_threshold"), 230)
    if threshold < 80:
        warnings.append(f"延迟阈值 {threshold}ms 过低，可能扫不出任何 IP，建议 150~300")

    workers = to_int(settings.get("workers"), 200)
    if workers > 400:
        warnings.append(f"并发数 {workers} 较高，低配机或家庭路由可能丢包/端口耗尽，建议 ≤300")

    sample_max = to_int(settings.get("sample_max"), DEFAULT_SAMPLE_MAX)
    if sample_max > 20000:
        warnings.append(f"采样上限 {sample_max} 过大，扫描耗时会明显变长")
    if sample_max < 50:
        warnings.append(f"采样上限 {sample_max} 太小，可能没有可用结果")

    if settings.get("scan_mode") == "httping" and threshold * HTTPING_SCALE_TLS > 2000:
        warnings.append("HTTPing 模式下阈值换算后超过 2000ms，筛选形同虚设，建议调低基础阈值")

    w_speed = to_float(settings.get("score_speed_weight"), 0.0)
    w_lat = to_float(settings.get("score_latency_weight"), 0.0)
    if w_speed == 0 and w_lat == 0:
        warnings.append("评分权重全为 0，综合评分将失去意义")

    if settings.get("http_enabled") and settings.get("allow_lan") and not str(settings.get("http_token") or "").strip():
        warnings.append("HTTP 面板已允许局域网访问但未设置 Token，任何内网设备都能操控扫描任务，建议设置 Token")

    port = to_int(settings.get("http_port"), 17443)
    if not (1 <= port <= 65535):
        warnings.append(f"HTTP 服务端口 {port} 无效")

    min_speed = to_float(settings.get("min_speed"), 0.0)
    if min_speed > 50:
        warnings.append(f"测速阈值 {min_speed} MB/s 过高，绝大多数节点会被筛掉")

    speed_workers = to_int(settings.get("speed_workers"), 1)
    if speed_workers > 5:
        warnings.append(f"测速并发 {speed_workers} 过高，可能触发 Cloudflare 限速并让速度读数虚低，建议 1~3")

    if settings.get("use_remote_sources"):
        from core.sources import sanitize_sources
        enabled = [s for s in sanitize_sources(settings.get("remote_sources")) if s.get("enabled")]
        if not enabled:
            warnings.append("已启用远程数据源，但没有任何一个源处于启用状态")
        else:
            retries = to_int(settings.get("source_retries"), 3)
            timeout = to_float(settings.get("source_timeout"), 8.0)
            if retries * timeout > 60:
                warnings.append(
                    f"远程数据源最坏情况会等待约 {int(retries * timeout)}s（重试 {retries} × 超时 {timeout}s），"
                    "建议缩短超时或减少重试次数")

    topn = to_int(settings.get("per_region_topn"), 0)
    if topn > 20:
        warnings.append(f"分地区 TopN = {topn} 偏大，测速耗时会明显变长")

    return warnings
