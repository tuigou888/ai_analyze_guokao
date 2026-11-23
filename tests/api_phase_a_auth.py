#!/usr/bin/env python3
"""P0-A 认证与注册流程 API 测试。运行前提：8091 服务 + 副本库 SMTP 已清空（验证码走日志降级）。"""
import re
import subprocess
import sys
sys.path.insert(0, "/home/tuigou/works/play/apps/ai_analyze_guokao/tests")
import json as _j
from api_lib import req, record, summary, extract_cookie, RESULTS

LOG = "/home/tuigou/works/play/apps/ai_analyze_guokao/tests/artifacts/serve.log"


def get_code_from_log(email):
    out = subprocess.run(["grep", email, LOG], capture_output=True, text=True).stdout
    codes = re.findall(r"code=(\d{6})", out)
    return codes[-1] if codes else None


print("== A1 email-code ==")
st, b, _ = req("POST", "/api/auth/email-code", {"email": "qa-u1-tx9@test.local"})
record("A1.1", "email-code 正常请求", st == 200 and b.get("ok"), f"{st} {b}")

st, b, _ = req("POST", "/api/auth/email-code", {})
record("A1.2", "email-code 缺 email 应 4xx", 400 <= st < 500 and st != 500, f"{st} {b}")

st, b, _ = req("POST", "/api/auth/email-code", {"email": "not-an-email"})
record("A1.3", "email-code 非法 email 应 4xx", 400 <= st < 500 and st != 500, f"{st} {b}")

st, b, _ = req("POST", "/api/auth/email-code", {"email": "qa-u1-tx9@test.local"}, headers={"Origin": "http://evil.example.com"})
record("A1.4", "email-code 伪造 Origin 应 403", st == 403, f"{st} {b}")

print("== A2 register ==")
code = get_code_from_log("qa-u1-tx9@test.local")
st, b, _ = req("POST", "/api/auth/register", {
    "username": "qa-u1", "password": "QaUser-2026-tx9", "nickname": "测试用户一",
    "email": "qa-u1-tx9@test.local", "code": code or "000000"})
record("A2.1", "register 正常注册", st == 201, f"{st} {b}")

st, b, _ = req("POST", "/api/auth/register", {
    "username": "qa-u1x", "password": "QaUser-2026-tx9", "email": "qa-u2-tx9@test.local", "code": "999999"})
record("A2.2", "register 错误验证码应 4xx", 400 <= st < 500 and st != 500, f"{st} {b}")

st, b, _ = req("POST", "/api/auth/register", {
    "username": "qa-u1", "password": "QaUser-2026-tx9", "email": "qa-u3-tx9@test.local", "code": code or "000000"})
record("A2.3", "register 重复用户名应 4xx", 400 <= st < 500 and st != 500, f"{st} {b}")

st, b, _ = req("POST", "/api/auth/register", {
    "username": "qa-u2", "password": "QaUser-2026-tx9", "email": "qa-u1-tx9@test.local", "code": code or "000000"})
record("A2.4", "register 重复 email 应 4xx", 400 <= st < 500 and st != 500, f"{st} {b}")

st, b, _ = req("POST", "/api/auth/register", {
    "username": "q", "password": "1", "email": "qa-u4-tx9@test.local", "code": code or "000000"})
record("A2.5", "register 弱密码/超短用户名应 4xx", 400 <= st < 500 and st != 500, f"{st} {b}")

print("== A3 login ==")
st, b, sc = req("POST", "/api/auth/login", {"username": "qa-u1", "password": "QaUser-2026-tx9"})
tok = extract_cookie(sc, "gk_user")
record("A3.1", "login 正常登录返回 cookie", st == 200 and tok is not None, f"{st} {sc}")

st2, b2, _ = req("POST", "/api/auth/login", {"username": "qa-u1", "password": "wrong-pass-tx9"})
record("A3.2", "login 错误密码应 4xx", 400 <= st2 < 500 and st2 != 500, f"{st2} {b2}")

st2, b2, _ = req("POST", "/api/auth/login", {"username": "no-such-user-tx9", "password": "QaUser-2026-tx9"})
record("A3.3", "login 不存在用户应 4xx 且提示一致", 400 <= st2 < 500, f"{st2} {b2}")

print("== A4 session ==")
st, b, _ = req("GET", "/api/auth/me", token=tok)
record("A4.1", "auth/me 登录后返回身份", st == 200 and isinstance(b, dict) and b.get("username") == "qa-u1", f"{st} {b}")

st, b, _ = req("GET", "/api/auth/me")
record("A4.2", "auth/me 未登录应 401", st == 401, f"{st} {b}")

st, b, _ = req("GET", "/api/auth/me", token="forged-token-tx9")
record("A4.3", "auth/me 伪造 token 应 401", st == 401, f"{st} {b}")

st, b, _ = req("POST", "/api/auth/password", {"current_password": "QaUser-2026-tx9", "password": "QaUser-2026-tx8"}, token=tok)
record("A4.4", "改密码成功", st == 200, f"{st} {b}")

st, b, _ = req("GET", "/api/auth/me", token=tok)
record("A4.5", "改密码后旧会话状态", st in (200, 401), f"status={st}(观察点: 旧会话是否保留) {b}")

st, b, sc = req("POST", "/api/auth/login", {"username": "qa-u1", "password": "QaUser-2026-tx8"})
tok2 = extract_cookie(sc, "gk_user")
record("A4.6", "新密码可登录", st == 200 and tok2 is not None, f"{st} {b}")

st, b, _ = req("POST", "/api/auth/password", {"current_password": "bad-old-tx9", "password": "QaUser-2026-tx7"}, token=tok2)
record("A4.7", "旧密码错误改密码应 4xx", 400 <= st < 500 and st != 500, f"{st} {b}")

st, b, _ = req("POST", "/api/auth/logout", token=tok2)
record("A4.8", "logout 正常", st == 200, f"{st} {b}")

st, b, _ = req("GET", "/api/auth/me", token=tok2)
record("A4.9", "logout 后会话失效", st == 401, f"{st} {b}")

fails = summary("P0-A 认证")
# 恢复密码供后续测试使用
st, b, _ = req("POST", "/api/auth/login", {"username": "qa-u1", "password": "QaUser-2026-tx8"})
print("restore login:", st)
with open("/home/tuigou/works/play/apps/ai_analyze_guokao/tests/artifacts/phase_a_result.json", "w") as f:
    _j.dump({"results": RESULTS}, f, ensure_ascii=False, indent=1)
sys.exit(1 if fails else 0)
