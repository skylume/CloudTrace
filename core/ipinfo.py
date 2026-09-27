#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""IP 归属地 / 数据中心增量缓存 + 国家（地区）黑白名单前置过滤。

对齐 cfnb 的 `IpInfoAsync` + `calibrate_regions()` + `BLOCKED_COUNTRIES` /
`ALLOWED_COUNTRIES`，但有一处关键取舍：

    cfnb 用 ipinfo.io 查国家/城市/ASN，需要申请 Token 且每次查询都要联网；
    本项目**直接复用 `/cdn-cgi/trace` 已经返回的 `loc`（出口国家）与 `colo`（数据中心）**，
    零额外请求、零凭证，把结果增量写入本地缓存。

因此这里只解决两件在 cfnb 里真实存在、且本项目此前缺失的事：

1. **增量缓存**：同一个 IP 的 colo/loc 只探测一次，后续扫描（含分地区统计、
   测速排序）可直接复用，不必再打一次 trace。
2. **前置过滤**：扫描前先按缓存里的**数据中心地区**做黑白名单过滤，把必然被
   淘汰的节点挡在 TCP/HTTP 测试之前，省下并发预算（cfnb 的顺序是
   端口 → 黑名单 → 白名单）。

关于「按什么过滤」的一处重要取舍：
    trace 里的 `loc` 是**请求方（本机）所在国家**，同一次扫描里所有节点都一样，
    拿它过滤毫无意义；真正区分节点的是 `colo`（该节点由哪个 Cloudflare 数据中心
    服务）。因此本模块按 `colo` 过滤，并支持两种写法：3 位数据中心码（HKG）或
    2 位国家码（HK，经 `core.constants.get_country_from_iata` 换算）。

对外接口：
    parse_region_list(value)               "cn, HKG; nrt" → ["CN","HKG","NRT"]
    IpInfoCache                            本地缓存（线程安全、原子落盘）
    get_ip_cache()                         进程内单例
    filter_ips_by_region(ips, allow, block, cache)   → (保留列表, 被剔除数)
"""

import os
import threading
from typing import Dict, List, Optional, Tuple

from core.constants import APP_DIR, get_country_from_iata
from core.utils import atomic_write_json, safe_json_load


IPINFO_CACHE_FILE = os.path.join(APP_DIR, "ipinfo_cache.json")
# 缓存条目上限：超出后按插入顺序淘汰最旧的（Python 3.7+ dict 保序）
DEFAULT_MAX_ENTRIES = 100000


def parse_region_list(value) -> List[str]:
    """把「地区代码」文本解析成规范化的大写代码列表。

    接受两种写法（可混用）：
        2 位 ISO 国家/地区代码   CN / HK / JP / US
        3 位数据中心代码(IATA)   HKG / NRT / SIN

    接受 `CN, HK; NRT  JP` 这类混合分隔；非法项（非 2/3 位字母）被忽略。
    空值 → []（表示不做该方向的过滤）。
    """
    if value is None:
        return []
    if isinstance(value, (list, tuple, set)):
        tokens = [str(v) for v in value]
    else:
        tokens = str(value).replace(',', ' ').replace(';', ' ').replace('|', ' ').split()
    out: List[str] = []
    seen = set()
    for token in tokens:
        code = token.strip().upper()
        if len(code) not in (2, 3) or not code.isalpha():
            continue
        if code not in seen:
            seen.add(code)
            out.append(code)
    return out


def format_region_list(codes) -> str:
    """地区代码列表 → 文本（供 UI 输入框回填）。"""
    return ",".join(parse_region_list(codes))


def _region_matches(colo: str, tokens: List[str]) -> Optional[bool]:
    """判断数据中心代码 colo 是否命中 tokens 中的任意一项。

    返回 True（命中）/ False（明确不命中）/ None（无法判定：colo 所属国家
    不在内置映射表里，而 token 里只有国家码）。无法判定时调用方应保守保留节点。
    """
    colo = (colo or "").upper()
    if not colo:
        return None
    country = get_country_from_iata(colo)
    undetermined = False
    for token in tokens:
        if len(token) == 3:
            if token == colo:
                return True
        else:                     # 2 位国家/地区代码
            if country is None:
                undetermined = True
            elif token == country:
                return True
    return None if undetermined else False


class IpInfoCache:
    """IP → (国家代码 loc, 数据中心 colo) 的本地增量缓存。

    线程安全：扫描线程池里多个协程/线程可能同时写入。
    落盘走原子写，避免进程崩溃留下半截 JSON。
    """

    def __init__(self, path: str = IPINFO_CACHE_FILE,
                 max_entries: int = DEFAULT_MAX_ENTRIES):
        self.path = path
        self.max_entries = max(1, int(max_entries))
        self._lock = threading.RLock()
        self._data: Dict[str, List[str]] = {}
        self._dirty = False
        self._loaded = False

    # ---------------- 读写 ----------------
    def load(self) -> "IpInfoCache":
        with self._lock:
            if self._loaded:
                return self
            raw = safe_json_load(self.path)
            data: Dict[str, List[str]] = {}
            if isinstance(raw, dict):
                items = raw.get("ips") if "ips" in raw else raw
                if isinstance(items, dict):
                    for ip, val in items.items():
                        parsed = self._coerce(val)
                        if parsed is not None:
                            data[str(ip)] = parsed
            self._data = data
            self._loaded = True
            self._dirty = False
            return self

    @staticmethod
    def _coerce(val) -> Optional[List[str]]:
        """把缓存里的任意写法规范成 [loc, colo]。"""
        if isinstance(val, (list, tuple)):
            loc = str(val[0]).upper() if len(val) > 0 and val[0] else ""
            colo = str(val[1]).upper() if len(val) > 1 and val[1] else ""
            return [loc, colo]
        if isinstance(val, str):
            # 兼容 "CN|SIN" / "CN/SIN" 这类紧凑写法
            for sep in ('|', '/', ' '):
                if sep in val:
                    a, _, b = val.partition(sep)
                    return [a.strip().upper(), b.strip().upper()]
            return [val.strip().upper(), ""]
        if isinstance(val, dict):
            loc = str(val.get("loc") or val.get("country") or "").upper()
            colo = str(val.get("colo") or "").upper()
            return [loc, colo]
        return None

    def save(self) -> bool:
        """把缓存原子落盘；无改动时直接返回 True（避免无谓 IO）。"""
        with self._lock:
            if not self._dirty:
                return True
            # 超出上限则按插入顺序丢弃最旧的
            if len(self._data) > self.max_entries:
                overflow = len(self._data) - self.max_entries
                for key in list(self._data.keys())[:overflow]:
                    self._data.pop(key, None)
            payload = {"version": 1, "count": len(self._data), "ips": self._data}
            try:
                atomic_write_json(self.path, payload)
            except Exception:
                return False
            self._dirty = False
            return True

    # ---------------- 查询 / 更新 ----------------
    def get(self, ip: str) -> Optional[List[str]]:
        with self._lock:
            return self._data.get(str(ip))

    def country(self, ip: str) -> Optional[str]:
        """返回该 IP 已知的出口国家代码（大写）；未知返回 None。"""
        entry = self.get(ip)
        if not entry:
            return None
        code = (entry[0] or "").upper()
        return code or None

    def colo(self, ip: str) -> Optional[str]:
        entry = self.get(ip)
        if not entry:
            return None
        code = (entry[1] or "").upper()
        return code or None

    def update(self, ip: str, loc: Optional[str] = None,
               colo: Optional[str] = None) -> None:
        """增量写入一个 IP 的 loc / colo；两者都为空则忽略。"""
        ip = str(ip or "").strip()
        if not ip:
            return
        new_loc = (loc or "").upper().strip()
        new_colo = (colo or "").upper().strip()
        if not new_loc and not new_colo:
            return
        with self._lock:
            old = self._data.get(ip) or ["", ""]
            merged = [new_loc or old[0], new_colo or old[1]]
            if merged != old or ip not in self._data:
                self._data[ip] = merged
                self._dirty = True

    def update_from_detail(self, ip: str, detail: Optional[Dict]) -> None:
        """从 trace 明细（含 loc / colo）增量写入。"""
        if not isinstance(detail, dict):
            return
        loc = detail.get("loc") or detail.get("country")
        colo = detail.get("colo") or detail.get("iata_code")
        # 过滤掉 trace 里常见的无意义值
        if isinstance(loc, str) and loc.upper() in ("XX", "T1", "UNKNOWN", "NONE"):
            loc = ""
        if isinstance(colo, str) and colo.upper() in ("UNKNOWN", "NONE", ""):
            colo = ""
        self.update(ip, loc=loc, colo=colo)

    def update_many(self, mapping: Dict[str, Dict]) -> int:
        """批量写入 {ip: detail}；返回写入条数。"""
        n = 0
        for ip, detail in (mapping or {}).items():
            before = self._data.get(str(ip))
            self.update_from_detail(ip, detail)
            if self._data.get(str(ip)) != before:
                n += 1
        return n

    # ---------------- 维护 ----------------
    def __len__(self) -> int:
        with self._lock:
            return len(self._data)

    def clear(self) -> None:
        with self._lock:
            self._data.clear()
            self._dirty = True

    def stats(self) -> Dict:
        with self._lock:
            return {"count": len(self._data), "path": self.path}


_cache_singleton: Optional[IpInfoCache] = None
_singleton_lock = threading.Lock()


def get_ip_cache() -> IpInfoCache:
    """进程内单例缓存（首次调用自动从磁盘加载）。"""
    global _cache_singleton
    with _singleton_lock:
        if _cache_singleton is None:
            _cache_singleton = IpInfoCache().load()
        return _cache_singleton


def filter_ips_by_region(ips: List[str], allow=None, block=None,
                         cache: Optional[IpInfoCache] = None
                         ) -> Tuple[List[str], int]:
    """按**数据中心地区**前置过滤；顺序与 cfnb 一致：先黑名单，后白名单。

    匹配依据是缓存的 `colo`（Cloudflare 边缘的 IATA 机场码），而不是 trace 的
    `loc` —— 因为 `loc` 描述的是「请求方（本机）所在国家」，对同一次扫描的
    所有节点都相同，无法区分节点；`colo` 才是「这个节点由哪个数据中心服务」。

    - token 可以是 3 位数据中心码（HKG）或 2 位国家码（HK，经内置映射换算）。
    - 只对**缓存里已知 colo** 的 IP 生效；未知一律保留（首次扫描缓存为空 →
      不过滤任何东西，符合「增量」语义）。
    - 任一列表为空即跳过该步。
    返回 (保留的 IP 列表, 被剔除数量)。
    """
    allow_tokens = parse_region_list(allow)
    block_tokens = parse_region_list(block)
    if not allow_tokens and not block_tokens:
        return list(ips), 0

    store = cache if cache is not None else get_ip_cache()

    kept: List[str] = []
    removed = 0
    for ip in ips:
        colo = store.colo(ip)
        if not colo:
            kept.append(ip)
            continue
        if block_tokens and _region_matches(colo, block_tokens):
            removed += 1
            continue
        if allow_tokens and _region_matches(colo, allow_tokens) is False:
            removed += 1
            continue
        kept.append(ip)
    return kept, removed


# 向后兼容别名（旧名以「国家」命名，实际按数据中心地区过滤）
parse_country_list = parse_region_list
format_country_list = format_region_list
filter_ips_by_country = filter_ips_by_region
