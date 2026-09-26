#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import os
import re
import socket
import ipaddress
from typing import List, Tuple, Dict, Optional


MAX_DOMAIN_IPS = 50
# IP 段（a-b）规模不超过该值时直接展开成逐条直测，超过则折算为 CIDR 走采样
MAX_RANGE_EXPAND = 1024


def _strip_scheme(token: str) -> Tuple[str, Optional[bool]]:
    """剥离 http:// / https:// 前缀，返回 (剩余部分, use_tls|None)。"""
    lowered = token.lower()
    if lowered.startswith("https://"):
        return token[8:], True
    if lowered.startswith("http://"):
        return token[7:], False
    return token, None


def _split_host_port(token: str, default_port: int):
    """把单个 token 拆成 (host, port, explicit_port)。"""
    if token.startswith('['):
        end = token.find(']')
        if end == -1:
            return None, None, False
        host = token[1:end]
        rest = token[end + 1:]
        if not rest:
            return host, default_port, False
        if rest.startswith(':'):
            port_str = rest[1:].strip()
            if port_str.isdigit():
                return host, int(port_str), True
        return None, None, False

    if token.count(':') == 1:
        head, tail = token.split(':')
        if tail.isdigit() and head:
            return head, int(tail), True
    return token, default_port, False


def _resolve_host(host: str, port: int) -> List[Tuple[str, int]]:
    """把域名解析为 [(ip, version)]；解析失败返回 []。"""
    out: List[Tuple[str, int]] = []
    try:
        infos = socket.getaddrinfo(host, port, proto=socket.IPPROTO_TCP)
    except Exception:
        return out
    seen = set()
    for info in infos:
        sockaddr = info[4]
        ip = sockaddr[0]
        if ip in seen:
            continue
        seen.add(ip)
        try:
            version = ipaddress.ip_address(ip).version
        except ValueError:
            continue
        out.append((ip, version))
        if len(out) >= MAX_DOMAIN_IPS:
            break
    return out


def parse_ip_list(text: str, default_port: int = 443,
                  resolve_domains: bool = True) -> Tuple[List[Dict], List[str]]:
    """解析非标 IP / 域名列表。

    支持格式（每行一个）：
        1.2.3.4                     纯 IPv4
        1.2.3.4:8443                带端口
        1.2.3.4 8443                空格分隔端口
        2606:4700::1111             纯 IPv6
        [2606:4700::1111]:8443      IPv6 + 端口
        example.com                 域名（自动解析为 IP，最多 50 个）
        https://example.com:8443/   带 scheme（决定是否走 TLS）
        1.2.3.4:8443 # 香港          行尾注释（# 之后忽略）

    返回 (entries, errors)；entries: [{'ip','port','ip_version','use_tls'}]
    """
    entries: List[Dict] = []
    errors: List[str] = []
    seen = set()

    for lineno, raw in enumerate(text.splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith('#'):
            continue
        # 行尾注释
        if '#' in line:
            line = line.split('#', 1)[0].strip()
        if not line:
            continue

        body, use_tls = _strip_scheme(line)
        body = body.strip()
        if not body:
            errors.append(f"第{lineno}行: 内容为空 \"{raw.strip()}\"")
            continue

        # 去掉 URL 的路径部分（域名形式才需要）
        if '/' in body:
            body = body.split('/', 1)[0]

        parts = body.split()
        if len(parts) > 3:
            errors.append(f"第{lineno}行: 格式无效 \"{raw.strip()}\"")
            continue

        if len(parts) == 1:
            host, port, explicit = _split_host_port(parts[0], default_port)
        elif len(parts) == 2:
            host, port, explicit = _split_host_port(parts[0], default_port)
            if parts[1].isdigit():
                port, explicit = int(parts[1]), True
            elif host is None:
                errors.append(f"第{lineno}行: 端口无效 \"{raw.strip()}\"")
                continue
        else:
            host, port, explicit = _split_host_port(parts[0], default_port)
            if parts[1].isdigit():
                port, explicit = int(parts[1]), True
            elif host is None:
                errors.append(f"第{lineno}行: 端口无效 \"{raw.strip()}\"")
                continue

        if host is None:
            errors.append(f"第{lineno}行: 格式无效 \"{raw.strip()}\"")
            continue
        if not (1 <= int(port) <= 65535):
            errors.append(f"第{lineno}行: 端口超出范围 \"{raw.strip()}\"")
            continue

        # 先按字面 IP 解析
        try:
            addr = ipaddress.ip_address(host)
            resolved = [(str(addr), addr.version)]
        except ValueError:
            if not resolve_domains:
                errors.append(f"第{lineno}行: 不是有效 IP \"{raw.strip()}\"")
                continue
            resolved = _resolve_host(host, int(port))
            if not resolved:
                errors.append(f"第{lineno}行: 无法解析主机 \"{host}\"")
                continue

        for ip_str, version in resolved:
            key = (ip_str, int(port))
            if key in seen:
                continue
            seen.add(key)
            entry = {
                'ip': ip_str,
                'port': int(port),
                'ip_version': version,
            }
            if use_tls is not None:
                entry['use_tls'] = use_tls
            entries.append(entry)

    return entries, errors


def load_entries_from_file(filepath: str, default_port: int = 443) -> Tuple[List[Dict], List[str]]:
    """从 txt/csv 文件读取并解析（逗号视为分隔符）。"""
    try:
        if not os.path.exists(filepath):
            return [], [f"文件不存在: {filepath}"]
        with open(filepath, 'r', encoding='utf-8-sig', errors='ignore') as f:
            text = f.read().replace(',', ' ')
        return parse_ip_list(text, default_port)
    except Exception as e:
        return [], [f"读取文件失败: {e}"]


def load_source_text_from_file(filepath: str) -> Tuple[str, Optional[str]]:
    """读取「自定义来源」文本文件（保留原始行，交给 parse_source_text 分类）。"""
    try:
        if not os.path.exists(filepath):
            return "", f"文件不存在: {filepath}"
        with open(filepath, 'r', encoding='utf-8-sig', errors='ignore') as f:
            return f.read(), None
    except Exception as e:
        return "", f"读取文件失败: {e}"


def _classify_ip_range(body: str, ip_version: int):
    """识别 `1.2.3.4-1.2.3.20` / `a~b` 形式的 IP 段。

    返回 (addr_a, addr_b) 或 None；两端必须是同族合法 IP，
    否则（例如域名里的连字符 `my-site.com`）返回 None 交给后续分支处理。
    """
    for sep in ('-', '~'):
        if sep not in body:
            continue
        left, _, right = body.partition(sep)
        left, right = left.strip(), right.strip()
        if not left or not right:
            continue
        try:
            a = ipaddress.ip_address(left)
            b = ipaddress.ip_address(right)
        except ValueError:
            continue
        if a.version != b.version:
            continue
        if a.version != ip_version:
            return ("version", a.version)
        if int(a) > int(b):
            a, b = b, a
        return (a, b)
    return None


def parse_source_text(text: str, default_port: int = 443, ip_version: int = 4,
                      resolve_domains: bool = True) -> Tuple[List[str], List[Dict], List[str], Dict]:
    """解析「自定义来源」文本，返回 (cidrs, entries, errors, stats)。

    每行按内容自动分类，因此同一个输入框可以混着写：

        CIDR           1.2.3.0/24         → cidrs（扫描时按采样密度抽 IP）
        IPv6 CIDR      2606:4700::/32     → cidrs
        单个 IP        1.2.3.4            → entries（逐条直测）
        IP + 端口      1.2.3.4:8443       → entries
        IPv6 + 端口    [2606:4700::1]:8443 → entries
        空格分隔端口   1.2.3.4 8443        → entries
        IP 段          1.2.3.4-1.2.3.20   → 规模 ≤ 1024 展开为 entries，否则折算 CIDR
        域名           example.com         → entries（getaddrinfo 解析，最多 50 个）
        带 scheme      https://example.com:8443/  → entries（scheme 决定是否走 TLS）
        注释           # 整行 或 行尾 # 注释

    与 `parse_ip_list` 的区别：本函数**同时**接受网段与逐条节点，并保证
    同一个 IP 只出现一次（网段与直测互不重复）。
    """
    cidrs: List[str] = []
    entries: List[Dict] = []
    errors: List[str] = []
    # skipped：与所选 IP 版本不符而忽略的行（非致命，交给 UI 作为提示而非报错）
    stats = {"cidr": 0, "entry": 0, "domain": 0, "range": 0,
             "ignored": 0, "skipped": 0, "notes": []}

    seen_cidr = set()
    seen_entry = set()

    for lineno, raw in enumerate(text.splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith('#'):
            continue
        if '#' in line:
            line = line.split('#', 1)[0].strip()
        if not line:
            continue

        body, _use_tls = _strip_scheme(line)
        body = body.strip()
        if not body:
            errors.append(f"第{lineno}行: 内容为空 \"{raw.strip()}\"")
            continue

        # ---- 1) 纯 CIDR（无 scheme 且形如 a/b） ----
        if _use_tls is None and '/' in body:
            try:
                net = ipaddress.ip_network(body, strict=False)
            except ValueError:
                net = None
            if net is not None:
                if net.version != ip_version:
                    stats["skipped"] += 1
                    stats["notes"].append(
                        f"第{lineno}行: {body} 不是 IPv{ip_version} 网段，已忽略")
                elif body in seen_cidr:
                    stats["ignored"] += 1
                else:
                    seen_cidr.add(body)
                    cidrs.append(body)
                    stats["cidr"] += 1
                continue

        # ---- 2) IP 段 a-b / a~b ----
        rng = _classify_ip_range(body, ip_version)
        if rng is not None:
            if rng[0] == "version":
                stats["skipped"] += 1
                stats["notes"].append(
                    f"第{lineno}行: {body} 是 IPv{rng[1]} 段，与所选 IP 版本不符，已忽略")
                continue
            a, b = rng
            size = int(b) - int(a) + 1
            if size <= MAX_RANGE_EXPAND:
                added = 0
                for value in range(int(a), int(b) + 1):
                    ip = str(ipaddress.ip_address(value))
                    key = (ip, int(default_port))
                    if key in seen_entry:
                        continue
                    seen_entry.add(key)
                    entries.append({"ip": ip, "port": int(default_port),
                                    "ip_version": ip_version})
                    added += 1
                stats["range"] += 1
                stats["entry"] += added
            else:
                added = 0
                for net in ipaddress.summarize_address_range(a, b):
                    c = str(net)
                    if c in seen_cidr:
                        continue
                    seen_cidr.add(c)
                    cidrs.append(c)
                    added += 1
                stats["range"] += 1
                stats["cidr"] += added
            continue

        # ---- 3) 单个 IP / 域名 / host:port（保留 scheme 供 parse_ip_list 判断 TLS） ----
        got, errs = parse_ip_list(line, default_port, resolve_domains)
        if not got:
            # 预览模式（resolve_domains=False）不做 DNS，域名只统计不报错
            if not resolve_domains and _looks_like_domain(body):
                stats["domain"] += 1
                continue
            detail = errs[0].split(': ', 1)[-1] if errs else "格式无效"
            errors.append(f"第{lineno}行: {detail}")
            continue

        is_literal = _is_literal_ip(body)
        added = 0
        mismatched = False
        for e in got:
            if int(e.get("ip_version", 4)) != ip_version:
                mismatched = True
                continue
            key = (e["ip"], int(e.get("port", default_port)))
            if key in seen_entry:
                stats["ignored"] += 1
                continue
            seen_entry.add(key)
            entries.append(e)
            added += 1
        if added:
            stats["entry"] += added
            if not is_literal:
                stats["domain"] += 1
        if mismatched and not added:
            stats["skipped"] += 1
            stats["notes"].append(
                f"第{lineno}行: {body} 与所选 IP 版本(IPv{ip_version})不符，已忽略")

    return cidrs, entries, errors, stats


def _is_literal_ip(token: str) -> bool:
    """判断 token（可带端口 / 方括号）是否为字面 IP。"""
    host = token
    if host.startswith('['):
        end = host.find(']')
        if end != -1:
            host = host[1:end]
    elif host.count(':') == 1:
        head, tail = host.split(':')
        if tail.isdigit():
            host = head
    try:
        ipaddress.ip_address(host.strip())
        return True
    except ValueError:
        return False


_DOMAIN_RE = re.compile(r'^(?=.{1,253}$)([A-Za-z0-9_](?:[A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?\.)+[A-Za-z]{2,63}$')


def _looks_like_domain(token: str) -> bool:
    """粗略判断是否像域名（用于预览模式跳过 DNS 时仍能正确计数）。"""
    host = token
    if host.startswith('['):
        return False
    if '/' in host:
        host = host.split('/', 1)[0]
    if host.count(':') == 1:
        head, tail = host.split(':')
        if tail.isdigit():
            host = head
    return bool(_DOMAIN_RE.match(host.strip()))


def describe_source_stats(stats: Dict) -> str:
    """把 parse_source_text 的 stats 转成一句人话，供 UI 预览。"""
    parts = []
    if stats.get("cidr"):
        parts.append(f"{stats['cidr']} 个网段（采样）")
    if stats.get("entry"):
        parts.append(f"{stats['entry']} 个节点（直测）")
    if stats.get("domain"):
        parts.append(f"含 {stats['domain']} 个域名")
    if stats.get("range"):
        parts.append(f"{stats['range']} 个 IP 段")
    if not parts:
        return "未识别到任何有效内容"
    text = "，".join(parts)
    if stats.get("ignored"):
        text += f"；{stats['ignored']} 条重复已去重"
    if stats.get("skipped"):
        text += f"；{stats['skipped']} 行与所选 IP 版本不符已忽略"
    return text


def parse_port_list(value) -> List[int]:
    """把「443,8443 2053」这类文本解析为去重后的端口列表（用于前置过滤）。

    空值 → []（表示不过滤）。非法项被忽略。
    """
    if value is None:
        return []
    if isinstance(value, (list, tuple, set)):
        tokens = [str(v) for v in value]
    else:
        tokens = str(value).replace(',', ' ').replace(';', ' ').split()
    out: List[int] = []
    seen = set()
    for token in tokens:
        token = token.strip()
        if not token:
            continue
        if not token.isdigit():
            continue
        port = int(token)
        if 1 <= port <= 65535 and port not in seen:
            seen.add(port)
            out.append(port)
    return out
