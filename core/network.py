#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import ssl
import socket
import time
import asyncio
import aiohttp
import logging
from typing import Optional, Tuple, Dict

from core.compat import IS_WIN7
from core.constants import AIRPORT_CODES, PORT_OPTIONS


logger = logging.getLogger("CloudTrace")

DEFAULT_TEST_HOST = "speed.cloudflare.com"

# Cloudflare 常用 HTTPS 端口集合：决定探测/测速时是否走 TLS
HTTPS_PORTS = {int(p) for p in PORT_OPTIONS}  # 443/2053/2083/2087/2096/8443


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


async def get_iata_code_async(session: aiohttp.ClientSession, ip: str,
                              timeout: int = 3, port: Optional[int] = None) -> Optional[str]:
    test_host = DEFAULT_TEST_HOST
    if port is not None:
        brackets = f"[{ip}]" if ':' in ip else ip
        schemes = ["https", "http"] if port_uses_tls(port) else ["http", "https"]
        urls = [f"{sch}://{brackets}:{int(port)}/cdn-cgi/trace" for sch in schemes]
    elif ':' in ip:
        urls = [f"https://[{ip}]/cdn-cgi/trace", f"http://[{ip}]/cdn-cgi/trace"]
    else:
        urls = [f"https://{ip}/cdn-cgi/trace", f"http://{ip}/cdn-cgi/trace"]

    headers = {
        "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
        "Host": test_host
    }
    ssl_ctx = create_compat_ssl_context()

    for url in urls:
        try:
            use_ssl = url.startswith('https://')
            ssl_context = ssl_ctx if use_ssl else None
            async with session.get(
                url, headers=headers, ssl=ssl_context,
                timeout=aiohttp.ClientTimeout(total=timeout),
                allow_redirects=False
            ) as response:
                if response.status == 200:
                    text = await response.text()
                    for line in text.strip().split('\n'):
                        if line.startswith('colo='):
                            colo_value = line.split('=', 1)[1].strip()
                            if colo_value and colo_value.upper() != 'UNKNOWN':
                                return colo_value.upper()
                    if 'CF-RAY' in response.headers:
                        cf_ray = response.headers['CF-RAY']
                        if '-' in cf_ray:
                            parts = cf_ray.split('-')
                            for part in parts[-2:]:
                                if len(part) == 3 and part.isalpha():
                                    return part.upper()
        except Exception:
            continue
    return None


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


async def measure_tcp_latency(ip: str, port: int, ping_times: int = 4,
                              timeout: float = 1.0) -> Optional[float]:
    """TCP 握手延迟：多次探测并发发起，取最小值。"""
    if ping_times <= 0:
        return None
    tasks = [asyncio.create_task(async_tcp_ping(ip, port, timeout)) for _ in range(ping_times)]
    results = await asyncio.gather(*tasks, return_exceptions=True)
    latencies = [r for r in results if isinstance(r, float)]
    if latencies:
        return min(latencies)
    return None


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
                   use_tls: bool = True) -> Tuple[float, Optional[str], int]:
    """向指定 IP 实测下载速度 (MB/s)。

    - `use_tls` 由调用方按端口决定：非标列表里的 `http://` 节点（如 80 端口）
      必须走明文，否则握手必然失败、速度恒为 0。
    - 校验 HTTP 状态码（非 200 记 0），并把状态码一并返回，供上层识别 429 限速。
    - 响应头收到后才开始计时（不含建连时间）
    - 支持 chunked 解码（不会把块长度算进速度）
    - 字节数异常少 / 时间异常短视为失败，防止假高速
    返回 (speed, error_message|None, http_status)。
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
    try:
        sock = _connect_raw(ip, int(port), 3)
        if use_tls:
            ss = ctx.wrap_socket(sock, server_hostname=host)
            sock = None
        else:
            ss = sock
            sock = None
        ss.settimeout(1.0)
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
