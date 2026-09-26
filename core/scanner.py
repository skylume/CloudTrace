#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import random
import time
import socket
import asyncio
import aiohttp
import ipaddress
import logging
from concurrent.futures import ThreadPoolExecutor, as_completed
from datetime import datetime
from typing import List, Optional, Dict, Callable

from core.constants import load_or_update_ip_cache, AIRPORT_CODES
from core.network import (
    get_iata_code_async, get_iata_code_from_ip, verify_cloudflare,
    get_iata_translation, measure_tcp_latency, measure_http_latency,
    download_speed, HTTPS_PORTS, port_uses_tls,
)
from core.scoring import apply_scores
from core.speed_url import parse_speed_url


logger = logging.getLogger("CloudTrace")

# HTTPing 延迟相对 TCPing 的放大倍率（仅作阈值/颜色等级参考，非精确换算）
HTTPING_SCALE_TLS = 4.0
HTTPING_SCALE_NO_TLS = 1.3

DEFAULT_SAMPLE_MAX = 5000
# 每个 IPv4 /24 子网随机取几个 IP（采样密度）
IPV4_PER_SUBNET = 2
# 连续多少次 429 判定被限速并中止整批测速
DEFAULT_MAX_CONSECUTIVE_429 = 3


def effective_latency_threshold(threshold: int, scan_mode: str, port: int) -> float:
    """按扫描模式换算实际延迟阈值。"""
    if scan_mode != "httping":
        return float(threshold)
    scale = HTTPING_SCALE_TLS if port in HTTPS_PORTS else HTTPING_SCALE_NO_TLS
    return threshold * scale


class BaseScanner:
    def __init__(self, log_callback=None, progress_callback=None, funnel_callback=None,
                 port=443, max_workers=200, timeout=1.0, ping_times=3,
                 latency_threshold=230, custom_cidrs=None, custom_entries=None,
                 pre_filter_ports=None, remote_fetch=None,
                 sample_max=DEFAULT_SAMPLE_MAX, scan_mode="tcping"):
        self.max_workers = max_workers
        self.timeout = timeout
        self.ping_times = ping_times
        self.latency_threshold = latency_threshold
        self.running = True
        self.log_callback = log_callback
        self.progress_callback = progress_callback
        self.funnel_callback = funnel_callback
        self.port = port
        self.custom_cidrs = custom_cidrs or {}
        self.sample_max = max(1, int(sample_max or DEFAULT_SAMPLE_MAX))
        self.scan_mode = scan_mode if scan_mode in ("tcping", "httping") else "tcping"
        # 前置端口过滤（参考 cfnb：在 TCP 测试之前就把不可能用的端口剔掉）
        self.pre_filter_ports = self._normalize_ports(pre_filter_ports)
        # 远程数据源拉取器：() -> (entries, warnings)；由上层注入，在工作线程里执行
        self.remote_fetch = remote_fetch
        # 逐 IP 覆盖表（自定义/非标导入/远程数据源共用）：ip → port / ip_version / use_tls
        self._entry_ports: Dict[str, int] = {}
        self._entry_versions: Dict[str, int] = {}
        self._entry_tls: Dict[str, bool] = {}
        self._entry_order: List[str] = []
        self.filtered_out = 0          # 被前置端口过滤掉的节点数
        self.remote_loaded = 0         # 远程数据源并入的节点数
        # 漏斗计数：生成 → 延迟达标 → 有地区码
        self.funnel = {"generated": 0, "latency_ok": 0, "with_iata": 0}
        if custom_entries:
            self._load_entries(custom_entries)

    @staticmethod
    def _normalize_ports(ports) -> List[int]:
        from core.importer import parse_port_list
        return parse_port_list(ports)

    def _load_entries(self, entries) -> int:
        """把逐条节点并入覆盖表；返回新增条数（重复 / 被过滤的不计）。"""
        added = 0
        for e in entries or []:
            ip = str(e.get('ip') or '').strip()
            if not ip:
                continue
            try:
                port = int(e.get('port') or self.port)
            except (TypeError, ValueError):
                port = self.port
            if not (1 <= port <= 65535):
                port = self.port
            if self.pre_filter_ports and port not in self.pre_filter_ports:
                self.filtered_out += 1
                continue
            if ip in self._entry_ports:
                continue
            self._entry_ports[ip] = port
            try:
                version = int(e.get('ip_version') or (6 if ':' in ip else 4))
            except (TypeError, ValueError):
                version = 6 if ':' in ip else 4
            self._entry_versions[ip] = version
            if e.get('use_tls') is not None:
                self._entry_tls[ip] = bool(e.get('use_tls'))
            self._entry_order.append(ip)
            added += 1
        return added

    def _fetch_remote_entries(self):
        """在工作线程里拉取远程数据源并并入候选池（失败不影响本次扫描）。"""
        if not self.remote_fetch:
            return
        try:
            entries, warnings = self.remote_fetch()
        except Exception as e:
            if self.log_callback:
                self.log_callback(f"远程数据源拉取失败: {e}")
            return
        for warn in warnings or []:
            if self.log_callback:
                self.log_callback(warn)
        if entries:
            added = self._load_entries(entries)
            self.remote_loaded += added
            if self.log_callback:
                self.log_callback(
                    f"远程数据源共 {len(entries)} 条记录，新增可用节点 {added} 个")
        elif self.log_callback:
            self.log_callback("远程数据源未返回任何节点")

    @property
    def ip_version(self) -> int:
        raise NotImplementedError

    @property
    def ip_label(self) -> str:
        return "IPv4" if self.ip_version == 4 else "IPv6"

    def custom_entry_ips(self) -> List[str]:
        """当前 IP 版本下需要逐条直测的 IP（保持输入顺序）。"""
        return [ip for ip in self._entry_order
                if self._entry_versions.get(ip) == self.ip_version]

    def describe_source(self) -> str:
        """给日志用的一句话来源描述（替代原来写死的「Cloudflare 官方 IP 段」）。"""
        mode = self.custom_cidrs.get("mode", "仅官方")
        n_cidr = len(self.custom_cidrs.get("list") or [])
        n_entry = len(self._entry_order)
        if mode == "仅自定义":
            parts = []
            if n_cidr:
                parts.append(f"自定义 {n_cidr} 个网段（采样）")
            if n_entry:
                parts.append(f"{n_entry} 个指定节点（直测）")
            return " + ".join(parts) or "自定义来源"
        if mode == "官方+自定义":
            parts = [f"Cloudflare 官方 {self.ip_label} 段"]
            if n_cidr:
                parts.append(f"自定义 {n_cidr} 个网段")
            if n_entry:
                parts.append(f"{n_entry} 个指定节点")
            return " + ".join(parts)
        return f"Cloudflare 官方 {self.ip_label} 网段"

    def port_for(self, ip: str) -> int:
        return self._entry_ports.get(ip, self.port)

    def version_for(self, ip: str) -> int:
        return self._entry_versions.get(ip, self.ip_version)

    def tls_for(self, ip: str, port: Optional[int] = None) -> bool:
        """该 IP 是否走 TLS：优先逐条覆盖，其次按端口推断。"""
        if ip in self._entry_tls:
            return bool(self._entry_tls[ip])
        return port_uses_tls(port if port is not None else self.port_for(ip))

    def connector_family(self):
        return socket.AF_INET6 if self.ip_version == 6 else None

    def resolve_cidrs(self) -> List[str]:
        """按 CIDR 模式合并官方段与自定义段（IPv4/IPv6 共用逻辑）。"""
        cidr_mode = self.custom_cidrs.get("mode", "仅官方")
        custom_list = self.custom_cidrs.get("list", [])
        official_cidrs = load_or_update_ip_cache(self.ip_version)

        if cidr_mode == "仅自定义":
            return list(custom_list)
        if cidr_mode == "官方+自定义":
            return list(official_cidrs) + list(custom_list)
        return list(official_cidrs)

    def generate_ips_from_cidrs(self) -> List[str]:
        raise NotImplementedError

    async def test_ip_latency(self, session, ip):
        if not self.running:
            return None
        port = self.port_for(ip)
        if self.scan_mode == "httping":
            use_tls = self.tls_for(ip, port)
            return await measure_http_latency(session, ip, port, self.timeout, use_tls)
        return await measure_tcp_latency(ip, port, self.ping_times, self.timeout)

    async def test_single_ip(self, session, ip):
        if not self.running:
            return None
        port = self.port_for(ip)
        threshold = effective_latency_threshold(self.latency_threshold, self.scan_mode, port)
        latency = await self.test_ip_latency(session, ip)
        if latency is not None and latency < threshold:
            self.funnel["latency_ok"] += 1
            iata_code = None
            if self.running:
                try:
                    iata_code = await get_iata_code_async(session, ip, self.timeout, port=port)
                except Exception as e:
                    if self.log_callback:
                        self.log_callback(f"获取地区码失败 {ip}: {str(e)}")
            if iata_code:
                self.funnel["with_iata"] += 1
            return {
                'ip': ip, 'latency': latency, 'iata_code': iata_code,
                'chinese_name': get_iata_translation(iata_code) if iata_code else "未知地区",
                'success': True, 'ip_version': self.version_for(ip),
                'scan_time': datetime.now().strftime("%H:%M:%S"),
                'port': port, 'ping_times': self.ping_times,
                'scan_mode': self.scan_mode,
                'use_tls': self.tls_for(ip, port),
            }
        return None

    async def batch_test_ips(self, ip_list: List[str]):
        semaphore = asyncio.Semaphore(self.max_workers)

        async def test_with_semaphore(session, ip):
            async with semaphore:
                return await self.test_single_ip(session, ip)

        connector_kwargs = {
            'limit': self.max_workers, 'force_close': True,
            'enable_cleanup_closed': True, 'limit_per_host': 0
        }
        family = self.connector_family()
        if family:
            connector_kwargs['family'] = family

        connector = aiohttp.TCPConnector(**connector_kwargs)
        successful_results = []
        start_time = time.time()

        async with aiohttp.ClientSession(connector=connector) as session:
            tasks = []
            for ip in ip_list:
                if not self.running:
                    break
                tasks.append(asyncio.create_task(test_with_semaphore(session, ip)))

            completed = 0
            total = len(tasks)
            last_update_time = time.time()

            pending = set(tasks)
            while pending:
                if not self.running:
                    for task in pending:
                        task.cancel()
                    await asyncio.gather(*pending, return_exceptions=True)
                    break
                done, pending = await asyncio.wait(pending, timeout=0.5, return_when=asyncio.FIRST_COMPLETED)

                for future in done:
                    completed += 1
                    try:
                        result = future.result()
                        if result:
                            successful_results.append(result)
                    except Exception:
                        pass

                current_time = time.time()
                if current_time - last_update_time >= 0.5 or completed == total:
                    elapsed = current_time - start_time
                    ips_per_second = completed / elapsed if elapsed > 0 else 0
                    if self.progress_callback:
                        self.progress_callback(completed, total, len(successful_results), ips_per_second)
                    last_update_time = current_time

        return successful_results

    async def run_scan_async(self):
        try:
            # 远程数据源在工作线程里拉取，避免阻塞 UI 与请求处理
            self._fetch_remote_entries()
            if self.log_callback:
                mode_txt = "HTTPing (TTFB)" if self.scan_mode == "httping" else "TCPing (握手)"
                self.log_callback(
                    f"扫描来源: {self.describe_source()} (端口: {self.port}, 模式: {mode_txt})")
                self.log_callback(f"并发数: {self.max_workers} | 延迟阈值: {self.latency_threshold}ms | 采样上限: {self.sample_max}")
                if self.pre_filter_ports:
                    self.log_callback(
                        f"端口前置过滤: 仅保留 {', '.join(str(p) for p in self.pre_filter_ports)}"
                        f"（已剔除 {self.filtered_out} 个节点）")
            ip_list = self.generate_ips_from_cidrs()
            if not ip_list:
                if self.log_callback:
                    self.log_callback(f"错误: 未能生成{self.ip_label} IP列表")
                return None
            self.funnel["generated"] = len(ip_list)
            if self.funnel_callback:
                self.funnel_callback(dict(self.funnel))
            if self.log_callback:
                self.log_callback(f"已生成 {len(ip_list)} 个待测{self.ip_label} IP（已去重）")
                self.log_callback(f"开始延迟测试...")
            results = await self.batch_test_ips(ip_list)
            if not self.running:
                if self.log_callback:
                    self.log_callback(f"{self.ip_label}扫描被用户中止")
                return None
            if results:
                with_iata = sum(1 for r in results if r.get('iata_code'))
                if self.log_callback:
                    self.log_callback(
                        f"{self.ip_label}扫描完成: 共{len(results)}个IP可用，{with_iata}个获取地区码"
                        f"（{self.funnel['generated']} → {self.funnel['latency_ok']} → {with_iata}）"
                    )
            return results
        except Exception as e:
            if self.log_callback:
                self.log_callback(f"{self.ip_label}扫描过程中出现错误: {str(e)}")
            logger.exception("扫描异常")
            return None

    def stop(self):
        self.running = False


class IPv4Scanner(BaseScanner):
    @property
    def ip_version(self):
        return 4

    def generate_ips_from_cidrs(self) -> List[str]:
        ip_list = []
        seen = set()

        for cidr in self.resolve_cidrs():
            try:
                network = ipaddress.ip_network(cidr, strict=False)
                if network.version != 4:
                    continue
                for subnet in network.subnets(new_prefix=24):
                    hosts = list(subnet.hosts())
                    for ip in random.sample(hosts, min(IPV4_PER_SUBNET, len(hosts))):
                        s = str(ip)
                        if s not in seen:
                            seen.add(s)
                            ip_list.append(s)
            except ValueError as e:
                if self.log_callback:
                    self.log_callback(f"处理CIDR {cidr} 时出错: {e}")

        if len(ip_list) > self.sample_max:
            ip_list = random.sample(ip_list, self.sample_max)
            seen = set(ip_list)

        # 指定节点（自定义 IP / 远程数据源）必须保留，且不受采样上限裁剪
        for ip in self.custom_entry_ips():
            if ip not in seen:
                seen.add(ip)
                ip_list.append(ip)
        return ip_list


class IPv6Scanner(BaseScanner):
    @property
    def ip_version(self):
        return 6

    def __init__(self, **kwargs):
        kwargs.setdefault('latency_threshold', 320)
        kwargs.setdefault('ping_times', 2)
        super().__init__(**kwargs)

    def generate_ips_from_cidrs(self) -> List[str]:
        ip_list = []
        seen = set()

        for cidr in self.resolve_cidrs():
            try:
                network = ipaddress.ip_network(cidr, strict=False)
                if network.version != 6:
                    continue
                if network.num_addresses <= 2:
                    continue
                prefixlen = network.prefixlen
                if prefixlen <= 32:
                    sample_size = 2800
                elif prefixlen <= 40:
                    sample_size = 500
                else:
                    sample_size = 200

                attempts = 0
                added = 0
                max_attempts = sample_size * 3
                while added < sample_size and attempts < max_attempts:
                    attempts += 1
                    random_ip_int = random.randint(
                        int(network.network_address) + 1,
                        int(network.broadcast_address) - 1
                    )
                    s = str(ipaddress.IPv6Address(random_ip_int))
                    if s not in seen:
                        seen.add(s)
                        ip_list.append(s)
                        added += 1
            except ValueError as e:
                if self.log_callback:
                    self.log_callback(f"处理CIDR {cidr} 时出错: {e}")

        if len(ip_list) > self.sample_max:
            ip_list = random.sample(ip_list, self.sample_max)
            seen = set(ip_list)

        for ip in self.custom_entry_ips():
            if ip not in seen:
                seen.add(ip)
                ip_list.append(ip)
        return ip_list


class ImportedScanner(BaseScanner):
    """纯直测列表扫描器：跳过 CIDR 采样，逐条直测（允许 v4/v6 混合）。"""

    def __init__(self, entries=None, **kwargs):
        kwargs.setdefault('custom_entries', list(entries or []))
        super().__init__(**kwargs)
        self._versions = set(self._entry_versions.values()) or {4}

    @property
    def ip_version(self):
        """混合 v4/v6 时返回 4（历史归档按结果内 ip_version 判定，见 main_window）。"""
        return 6 if self._versions == {6} else 4

    @property
    def ip_label(self) -> str:
        if len(self._versions) > 1:
            return "非标(混合)"
        return "IPv4" if self.ip_version == 4 else "IPv6"

    def connector_family(self):
        # 混合列表不强制 family，交由系统路由选择
        if self._versions == {6}:
            return socket.AF_INET6
        return None

    def resolve_cidrs(self):
        return []

    def describe_source(self) -> str:
        return f"直测列表 {len(self._entry_order)} 个节点"

    def generate_ips_from_cidrs(self) -> List[str]:
        return list(self._entry_order)


class SpeedTestTask:
    """下载测速任务（普通线程类，无 Qt 依赖；由 TaskManager 驱动）。

    中止语义：用户点「停止」时 `run()` 返回 **None**（而非部分结果），
    上层据此发 EV_SPEED_ABORT，避免把半截结果当成「完成」写进历史。
    """

    def __init__(self, results, region_code=None, max_test_count=10, current_port=443,
                 speed_url="auto", min_speed=0.0, selected_ips=None,
                 verify_nodes=True, score_weights=None,
                 download_interval=3, label=None,
                 speed_workers=1, result_limit=0, per_region_topn=0,
                 max_consecutive_429=DEFAULT_MAX_CONSECUTIVE_429):
        self.results = results
        self.region_code = region_code.upper() if region_code else None
        self.max_test_count = max(1, int(max_test_count or 10))
        self.download_interval = max(0, int(download_interval))
        self.download_time_limit = 3
        self.running = True
        self.current_port = current_port
        self.min_speed = float(min_speed or 0.0)
        self.selected_ips = selected_ips  # 单点/勾选测速：直接给定 IP 信息列表
        self.verify_nodes = bool(verify_nodes)
        self.score_weights = score_weights
        self.label = label  # 覆盖默认 test_type 文案
        self.speed_workers = max(1, int(speed_workers or 1))
        self.result_limit = max(0, int(result_limit or 0))
        # 分地区 TopN：>0 时每个地区只取延迟最低的 N 个进测速队列（参考 cfnb）
        self.per_region_topn = max(0, int(per_region_topn or 0))
        self.max_consecutive_429 = max(1, int(max_consecutive_429 or DEFAULT_MAX_CONSECUTIVE_429))
        self.test_host, self.download_path, self.speed_url_tls = parse_speed_url(speed_url)
        self.aborted = False          # 用户中止
        self.rate_limited = False     # 触发限速熔断
        self._early_stop = False      # 已收够合格结果
        # 回调由 TaskManager 注入
        self.log_callback: Optional[Callable[[str], None]] = None
        self.progress_callback: Optional[Callable[[int, int, int], None]] = None

    def _log(self, msg: str):
        if self.log_callback:
            self.log_callback(msg)

    def _should_run(self) -> bool:
        return self.running and not self._early_stop

    def _sleep_interruptible(self, seconds: float) -> bool:
        """可中断睡眠；被中止时返回 False。"""
        deadline = time.time() + max(0.0, seconds)
        while time.time() < deadline:
            if not self._should_run():
                return False
            time.sleep(min(0.1, max(0.0, deadline - time.time())))
        return True

    def _use_tls_for(self, ip_info: Dict, port: int) -> bool:
        explicit = ip_info.get('use_tls')
        if explicit is not None:
            return bool(explicit)
        return port_uses_tls(port, default=True)

    def _pick_targets(self) -> List[Dict]:
        if self.selected_ips:
            targets = list(self.selected_ips)
            self._log(f"指定测速: {len(targets)} 个IP")
            return targets

        if self.region_code:
            filtered = [r for r in self.results
                        if r.get('iata_code') and r['iata_code'].upper() == self.region_code]
            region_name = AIRPORT_CODES.get(self.region_code, '未知地区')
            self._log(f"开始地区测速：{self.region_code} ({region_name}) (端口: {self.current_port})")
            self._log(f"找到 {len(filtered)} 个 {self.region_code} 地区的IP")
        else:
            filtered = list(self.results)
            self._log(f"开始完全测速 (端口: {self.current_port})")

        filtered.sort(key=lambda x: x.get('latency', float('inf')))

        if self.per_region_topn > 0:
            buckets: Dict[str, List[Dict]] = {}
            for r in filtered:
                code = (r.get('iata_code') or 'UNKNOWN').upper()
                buckets.setdefault(code, []).append(r)
            picked: List[Dict] = []
            for items in buckets.values():
                picked.extend(items[:self.per_region_topn])
            picked.sort(key=lambda x: x.get('latency', float('inf')))
            self._log(
                f"分地区 TopN 模式：{len(buckets)} 个地区 × 最多 {self.per_region_topn} 个"
                f" = {len(picked)} 个候选（原 {len(filtered)} 个）")
            filtered = picked

        return filtered[:min(self.max_test_count, len(filtered))]

    def _test_one(self, ip_info: Dict):
        """测速单个节点。返回 (kind, payload)。

        kind ∈ {'ok','skip','429','stop'}；payload 为结果 dict 或 ip。
        """
        if not self._should_run():
            return ("stop", None)
        ip = ip_info['ip']
        port = int(ip_info.get('port') or self.current_port)
        latency = ip_info.get('latency', 0)
        use_tls = self._use_tls_for(ip_info, port)

        verified = None
        if self.verify_nodes:
            verified = verify_cloudflare(ip, timeout=3, port=port)
            if not verified:
                self._log(f"  可用性验证失败（非 Cloudflare 节点），跳过: {ip}")
                return ("skip", ip)

        speed, err, status = download_speed(
            ip, port, host=self.test_host,
            path=self.download_path, time_limit=self.download_time_limit,
            should_continue=self._should_run,
            use_tls=use_tls,
        )
        if status == 429:
            return ("429", ip)
        if err and err != "用户中止":
            self._log(f"  测速失败 {ip}: {err}")

        # 优先复用扫描阶段已解析的地区码，缺失时才回查
        colo = ip_info.get('iata_code')
        if not colo or colo == "Unknown":
            colo = get_iata_code_from_ip(ip, timeout=3, port=port)
        colo = colo.upper() if colo else 'UNKNOWN'
        speed_result = {
            'ip': ip, 'latency': latency, 'download_speed': speed,
            'iata_code': colo,
            'chinese_name': AIRPORT_CODES.get(colo, '未知地区'),
            'test_type': self._test_type(), 'port': port,
            'verified': verified, 'use_tls': use_tls,
        }
        return ("ok", speed_result)

    def _test_type(self) -> str:
        return self.label or ("单点测速" if self.selected_ips
                              else "地区测速" if self.region_code else "完全测速")

    def _handle(self, kind, payload, speed_results: List[Dict], state: Dict) -> Optional[str]:
        """统一处理单个测速结果；返回 'break' 表示需要中止循环。"""
        if kind == "skip":
            state["skipped"] += 1
            return None
        if kind == "stop":
            return "break"
        if kind == "429":
            state["consecutive_429"] += 1
            self._log(f"  触发限速(HTTP 429)，连续 {state['consecutive_429']} 次")
            if state["consecutive_429"] >= self.max_consecutive_429:
                self.rate_limited = True
                self._log(
                    f"⚠️ 连续 {state['consecutive_429']} 次被限速，判定 Cloudflare 已对当前网络限速，"
                    "中止本批测速以免剩余 IP 全部拿到假速度"
                )
                return "break"
            return None
        if kind == "ok" and payload:
            state["consecutive_429"] = 0
            speed_results.append(payload)
            self._log(f"  测速结果: {payload['download_speed']} MB/s, 地区: {payload['chinese_name']}")
            if self.result_limit and len(speed_results) >= self.result_limit:
                self._log(f"已达到合格结果上限 {self.result_limit}，提前结束测速")
                self._early_stop = True
                return "break"
        return None

    def _run_serial(self, targets: List[Dict]) -> List[Dict]:
        speed_results: List[Dict] = []
        state = {"skipped": 0, "consecutive_429": 0}
        total = len(targets)
        for i, ip_info in enumerate(targets):
            if not self._should_run():
                break
            port = int(ip_info.get('port') or self.current_port)
            self._log(f"[{i+1}/{total}] 正在测速 {ip_info['ip']}(端口: {port})")
            if self.progress_callback:
                self.progress_callback(i + 1, total, len(speed_results))
            kind, payload = self._test_one(ip_info)
            if self._handle(kind, payload, speed_results, state) == "break":
                break
            if i < total - 1 and not self._sleep_interruptible(self.download_interval):
                break
        if state["skipped"]:
            self._log(f"可用性验证淘汰 {state['skipped']} 个非 CF 节点")
        return speed_results

    def _run_parallel(self, targets: List[Dict]) -> List[Dict]:
        speed_results: List[Dict] = []
        state = {"skipped": 0, "consecutive_429": 0}
        total = len(targets)
        completed = 0
        stagger = (self.download_interval / float(self.speed_workers)) if self.download_interval else 0.0

        executor = ThreadPoolExecutor(max_workers=self.speed_workers,
                                      thread_name_prefix="ct-speed")
        pending = []
        try:
            for ip_info in targets:
                if not self._should_run():
                    break
                port = int(ip_info.get('port') or self.current_port)
                self._log(f"提交测速 {ip_info['ip']}(端口: {port})")
                pending.append(executor.submit(self._test_one, ip_info))
                if stagger > 0 and not self._sleep_interruptible(stagger):
                    break

            for fut in as_completed(pending):
                if not self._should_run():
                    break
                completed += 1
                try:
                    kind, payload = fut.result()
                except Exception as e:
                    logger.debug("并发测速子任务异常: %s", e)
                    continue
                if self.progress_callback:
                    self.progress_callback(completed, total, len(speed_results))
                if self._handle(kind, payload, speed_results, state) == "break":
                    break
        finally:
            executor.shutdown(wait=True)
        if state["skipped"]:
            self._log(f"可用性验证淘汰 {state['skipped']} 个非 CF 节点")
        return speed_results

    def run(self) -> Optional[List[Dict]]:
        """执行测速。中止返回 None；正常结束返回结果列表（可能为空）。"""
        try:
            if not self.results:
                self._log("错误：没有可用的IP进行测速")
                return []

            target_ips = self._pick_targets()
            if not target_ips:
                self._log("没有找到可用的IP进行测速")
                return []

            self._log(f"{self._test_type()}：将对 {len(target_ips)} 个IP进行测速")
            self._log(f"测速源: {self.test_host} · 单点限时 {self.download_time_limit}s")
            if self.speed_workers > 1:
                self._log(f"并发测速: {self.speed_workers} 线程（提高吞吐，间隔按并发数摊薄）")

            if self.speed_workers <= 1:
                speed_results = self._run_serial(target_ips)
            else:
                speed_results = self._run_parallel(target_ips)

            if not self.running:
                self.aborted = True
            if self.aborted:
                self._log("测速被用户中止，本次结果不写入历史")
                return None

            if self.min_speed > 0:
                before = len(speed_results)
                speed_results = [r for r in speed_results if r['download_speed'] >= self.min_speed]
                if before != len(speed_results):
                    self._log(f"阈值筛选: {before} → {len(speed_results)} (≥ {self.min_speed} MB/s)")

            # 综合评分排序（score = W_speed×MB/s ÷ (1 + W_latency×lat秒)）
            speed_results = apply_scores(speed_results, self.score_weights)
            if speed_results:
                best = speed_results[0]
                self._log(
                    f"测速完成！成功 {len(speed_results)}/{len(target_ips)} 个IP，"
                    f"最优 {best['ip']} ({best['download_speed']} MB/s, 评分 {best.get('score')})"
                )
            elif self.rate_limited:
                self._log("本批测速因限速中止，未得到有效结果")
            else:
                self._log("所有IP测速失败")
            return speed_results
        except Exception as e:
            self._log(f"测速过程中出现错误: {str(e)}")
            logger.exception("测速异常")
            return []

    def stop(self):
        self.running = False
