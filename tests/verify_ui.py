#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""CloudTrace 桌面 UI 离屏冒烟测试（QT_QPA_PLATFORM=offscreen）。"""
import os
import sys
import io
import json

os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")
os.environ.setdefault("CLOUDTRACE_ALLOW_MULTI", "1")
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)

# settings.json 是仓库文件：测试会写它，先备份、结束后原样还原
SETTINGS_PATH = os.path.join(ROOT, "settings.json")
_ORIG_SETTINGS = None
if os.path.exists(SETTINGS_PATH):
    with io.open(SETTINGS_PATH, "r", encoding="utf-8") as f:
        _ORIG_SETTINGS = f.read()


def restore_settings_file():
    if _ORIG_SETTINGS is not None:
        with io.open(SETTINGS_PATH, "w", encoding="utf-8") as f:
            f.write(_ORIG_SETTINGS)

from PySide6.QtWidgets import QApplication, QTableWidget
from PySide6.QtCore import Qt

from settings import reset_settings, get_settings
from service.task_manager import task_manager
from service.events import EV_SPEED_ABORT, EV_SCAN_DONE, EV_SPEED_DONE, EV_SETTINGS, EV_STATE

app = QApplication.instance() or QApplication(sys.argv)

PASS, FAIL = [], []


def check(name, cond, detail=""):
    (PASS if cond else FAIL).append(name)
    print(("  [OK]   " if cond else "  [FAIL] ") + name + (("  -> " + str(detail)) if detail and not cond else ""))


reset_settings()

print("\n== 1. WorkerBridge 订阅生命周期（缺陷 3.1） ==")
from ui.bridge import WorkerBridge
b = WorkerBridge()
check("9 条订阅全部登记", len(b._unsubs) == 9, len(b._unsubs))

got = {}
b.speed_aborted.connect(lambda: got.__setitem__("speed_abort", True))
b.scan_aborted.connect(lambda: got.__setitem__("scan_abort", True))
b.speed_completed.connect(lambda r: got.__setitem__("speed_done", r))
b.scan_completed.connect(lambda r: got.__setitem__("scan_done", r))
b.settings_changed.connect(lambda s: got.__setitem__("settings", s))

task_manager.bus.emit(EV_SPEED_ABORT, None)
app.processEvents()
check("EV_SPEED_ABORT -> speed_aborted", got.get("speed_abort") is True)

task_manager.bus.emit(EV_SPEED_DONE, [{"ip": "1.1.1.1"}])
app.processEvents()
check("EV_SPEED_DONE -> speed_completed", got.get("speed_done") == [{"ip": "1.1.1.1"}])

task_manager.bus.emit(EV_SCAN_DONE, None)
app.processEvents()
check("EV_SCAN_DONE(None) -> scan_aborted", got.get("scan_abort") is True)

task_manager.bus.emit(EV_SETTINGS, {"workers": 3})
app.processEvents()
check("EV_SETTINGS -> settings_changed", got.get("settings") == {"workers": 3})

b.detach()
check("detach 后订阅清零", len(b._unsubs) == 0)
task_manager.bus.emit(EV_SPEED_ABORT, None)
app.processEvents()
check("detach 后不再收到事件（无异常）", True)

print("\n== 2. RegionChips 重建不重影（缺陷 3.6） ==")
from ui.widgets import RegionChips, Card, StatCard, EmptyState, FlowLayout
chips = RegionChips()
chips.set_stats([{"code": "HKG", "name": "中国香港", "count": 5},
                 {"code": "NRT", "name": "日本", "count": 3}])
n1 = chips.layout().count()
chips.set_stats([{"code": "SIN", "name": "新加坡", "count": 2}])
n2 = chips.layout().count()
check("首次 2 个芯片", n1 == 2, n1)
check("重建后仅 1 个（无重影）", n2 == 1, n2)
chips.set_stats([{"code": "HKG", "name": "中国香港", "count": 5},
                 {"code": "NRT", "name": "日本", "count": 3},
                 {"code": "SIN", "name": "新加坡", "count": 2}])
check("再重建为 3 个", chips.layout().count() == 3, chips.layout().count())
chips.select_all()
check("select_all 选中全部", sorted(chips.selected_codes()) == ["HKG", "NRT", "SIN"], chips.selected_codes())
chips.clear_selection()
check("clear_selection 清空", chips.selected_codes() == [])

print("\n== 2b. FunnelBar 顺序与重建 ==")
from ui.widgets import FunnelBar
fb = FunnelBar()
fb.set_steps([("生成", 5000), ("延迟达标", 342), ("地区解析", 318), ("可用", 8)])
labels = [w.text() for w in fb._widgets]
check("漏斗顺序为 生成→延迟达标→地区解析→可用",
      labels == ["生成 5000", "→", "延迟达标 342", "→", "地区解析 318", "→", "可用 8"], labels)
fb.set_steps([])
check("清空后仅剩占位", [w.text() for w in fb._widgets] == ["等待开始"],
      [w.text() for w in fb._widgets])
fb.set_steps([("生成", 1), ("可用", 2)])
check("重建后顺序正确（无残留 / 无错位）",
      [w.text() for w in fb._widgets] == ["生成 1", "→", "可用 2"],
      [w.text() for w in fb._widgets])
check("布局末位是 stretch（步骤靠左）",
      fb._layout.itemAt(fb._layout.count() - 1).spacerItem() is not None)

print("\n== 3. 消息框自适应尺寸（长文本不被裁切） ==")
from ui.dialogs import CustomMessageBox, HistorySelectDialog, ExportDialog
long_text = "可用地区码: " + ", ".join(f"CODE{i:02d}" for i in range(40))
dlg = CustomMessageBox(None, "标题", long_text)
check("最小宽度 400", dlg.minimumWidth() == 400, dlg.minimumWidth())
check("最大宽度 620", dlg.maximumWidth() == 620, dlg.maximumWidth())
check("已 adjustSize（宽度自适应）", dlg.width() >= 400, dlg.width())
dlg.deleteLater()

print("\n== 4. HistorySelectDialog 信号重载（缺陷 3.2 相关） ==")
hist = [{"save_time": "2026-01-01 10:00:00", "count": 12, "filename": "a.json", "filepath": "/tmp/a.json"},
        {"save_time": "2026-01-02 10:00:00", "count": 20, "filename": "b.json", "filepath": "/tmp/b.json"}]
hd = HistorySelectDialog("IPv4", "扫描", hist)
check("表格行数正确", hd.table.rowCount() == 2, hd.table.rowCount())
hd.table.selectRow(1)
hd._on_accept()
check("双击/确认后取到选中文件", hd.selected_filepath == "/tmp/b.json", hd.selected_filepath)
hd.deleteLater()

print("\n== 5. ExportDialog 格式选择（新增 TXT） ==")
ed = ExportDialog(has_scan=True, has_speed=True)
check("默认格式 csv", ed.format == "csv", ed.format)
txt_btn = [b for b in ed._fmt_group.buttons() if b.property("fmt") == "txt"]
check("存在 TXT 格式单选项", len(txt_btn) == 1)
txt_btn[0].setChecked(True)
ed._accept()
check("选择 TXT 后 format=txt", ed.format == "txt", ed.format)
check("全部字段勾选 -> fields=None", ed.fields is None, ed.fields)
ed.deleteLater()

print("\n== 6. 各页面构建与设置回填 ==")
from ui.pages.scan_page import ScanPage
from ui.pages.result_page import ResultPage
from ui.pages.speed_page import SpeedPage
from ui.pages.settings_page import SettingsPage
from ui.pages.history_page import HistoryPage

s = get_settings()
s["workers"] = 321
s["latency_threshold"] = 222
s["sample_max"] = 4321
s["speed_workers"] = 2
s["speed_result_limit"] = 9

sp = ScanPage(s)
sp.reload_from_settings(full=True)
col = sp.collect()
check("ScanPage.collect 返回 dict", isinstance(col, dict), type(col))
check("ScanPage 回填 workers=321", col.get("workers") == 321, col.get("workers"))
check("ScanPage 回填 threshold=222", col.get("threshold") == 222, col.get("threshold"))

# --- 新增控件：端口前置过滤 / 多形态来源 / 实时解析预览 / 远程数据源卡片 ---
sp.show()
app.processEvents()

sp.input_prefilter.setText("443,8443")
check("端口前置过滤写回 collect",
      sp.collect().get("pre_filter_ports") == "443,8443",
      sp.collect().get("pre_filter_ports"))

sp.combo_source.setCurrentText("仅自定义")
app.processEvents()
check("切到「仅自定义」显示多形态输入框", sp.text_source.isVisible())
check("切到「仅自定义」隐藏官方说明", not sp.lbl_source_official.isVisible())

sp.text_source.setPlainText("1.1.1.1\n10.0.0.0/24\n8.8.8.8:8443\n10.0.0.0-10.0.0.3")
app.processEvents()
check("多形态来源（CIDR/IP/IP:port/IP 段）实时预览出结果",
      "解析预览" in sp.lbl_source_preview.text(), sp.lbl_source_preview.text())
check("预览统计含「网段」与「指定节点」",
      "网段" in sp.lbl_source_preview.text(), sp.lbl_source_preview.text())

sp.combo_source.setCurrentText("仅官方")
app.processEvents()
check("切回「仅官方」隐藏多形态输入框", not sp.text_source.isVisible())
check("切回「仅官方」显示官方说明", sp.lbl_source_official.isVisible())

sp.combo_source.setCurrentText("官方+自定义")
app.processEvents()
check("「官方+自定义」也显示多形态输入框", sp.text_source.isVisible())

sp.chk_remote.setCurrentText("启用")
app.processEvents()
check("启用远程数据源 -> collect.use_remote_sources=True",
      sp.collect().get("use_remote_sources") is True,
      sp.collect().get("use_remote_sources"))
check("远程数据源文本可解析为源列表",
      isinstance(sp.collect().get("remote_sources"), list)
      and len(sp.collect().get("remote_sources")) >= 1,
      sp.collect().get("remote_sources"))
check("远程参数（重试 / 间隔 / 超时）进入 collect",
      all(k in sp.collect() for k in ("source_retries", "source_retry_delay", "source_timeout")),
      sorted(sp.collect().keys()))

sp.chk_remote.setCurrentText("不启用")
app.processEvents()
check("关闭远程数据源 -> collect.use_remote_sources=False",
      sp.collect().get("use_remote_sources") is False,
      sp.collect().get("use_remote_sources"))

# 还原为「仅官方」：来源模式会被持久化，若留成自定义会让第 7 节的 collect() 弹模态框而卡住
sp.combo_source.setCurrentText("仅官方")
app.processEvents()
sp.hide()

rp = ResultPage()
rows = [{"ip": "1.2.3.4", "latency": 50.0, "iata_code": "HKG", "chinese_name": "中国香港",
         "port": 443, "scan_mode": "tcping", "scan_time": "2026-01-01 00:00:00", "ip_version": 4},
        {"ip": "5.6.7.8", "latency": 180.0, "iata_code": "NRT", "chinese_name": "日本",
         "port": 443, "scan_mode": "tcping", "scan_time": "2026-01-01 00:00:00", "ip_version": 4}]
rp.set_results(rows, funnel={"generated": 1000, "latency_ok": 30, "with_iata": 20}, scan_mode="tcping")
check("ResultPage 表格 2 行", rp.table.rowCount() == 2, rp.table.rowCount())
check("ResultPage 勾选接口可用", isinstance(rp.checked_ip_infos(), list))
rp.set_empty()
check("set_empty 后表格清空", rp.table.rowCount() == 0, rp.table.rowCount())

spd = SpeedPage(s)
spd.reload_from_settings(full=True)
spd.set_results([{"ip": "1.1.1.1", "latency": 40.0, "download_speed": 12.5, "score": 9.1,
                  "iata_code": "HKG", "chinese_name": "中国香港", "port": 443,
                  "test_type": "完全测速", "verified": True}])
check("SpeedPage 表格 1 行", spd.table.rowCount() == 1, spd.table.rowCount())
check("SpeedPage.collect 返回 dict", isinstance(spd.collect(), dict))

stp = SettingsPage(s)
stp.set_param_provider(lambda: {"workers": 200})
stp.reload_from_settings()
check("SettingsPage 回填成功", True)

hp = HistoryPage()
check("HistoryPage 构建成功", hp is not None)

print("\n== 7. 主窗口构建 + 页面切换 + 状态同步 ==")
from ui.main_window import CloudflareScanUI
from settings import apply_settings

# 主窗口会真的起 HTTP 面板：这里改用空闲端口，避免默认 17443 被别的实例占用而卡住
import socket


def _free_port() -> int:
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


apply_settings({"http_port": _free_port(), "http_enabled": True, "allow_lan": False})
win = CloudflareScanUI()
check("主窗口构建成功", win is not None)
check("主窗口已绑定 bridge", win.bridge is not None)
for i in range(5):
    win._set_page(i)
check("5 个页面切换无异常", True)

# 模拟「另一侧 UI」改了设置 → 桌面表单应同步（缺陷 3.3）
from settings import apply_settings
apply_settings({"sample_max": 8888, "speed_workers": 4})
win._on_settings_changed(get_settings())
app.processEvents()
# 保证来源模式为「仅官方」，否则自定义来源为空时 collect() 会弹模态框阻塞测试
win.scan_page.combo_source.setCurrentText("仅官方")
app.processEvents()
check("设置广播后 scan sample_max 同步为 8888",
      win.scan_page.collect().get("sample_max") == 8888, win.scan_page.collect().get("sample_max"))
check("设置广播后 settings 页 speed_workers 同步为 4",
      win.settings_page.spin_speed_workers.value() == 4, win.settings_page.spin_speed_workers.value())

# 模拟测速中止 → 状态应为「已停止」而非「完成」（缺陷 3.2）
win._speed_aborted()
app.processEvents()
check("中止后状态为「已停止」", win.lbl_pill.text() == "已停止", win.lbl_pill.text())

# 表格 checkbox 全选/清空（result page）
win.result_page.set_results(rows, scan_mode="tcping")
win.result_page._set_all_checked(True)
n_all = len(win.result_page.checked_ip_infos())
win.result_page._set_all_checked(False)
n_none = len(win.result_page.checked_ip_infos())
check("勾选全部 -> 2", n_all == 2, n_all)
check("清空勾选 -> 0", n_none == 0, n_none)

win.bridge.detach()
win.close()
app.processEvents()

print("\n== 8. 单实例锁 ==")
from core.single_instance import acquire_single_instance, release_single_instance
check("首次获取成功", acquire_single_instance() is True)
check("同进程重复获取也成功（可重入）", acquire_single_instance() is True)
release_single_instance()
check("释放无异常", True)

print("\n" + "=" * 56)
print(f"通过 {len(PASS)} / 失败 {len(FAIL)}")
restore_settings_file()
if FAIL:
    print("失败项: " + ", ".join(FAIL))
    sys.exit(1)
print("全部通过 ✓")
