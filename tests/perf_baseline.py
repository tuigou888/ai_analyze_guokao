#!/usr/bin/env python3
"""性能基线 + 可用性抽样（不依赖浏览器）。浏览器探索由 ui_explore.mjs 负责。"""
import json
import statistics
import sys
import time
import urllib.request
from concurrent.futures import ThreadPoolExecutor

BASE = "http://127.0.0.1:8091"
out = []


def timed_get(path, cookie=None, n=10):
    from urllib.parse import quote
    path = quote(path, safe="/?&=%[]:@+$,;*'()!")
    ts = []
    status = None
    for _ in range(n):
        t0 = time.perf_counter()
        r = urllib.request.Request(BASE + path, headers={"Cookie": cookie} if cookie else {})
        try:
            with urllib.request.urlopen(r, timeout=30) as resp:
                status = resp.status
                resp.read()
        except urllib.error.HTTPError as e:
            status = e.code
        ts.append((time.perf_counter() - t0) * 1000)
    return {"path": path, "status": status, "n": n, "p50": round(statistics.median(ts), 1),
            "max": round(max(ts), 1), "min": round(min(ts), 1)}


print("== 静态资源与首屏 ==")
for p in ["/", "/login", "/assets/index-CapiEFMu.js"]:
    r = timed_get(p, n=5)
    out.append(r)
    print(r)

print("== 认证后核心 API（10 次取样）==")
# 登录拿 cookie
req = urllib.request.Request(BASE + "/api/auth/login", data=json.dumps(
    {"username": "qa-u1", "password": "QaUser-2026-tx8"}).encode(),
    headers={"Content-Type": "application/json", "Origin": BASE}, method="POST")
with urllib.request.urlopen(req) as resp:
    cookie = "gk_user=" + [v.split(";")[0].split("=")[1] for v in resp.headers.get_all("Set-Cookie") if "gk_user=" in v][0]
for p in ["/api/questions?size=20", "/api/filters", "/api/papers", "/api/concepts", "/api/practice/stats", "/api/wrongbook?size=20", "/api/account/dashboard"]:
    r = timed_get(p, cookie=cookie)
    out.append(r)
    print(r)

print("== 小并发（5 线程 × 6 请求 /api/questions）==")
def one(i):
    t0 = time.perf_counter()
    r = urllib.request.Request(BASE + "/api/questions?size=5&page=" + str(i % 5 + 1), headers={"Cookie": cookie})
    with urllib.request.urlopen(r, timeout=30) as resp:
        resp.read()
    return (time.perf_counter() - t0) * 1000
with ThreadPoolExecutor(5) as ex:
    ts = list(ex.map(one, range(30)))
out.append({"path": "/api/questions 并发30", "p50": round(statistics.median(ts), 1), "max": round(max(ts), 1), "min": round(min(ts), 1)})
print(f"并发30请求: p50={out[-1]['p50']}ms max={out[-1]['max']}ms min={out[-1]['min']}ms")

print("== 大页压力抽样（size=100 + 搜索）==")
for p in ["/api/questions?size=100", "/api/search?q=增长率", "/api/questions?q=增长率&size=50"]:
    r = timed_get(p, cookie=cookie, n=5)
    out.append(r)
    print(r)

with open("/home/tuigou/works/play/apps/ai_analyze_guokao/tests/artifacts/perf_baseline.json", "w") as f:
    json.dump(out, f, ensure_ascii=False, indent=1)
print("saved perf_baseline.json")
