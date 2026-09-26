#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""一次性运行全部验证脚本，汇总通过/失败。

用法（在项目根目录）：
    python tests/run_all.py
"""
import os
import shutil
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PY_TESTS = ["verify_core.py", "verify_api.py", "verify_ui.py"]
JS_TESTS = ["verify_web.js"]

env = dict(os.environ)
env.setdefault("QT_QPA_PLATFORM", "offscreen")
env.setdefault("CLOUDTRACE_ALLOW_MULTI", "1")

# jsdom 装在 WorkBuddy 托管的 node 工作区里，需要把它的 node_modules 暴露出来
NODE_WORKSPACE = os.environ.get(
    "CLOUDTRACE_NODE_MODULES",
    os.path.join(os.path.expanduser("~"), ".workbuddy-ai", "binaries",
                 "node", "workspace", "node_modules"),
)
if os.path.isdir(NODE_WORKSPACE):
    env["NODE_PATH"] = NODE_WORKSPACE + os.pathsep + env.get("NODE_PATH", "")

results = []
for name in PY_TESTS:
    path = os.path.join(ROOT, "tests", name)
    print("\n" + "#" * 60)
    print("# " + name)
    print("#" * 60)
    results.append((name, subprocess.run([sys.executable, path], cwd=ROOT, env=env).returncode))

node = shutil.which("node")
for name in JS_TESTS:
    path = os.path.join(ROOT, "tests", name)
    print("\n" + "#" * 60)
    print("# " + name)
    print("#" * 60)
    if not node:
        print("  [SKIP] 未找到 node，跳过")
        results.append((name, None))
        continue
    results.append((name, subprocess.run([node, path], cwd=ROOT, env=env).returncode))

print("\n" + "=" * 60)
failed = [n for n, rc in results if rc not in (0, None)]
for name, rc in results:
    tag = "[SKIP]" if rc is None else ("[PASS]" if rc == 0 else "[FAIL]")
    print(f"  {tag} {name}")
if failed:
    print(f"\n{len(failed)} 个脚本失败: " + ", ".join(failed))
    sys.exit(1)
print("\n全部验证脚本通过 ✓")
