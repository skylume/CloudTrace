#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import ssl
import socket
import math
import time
import asyncio
import aiohttp
import logging
from typing import Optional, Tuple, Dict, List

from core.compat import IS_WIN7
from core.constants import AIRPORT_CODES, PORT_OPTIONS


logger = logging.getLogger("CloudTrace")

DEFAULT_TEST_HOST = "speed.cloudflare.com"

# Cloudflare 常用 HTTPS 端口集合：决定探测/测速时是否走 TLS
HTTPS_PORTS = {int(p) for p in PORT_OPTIONS}  # 443/2053/2083/2087/2096/8443

# 结果里保存的 trace 明细字段（顺序即展示顺序）
TRACE_DETAIL_KEYS = (
    "colo", "loc", "client_ip", "http_version", "tls_version",
    "sni", "warp", "gateway", "kex", "visit_scheme",
)


def parse_trace(body) -> Dict[str, str]:
    """解析 `/cdn-cgi/trace` 的 `key=value` 文本，返回原始键值字典。

    Cloudflare 的 trace 响应形如：
        fl=123abc
        h=speed.cloudflare.com
        ip=203.0.113.7          ← 本机出口 IP（CF 视角）
        colo=HKG                ← 承载该请求的数据中心
        loc=HK                  ← CF 判定的访客落地区域
        http=http/2
        tls=TLSv1.3
        sni=plaintext
        warp=off
        gateway=off
        rbi=off
        kex=X25519
    """
    if isinstance(body, (bytes, bytearray)):
        text = bytes(body).decode("utf-8", errors="ignore")
    else:
        text = str(body or "")
    out: Dict[str, str] = {}
    for line in text.splitlines():
        line = line.strip()
        if not line or "=" not in line:
            continue
        key, _, value = line.partition("=")
        out[key.strip().lower()] = value.strip()
    return out


def trace_to_detail(trace: Dict[str, str]) -> Dict[str, str]:
    """把 trace 原始键值映射成结果字段（缺项给空串，方便直接进表格/CSV）。"""
    trace = trace or {}
    return {
        "colo": (trace.get("colo") or "").upper(),
        "loc": (trace.get("loc") or "").upper(),
        "client_ip": trace.get("ip") or "",
        "http_version": trace.get("http") or "",
        "tls_version": trace.get("tls") or "",
        "sni": trace.get("sni") or "",
        "warp": trace.get("warp") or "",
        "gateway": trace.get("gateway") or "",
        "kex": trace.get("kex") or "",
        "visit_scheme": trace.get("visit_scheme") or "",
    }


def empty_detail() -> Dict[str, str]:
    return {k: "" for k in TRACE_DETAIL_KEYS}


def _stdev(values: List[float]) -> float:
    """样本标准差（抖动）；少于 2 个样本时返回 0。"""
    if len(values) < 2:
        return 0.0
    mean = sum(values) / len(values)
    var = sum((v - mean) ** 2 for v in values) / (len(values) - 1)
    return math.sqrt(var)


def summarize_latencies(latencies: List[float], samples: int) -> Dict:
    """把一组探测延迟汇总成 min/avg/max/loss/jitter。

    `samples` 为实际发起的探测次数（用于算丢包率）；全部失败时各项为 None。
    """
    samples = max(1, int(samples or 1))
    ok = [float(v) for v in (latencies or []) if v is not None]
    stats = {
        "samples": samples,
        "ok_count": len(ok),
        "loss": round((samples - len(ok)) / samples * 100.0, 1),
        "latency": None,
        "latency_min": None,
        "latency_avg": None,
        "latency_max": None,
        "jitter": None,
    }
    if ok:
        stats["latency_min"] = round(min(ok), 2)
        stats["latency_max"] = round(max(ok), 2)
        stats["latency_avg"] = round(sum(ok) / len(ok), 2)
        stats["jitter"] = round(_stdev(ok), 2)
        # 与历史行为一致：扫描结果里的 latency 取最小值
        stats["latency"] = stats["latency_min"]
    return stats


def port_uses_tls(port: int, default: bool = True) -> bool:
    """按端口判断是否走 TLS；非标准端口默认按调用方给的 default 处理。"""
    try:
        port = int(port)
    except (TypeError, ValueError):
        return default
    if port in HTTPS_PORTS:
        return True
    if port in (80, 8080, 8880, 2052, 2082, 2086, 2095):
        return False
    return default


def create_compat_ssl_context():
    ctx = ssl.create_default_context()
    if IS_WIN7:
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
        if hasattr(ssl, 'TLSVersion'):
            try:
                ctx.minimum_version = ssl.TLSVersion.TLSv1_2
                ctx.maximum_version = ssl.TLSVersion.TLSv1_2
            except AttributeError:
                pass
        else:
            ctx.options |= ssl.OP_NO_TLSv1_3
            ctx.options |= ssl.OP_NO_TLSv1_1
            ctx.options |= ssl.OP_NO_TLSv1
            ctx.options |= ssl.OP_NO_SSLv2
            ctx.options |= ssl.OP_NO_SSLv3
    return ctx


def create_probe_ssl_context():
    """直连裸 IP 探测用：不校验证书（SNI 与 IP 无关，校验无意义）。"""
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    if hasattr(ssl, 'TLSVersion'):
        try:
            ctx.minimum_version = ssl.TLSVersion.TLSv1_2
        except AttributeError:
            pass
    else:
        ctx.options |= ssl.OP_NO_TLSv1_3
        ctx.options |= ssl.OP_NO_TLSv1_1
        ctx.options |= ssl.OP_NO_TLSv1
    return ctx


def _connect_raw(ip: str, port: int, timeout: float):
    """按地址族建立裸 TCP 连接（支持 IPv6 字面量）。"""
    if ':' in ip:
        addrinfo = socket.getaddrinfo(ip, port, socket.AF_INET6, socket.SOCK_STREAM)
        family, socktype, proto, _canon, sockaddr = addrinfo[0]
        sock = socket.socket(family, socktype, proto)
        sock.settimeout(timeout)
        sock.connect(sockaddr)
        return sock
    return socket.create_connection((ip, port), timeout=timeout)


def _probe_trace(ip: str, timeout: float = 3, test_host: str = DEFAULT_TEST_HOST,
                 port: Optional[int] = None, use_tls: Optional[bool] = None):
    """同步请求 `/cdn-cgi/trace`。

    - 指定 port 时只探测该端口（按 port_uses_tls 决定 scheme，失败再试另一 scheme）
    - 未指定 port 时依次尝试 443(https) / 80(http)
    返回 (status, headers小写字典, body)，失败返回 (0, {}, b"")。
    """
    if port is not None:
        first_tls = port_uses_tls(port) if use_tls is None else bool(use_tls)
        candidates = [(first_tls, int(port)), (not first_tls, int(port))]
    else:
        candidates = [(True, 443), (False, 80)]

    for tls, port_num in candidates:
        s = None
        try:
            host_header = f"[{ip}]" if ':' in ip else ip
            request = (
                f"GET /cdn-cgi/trace HTTP/1.1\r\n"
                f"Host: {test_host}\r\n"
                f"User-Agent: Mozilla/5.0\r\n"
                f"Connection: close\r\n\r\n"
            ).encode()

            s = _connect_raw(ip, port_num, timeout)
            if tls:
                ctx = create_compat_ssl_context()
                s = ctx.wrap_socket(s, server_hostname=test_host)
            s.sendall(request)

            data = b""
            while b"\r\n\r\n" not in data:
                chunk = s.recv(4096)
                if not chunk:
                    break
                data += chunk

            if b"\r\n\r\n" not in data:
                continue

            header_raw, _body = data.split(b"\r\n\r\n", 1)
            status_line = header_raw.split(b"\r\n", 1)[0].decode('latin-1', errors='ignore')
            parts = status_line.split()
            status = int(parts[1]) if len(parts) >= 2 and parts[1].isdigit() else 0

            headers = {}
            for line in header_raw.split(b"\r\n")[1:]:
                if b":" in line:
                    k, v = line.split(b":", 1)
                    headers[k.strip().lower()] = v.strip().lower()

            # 小响应通常一包内；多读 0.5s 防止 body 被截断
            s.settimeout(0.5)
            deadline = time.time() + 0.5
            while time.time() < deadline and len(data) < 65536:
                try:
                    chunk = s.recv(4096)
                except socket.timeout:
                    break
                if not chunk:
                    break
                data += chunk
            body = data.split(b"\r\n\r\n", 1)[1]

            if status:
                return status, headers, body
        except Exception:
            continue
        finally:
            if s is not None:
                try:
                    s.close()
                except Exception:
                    pass
    return 0, {}, b""


async def _probe_trace_async(ip: str, timeout: float = 3,
                             test_host: str = DEFAULT_TEST_HOST,
                             port: Optional[int] = None,
                             use_tls: Optional[bool] = None):
    """`_probe_trace` 的 asyncio 版本（语义、返回结构完全一致）。

    为什么不直接用 aiohttp：aiohttp 的 SNI 只能取自 URL 里的主机名，而我们要连的
    是**裸 IP**。Cloudflare 边缘必须看到合法 SNI 才会返回 `/cdn-cgi/trace`
    （SNI 是 IP 时直接 403 或握手失败），所以只能自己开裸连接，把
    `server_hostname` 设成探测用的主机名（= 同时作为 Host 头）。

    这也是本项目扫描结果长期拿不到 colo/loc 的根因：旧实现走 aiohttp，
    永远探测失败，导致每一行的「地区」都显示为「未知地区」。
    """
    if port is not None:
        first_tls = port_uses_tls(port) if use_tls is None else bool(use_tls)
        candidates = [(first_tls, int(port)), (not first_tls, int(port))]
    else:
        candidates = [(True, 443), (False, 80)]

    loop = asyncio.get_event_loop()
    for tls, port_num in candidates:
        writer = None
        try:
            ctx = create_probe_ssl_context() if tls else None
            reader, writer = await asyncio.wait_for(
                asyncio.open_connection(ip, port_num, ssl=ctx,
                                        server_hostname=test_host if tls else None),
                timeout=timeout,
            )

            request = (
                f"GET /cdn-cgi/trace HTTP/1.1\r\n"
                f"Host: {test_host}\r\n"
                f"User-Agent: Mozilla/5.0\r\n"
                f"Connection: close\r\n\r\n"
            ).encode()
            writer.write(request)
            await writer.drain()

            data = b""
            deadline = loop.time() + timeout
            while b"\r\n\r\n" not in data:
                remain = deadline - loop.time()
                if remain <= 0:
                    break
                chunk = await asyncio.wait_for(reader.read(4096), timeout=remain)
                if not chunk:
                    break
                data += chunk
            if b"\r\n\r\n" not in data:
                continue

            header_raw = data.split(b"\r\n\r\n", 1)[0]
            status_line = header_raw.split(b"\r\n", 1)[0].decode('latin-1', errors='ignore')
            parts = status_line.split()
            status = int(parts[1]) if len(parts) >= 2 and parts[1].isdigit() else 0

            headers = {}
            for line in header_raw.split(b"\r\n")[1:]:
                if b":" in line:
                    k, v = line.split(b":", 1)
                    headers[k.strip().lower()] = v.strip().lower()

            # 小响应通常一包内；再读 0.5s 防止 body 被截断
            tail_deadline = loop.time() + 0.5
            while loop.time() < tail_deadline and len(data) < 65536:
                remain = tail_deadline - loop.time()
                if remain <= 0:
                    break
                try:
                    chunk = await asyncio.wait_for(reader.read(4096), timeout=remain)
                except asyncio.TimeoutError:
                    break
                if not chunk:
                    break
                data += chunk
            body = data.split(b"\r\n\r\n", 1)[1]

            if status:
                return status, headers, body
        except Exception:
            continue
        finally:
            if writer is not None:
                try:
                    writer.close()
                except Exception:
                    pass
    return 0, {}, b""


def get_iata_code_from_ip(ip: str, timeout: int = 3,
                          port: Optional[int] = None) -> Optional[str]:
    status, headers, body = _probe_trace(ip, timeout, port=port)
    if status != 200:
        return None

    text = body.decode('utf-8', errors='ignore')
    for line in text.splitlines():
        if line.startswith('colo='):
            colo_value = line.split('=', 1)[1].strip()
            if colo_value and colo_value.upper() != 'UNKNOWN':
                return colo_value.upper()

    cf_ray = headers.get('cf-ray', '')
    if '-' in cf_ray:
        parts = cf_ray.split('-')
        for part in parts[-2:]:
            if len(part) == 3 and part.isalpha():
                return part.upper()
    return None


def probe_cloudflare(ip: str, timeout: int = 3,
                     port: Optional[int] = None) -> Dict:
    """探测节点并返回明细，供可用性验证与结果展示复用。

    判定（对齐 cfnb `check_http_server()` 的双重校验，任一命中即通过）：
      1. HTTP 400 且 `Server` 以 `cloudflare` 开头
         —— CF 对「Host 不属于本站」的标准回应，是最干净的证据；
      2. HTTP 200 且响应体带 `colo=` 或响应头带 `CF-RAY`
         —— 探测时用的 Host 是 speed.cloudflare.com，正常情况走这条。

    注意：仅凭 `Server: cloudflare*` 但状态码既不是 200 也不是 400（例如
    403/503）不再算通过 —— 那类节点往往是「能连上但不是干净 CF 回源」，
    放进来只会污染测速结果。
    """
    status, headers, body = _probe_trace(ip, timeout, port=port)
    server = headers.get('server', '')
    ok = False
    reason = "无响应"
    if status != 0:
        if status == 400 and server.startswith('cloudflare'):
            ok, reason = True, "HTTP 400 + Cloudflare Server 头"
        elif status == 200 and (b'colo=' in body or 'cf-ray' in headers):
            ok, reason = True, "HTTP 200 + trace/CF-RAY 特征"
        elif server.startswith('cloudflare'):
            reason = f"HTTP {status} 带 Cloudflare 头但状态码非 200/400，判为可疑"
        else:
            reason = f"HTTP {status} 且无 Cloudflare 特征"
    return {"ok": ok, "status": status, "server": server, "body": body, "reason": reason}


def verify_cloudflare(ip: str, timeout: int = 3, port: Optional[int] = None) -> bool:
    """可用性验证：确认该 IP 当前确实由 Cloudflare 承载（防劫持/非CF节点）。"""
    return probe_cloudflare(ip, timeout, port=port)["ok"]


def probe_node_detail(ip: str, timeout: int = 3, port: Optional[int] = None,
                      use_tls: Optional[bool] = None) -> Dict:
    """一次探测拿到「可用性判定 + trace 明细」（测速阶段用，避免重复请求）。

    返回 {"ok", "status", "server", "reason", **TRACE_DETAIL_KEYS}
    """
    probe = probe_cloudflare(ip, timeout, port=port)
    detail = trace_to_detail(parse_trace(probe.get("body") or b""))
    out = {
        "ok": bool(probe.get("ok")),
        "status": probe.get("status", 0),
        "server": probe.get("server", ""),
        "reason": probe.get("reason", ""),
    }
    out.update(detail)
    return out


async def get_node_detail_async(session: aiohttp.ClientSession, ip: str,
                                timeout: int = 3, port: Optional[int] = None,
                                use_tls: Optional[bool] = None) -> Dict:
    """扫描阶段的节点明细：一次 `/cdn-cgi/trace` 拿到数据中心/落地区域/协议栈信息。

    返回 {"colo", "loc", "client_ip", "http_version", "tls_version", "sni",
          "warp", "gateway", "kex", "visit_scheme", "cf_ray", "status"}；
    探测失败时各字段为空串（调用方无需判空）。

    `session` 参数只为兼容既有调用方而保留 —— 实际探测走裸连接（原因见
    `_probe_trace_async` 的说明），不再复用 aiohttp 连接池。
    """
    detail = empty_detail()
    detail.update({"cf_ray": "", "status": 0})

    status, headers, body = await _probe_trace_async(
        ip, timeout, port=port, use_tls=use_tls)
    if status != 200:
        return detail

    detail.update(trace_to_detail(parse_trace(body)))
    detail["status"] = status
    detail["cf_ray"] = headers.get('cf-ray', '')
    if not detail["colo"] and detail["cf_ray"] and '-' in detail["cf_ray"]:
        for part in detail["cf_ray"].split('-')[-2:]:
            if len(part) == 3 and part.isalpha():
                detail["colo"] = part.upper()
                break
    return detail


async def get_iata_code_async(session: aiohttp.ClientSession, ip: str,
                              timeout: int = 3, port: Optional[int] = None) -> Optional[str]:
    """向后兼容入口：只取地区码。"""
    detail = await get_node_detail_async(session, ip, timeout, port=port)
    colo = detail.get("colo") or ""
    return colo.upper() if colo and colo.upper() != 'UNKNOWN' else None


def get_iata_translation(iata_code: str) -> str:
    return AIRPORT_CODES.get(iata_code, iata_code)


async def async_tcp_ping(ip: str, port: int, timeout: float = 1.0) -> Optional[float]:
    start_time = time.monotonic()
    try:
        reader, writer = await asyncio.wait_for(asyncio.open_connection(ip, port), timeout=timeout)
        latency = (time.monotonic() - start_time) * 1000
        writer.close()
        try:
            await writer.wait_closed()
        except Exception:
            pass
        return round(latency, 2)
    except Exception:
        return None


async def measure_tcp_stats(ip: str, port: int, ping_times: int = 4,
                            timeout: float = 1.0) -> Dict:
    """TCP 握手延迟统计：并发发起 `ping_times` 次探测，汇总 min/avg/max/丢包/抖动。

    `latency` 仍取最小值，与历史行为保持一致（结果页/阈值判定口径不变）。
    全部失败时 latency 为 None（调用方据此判定不可用）。
    """
    samples = max(1, int(ping_times or 1))
    tasks = [asyncio.create_task(async_tcp_ping(ip, port, timeout)) for _ in range(samples)]
    results = await asyncio.gather(*tasks, return_exceptions=True)
    latencies = [r for r in results if isinstance(r, float)]
    return summarize_latencies(latencies, samples)


async def measure_tcp_latency(ip: str, port: int, ping_times: int = 4,
                              timeout: float = 1.0) -> Optional[float]:
    """TCP 握手延迟：多次探测并发发起，取最小值。"""
    if ping_times <= 0:
        return None
    return (await measure_tcp_stats(ip, port, ping_times, timeout))["latency"]


async def measure_http_stats(session: aiohttp.ClientSession, ip: str, port: int,
                             timeout: float = 1.0, use_tls: bool = True,
                             samples: int = 1) -> Dict:
    """HTTPing 统计：并发发起 `samples` 次 TTFB 探测，汇总 min/avg/max/丢包/抖动。

    与 TCPing 保持同样的「并发探测、取最小值」语义：总耗时约等于单次探测，
    不会因为要算抖动而把扫描时间乘以样本数。
    """
    samples = max(1, int(samples or 1))
    tasks = [asyncio.create_task(measure_http_latency(session, ip, port, timeout, use_tls))
             for _ in range(samples)]
    results = await asyncio.gather(*tasks, return_exceptions=True)
    latencies = [r for r in results if isinstance(r, float)]
    return summarize_latencies(latencies, samples)


async def measure_http_latency(session: aiohttp.ClientSession, ip: str, port: int,
                               timeout: float = 1.0, use_tls: bool = True) -> Optional[float]:
    """HTTPing：测量 TTFB（含 TCP+TLS 握手到响应头到达）。"""
    brackets = f"[{ip}]" if ':' in ip else ip
    scheme = "https" if use_tls else "http"
    url = f"{scheme}://{brackets}:{port}/cdn-cgi/trace"
    headers = {"Host": DEFAULT_TEST_HOST, "User-Agent": "Mozilla/5.0"}
    ssl_ctx = create_probe_ssl_context() if use_tls else None

    start = time.monotonic()
    try:
        async with session.get(
            url, headers=headers, ssl=ssl_ctx,
            timeout=aiohttp.ClientTimeout(total=timeout),
            allow_redirects=False,
        ) as response:
            ttfb = (time.monotonic() - start) * 1000
            return round(ttfb, 2)
    except Exception:
        return None


def _decode_chunked(buf: bytes) -> Tuple[int, bytes]:
    """增量解析 chunked 编码，返回 (已解码数据字节数, 剩余未处理字节)。"""
    total = 0
    pos = 0
    while True:
        idx = buf.find(b"\r\n", pos)
        if idx == -1:
            break
        size_line = buf[pos:idx]
        if b";" in size_line:
            size_line = size_line.split(b";", 1)[0]
        try:
            size = int(size_line.strip(), 16)
        except ValueError:
            return total + len(buf) - pos, b""
        if size == 0:
            return total, buf[pos:]
        data_start = idx + 2
        if len(buf) < data_start + size + 2:
            break
        total += size
        pos = data_start + size + 2
    return total, buf[pos:]


def download_speed(ip: str, port: int, host: str = DEFAULT_TEST_HOST,
                   path: str = "/__down?bytes=50000000",
                   time_limit: float = 3.0,
                   should_continue=None,
                   use_tls: bool = True,
                   stats: Optional[Dict] = None) -> Tuple[float, Optional[str], int]:
    """向指定 IP 实测下载速度 (MB/s)。

    - `use_tls` 由调用方按端口决定：非标列表里的 `http://` 节点（如 80 端口）
      必须走明文，否则握手必然失败、速度恒为 0。
    - 校验 HTTP 状态码（非 200 记 0），并把状态码一并返回，供上层识别 429 限速。
    - 响应头收到后才开始计时（不含建连时间）
    - 支持 chunked 解码（不会把块长度算进速度）
    - 字节数异常少 / 时间异常短视为失败，防止假高速
    返回 (speed, error_message|None, http_status)。

    传入 `stats`（dict）时，会额外写入实测明细：
    `bytes` / `seconds` / `content_length` / `ttfb` / `server` / `cf_ray`。
    """
    ctx = create_probe_ssl_context() if use_tls else None
    req = (
        f"GET {path} HTTP/1.1\r\n"
        f"Host: {host}\r\n"
        "User-Agent: Mozilla/5.0\r\n"
        "Accept: */*\r\n"
        "Connection: close\r\n\r\n"
    ).encode()

    sock = None
    ss = None
    if stats is not None:
        stats.clear()
        stats.update({
            "bytes": 0, "seconds": 0.0, "content_length": "",
            "ttfb": None, "server": "", "cf_ray": "", "http_status": 0,
        })
    try:
        connect_start = time.time()
        sock = _connect_raw(ip, int(port), 3)
        if use_tls:
            ss = ctx.wrap_socket(sock, server_hostname=host)
            sock = None
        else:
            ss = sock
            sock = None
        ss.settimeout(1.0)
        if stats is not None:
            stats["connect_ms"] = round((time.time() - connect_start) * 1000, 1)
        ss.sendall(req)

        # ---- 读取响应头 ----
        header_buf = b""
        header_deadline = time.time() + 5
        while b"\r\n\r\n" not in header_buf:
            if should_continue and not should_continue():
                ss.close()
                return 0.0, "用户中止", 0
            if time.time() > header_deadline:
                ss.close()
                return 0.0, "等待响应头超时", 0
            try:
                chunk = ss.recv(4096)
            except socket.timeout:
                continue
            if not chunk:
                ss.close()
                return 0.0, "连接被关闭（无响应头）", 0
            header_buf += chunk

        header_raw, body_buf = header_buf.split(b"\r\n\r\n", 1)
        status_line = header_raw.split(b"\r\n", 1)[0].decode('latin-1', errors='ignore')
        parts = status_line.split()
        status = int(parts[1]) if len(parts) >= 2 and parts[1].isdigit() else 0
        if status != 200:
            ss.close()
            return 0.0, f"HTTP 状态码 {status}", status

        header_map = {}
        for line in header_raw.split(b"\r\n")[1:]:
            if b":" in line:
                k, v = line.split(b":", 1)
                header_map[k.strip().lower()] = v.strip().lower()
        chunked = b"chunked" in header_map.get(b"transfer-encoding", b"")
        if stats is not None:
            stats["content_length"] = header_map.get(b"content-length", b"").decode(
                "latin-1", errors="ignore")
            stats["server"] = header_map.get(b"server", b"").decode("latin-1", errors="ignore")
            stats["cf_ray"] = header_map.get(b"cf-ray", b"").decode("latin-1", errors="ignore")
            stats["http_status"] = status
            if stats.get("ttfb") is None:
                stats["ttfb"] = round((time.time() - connect_start) * 1000, 1)

        # ---- 计时下载响应体 ----
        start = time.time()
        body = 0
        buf = body_buf
        if not chunked:
            body += len(buf)
            buf = b""

        while time.time() - start < time_limit:
            if should_continue and not should_continue():
                break
            try:
                data = ss.recv(65536)
            except socket.timeout:
                continue
            if not data:
                break
            if chunked:
                buf += data
                decoded, buf = _decode_chunked(buf)
                body += decoded
            else:
                body += len(data)
        ss.close()
        ss = None

        dur = time.time() - start
        if stats is not None:
            stats["bytes"] = body
            stats["seconds"] = round(dur, 3)
        if body < 16 * 1024:
            return 0.0, "下载数据量过少", status
        if dur < 0.3 and body < 1024 * 1024:
            return 0.0, "连接过早结束", status
        return round((body / 1024 / 1024) / max(dur, 0.1), 2), None, status
    except Exception as e:
        return 0.0, str(e), 0
    finally:
        for s in (sock, ss):
            if s is not None:
                try:
                    s.close()
                except Exception:
                    pass
