#!/usr/bin/env python3
"""API 测试公共库：轻量封装 urllib，手动管理 cookie（绕过 Secure 标记在 http 下的限制）。"""
import json
import re
import urllib.request
import urllib.error

BASE = "http://127.0.0.1:8091"
ORIGIN = {"Origin": "http://127.0.0.1:8091", "Content-Type": "application/json"}
RESULTS = []


def req(method, path, body=None, token=None, admin_token=None, headers=None, raw_body=None, ctype="application/json"):
    """返回 (status, json_obj|None, set_cookie_headers list)"""
    from urllib.parse import quote
    # 已含 %XX 的部分保留，裸中文/空格等按 UTF-8 编码
    path = quote(path, safe="/?&=%[]:@+$,;*'()!")
    url = BASE + path
    h = dict(ORIGIN)
    if headers:
        h.update(headers)
    if token:
        h["Cookie"] = "gk_user=" + token
    if admin_token:
        h["Cookie"] = "gk_admin=" + admin_token
    data = None
    if raw_body is not None:
        data = raw_body.encode() if isinstance(raw_body, str) else raw_body
    elif body is not None:
        data = json.dumps(body).encode()
    r = urllib.request.Request(url, data=data, method=method, headers=h)
    try:
        with urllib.request.urlopen(r, timeout=30) as resp:
            raw = resp.read()
            sc = [v for k, v in resp.getheaders() if k.lower() == "set-cookie"]
            try:
                return resp.status, json.loads(raw), sc
            except Exception:
                return resp.status, raw[:400], sc
    except urllib.error.HTTPError as e:
        raw = e.read()
        sc = [v for k, v in e.headers.items()] if e.headers else []
        sc = [v for v in sc if "ookie" in v or "gk_" in v] if sc else []
        try:
            return e.code, json.loads(raw), sc
        except Exception:
            return e.code, raw[:400], sc


def extract_cookie(sc_list, name):
    for sc in sc_list:
        m = re.search(name + r"=([^;]+)", sc)
        if m:
            return m.group(1)
    return None


def record(case_id, name, passed, detail="", severity_hint=""):
    RESULTS.append({"id": case_id, "name": name, "pass": passed, "detail": str(detail)[:500], "sev": severity_hint})
    mark = "PASS" if passed else "FAIL"
    print(f"[{mark}] {case_id} {name}" + (f" | {str(detail)[:200]}" if not passed else ""))


def summary(tag):
    total = len(RESULTS)
    fails = [r for r in RESULTS if not r["pass"]]
    print(f"\n===== {tag}: {total - len(fails)}/{total} passed =====")
    for f in fails:
        print(f"  FAIL {f['id']}: {f['name']} -> {f['detail'][:300]}")
    return fails
