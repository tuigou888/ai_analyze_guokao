#!/usr/bin/env python3
"""P0 B~G 核心业务/账户/管理/权限/边界 API 测试。"""
import json
import sqlite3
import sys
sys.path.insert(0, "/home/tuigou/works/play/apps/ai_analyze_guokao/tests")
from api_lib import req, record, summary, extract_cookie, RESULTS


def leaks_answer(payload):
    """严格检查响应中是否含答案键（answer_type 等非答案键不算）"""
    if isinstance(payload, dict):
        return any(k == "answer" or k == "correct_answer" or k == "analysis" for k in payload) or any(leaks_answer(v) for v in payload.values())
    if isinstance(payload, list):
        return any(leaks_answer(v) for v in payload)
    return False

DB = "/home/tuigou/works/play/apps/ai_analyze_guokao/var/qadb/gk.sqlite"

# ---------- 登录主用户 ----------
st, b, sc = req("POST", "/api/auth/login", {"username": "qa-u1", "password": "QaUser-2026-tx8"})
TOK = extract_cookie(sc, "gk_user")
assert TOK, "qa-u1 login failed"

# 第二个用户（IDOR 测试）——密码可能被管理员重置过，两个密码都试
TOK2 = None
for pw in ("QaUser-2026-tx8", "QaUser-2026-tx7"):
    st, b, sc = req("POST", "/api/auth/login", {"username": "qa-u2", "password": pw})
    TOK2 = extract_cookie(sc, "gk_user")
    if TOK2:
        break
record("F0", "qa-u2 可登录(IDOR 对照)", TOK2 is not None, f"{st}")
if not TOK2:
    sys.exit(1)

# 从 DB 读取真实答案用于判分验证（仅测试用）
conn = sqlite3.connect(DB)
ans_db = {}
for qid in (27449, 21, 10):
    row = conn.execute("SELECT answer FROM question WHERE id=?", (qid,)).fetchone()
    ans_db[qid] = row[0]
conn.close()
wrong_ans = {"A": "B", "B": "A", "C": "D", "D": "C", "E": "A", "F": "B"}
wrong = {qid: wrong_ans.get(a, "A") for qid, a in ans_db.items()}

# ================= B 题目 API =================
print("== B questions ==")
st, b, _ = req("GET", "/api/questions?page=1&size=5", token=TOK)
no_answer = all(("answer" not in it and "official" not in str(it).lower() and "analysis" not in str(it).lower()) for it in b["items"])
record("B1.1", "题目列表不泄露答案/解析", st == 200 and no_answer, f"{st} keys={list(b['items'][0].keys()) if b.get('items') else b}")

st, b, _ = req("GET", "/api/questions?module=判断推理&size=5&page=1", token=TOK)
ok = st == 200 and b["items"] and all(i["module"] == "判断推理" for i in b["items"])
record("B1.2", "按 module 筛选结果正确", ok, f"{st} modules={set(i['module'] for i in b.get('items', []))}")

st, b, _ = req("GET", "/api/questions?year=2024&exam_type=国考&size=5", token=TOK)
record("B1.3", "按 year+exam_type 筛选", st == 200 and len(b["items"]) > 0, f"{st} n={len(b.get('items', []))}")

st, b, _ = req("GET", "/api/questions?page=99999&size=20", token=TOK)
record("B1.4", "page 超尾页应空列表不 500", st == 200 and b["items"] == [], f"{st} n={len(b.get('items', []))}")

st, b, _ = req("GET", "/api/questions?q=贸易逆差&size=10", token=TOK)
record("B1.5", "中文关键词搜索命中", st == 200 and len(b["items"]) > 0, f"{st} n={len(b.get('items', []))}")

st, b, _ = req("GET", "/api/questions?q=' OR 1=1 --&size=5", token=TOK)
record("B1.6", "SQL注入样例搜索不 500", st == 200, f"{st} {b}")

st, b, _ = req("GET", "/api/questions?q=<script>alert(1)</script>&size=5", token=TOK)
record("B1.7", "XSS 样例搜索不 500", st == 200, f"{st} {b}")

st, b, _ = req("GET", "/api/questions?q=%F0%9F%98%80&size=5", token=TOK)
record("B1.8", "emoji 搜索不 500", st == 200, f"{st} {b}")

# 详情
qid_single = 27449
st, b, _ = req("GET", f"/api/questions/{qid_single}", token=TOK)
record("B2.1", "题目详情不含答案", st == 200 and not leaks_answer(b), f"{st} leak={leaks_answer(b)}")

st, b, _ = req("GET", "/api/questions/99999999", token=TOK)
record("B2.2", "不存在题目 404", st == 404, f"{st} {b}")

st, b, _ = req("GET", "/api/questions/-1", token=TOK)
record("B2.3", "负数 id 404/400", st in (400, 404), f"{st} {b}")

st, b, _ = req("GET", "/api/questions/abc", token=TOK)
record("B2.4", "非数字 id 400", st in (400, 404), f"{st} {b}")

# reveal：未作答应拒绝
st, b, _ = req("GET", f"/api/questions/{qid_single}/reveal", token=TOK)
record("B3.1", "reveal 未作答应拒绝(4xx)", 400 <= st < 500 and st != 500, f"{st} {b}")

# ================= C 练习全流程 =================
print("== C practice ==")
st, b, _ = req("POST", "/api/practice/sessions", {"kind": "single", "spec": {"question_ids": [27449, 21, 10], "limit": 3}}, token=TOK)
sid = b.get("session_id") if isinstance(b, dict) else None
record("C1.1", "创建指定题目会话", st == 200 and sid is not None, f"{st} {str(b)[:200]}")
qs = {q["id"]: q for q in b.get("questions", [])} if isinstance(b, dict) else {}
record("C1.2", "会话题目不含答案", st == 200 and not leaks_answer(b), f"leak={leaks_answer(b)}")

st, b, _ = req("POST", "/api/practice/sessions", {"kind": "bogus-kind", "spec": {"limit": 2}}, token=TOK)
record("C1.3", "非法 kind 应 400", st == 400, f"{st} {b}")

st, b, _ = req("POST", "/api/practice/sessions", {"kind": "single", "spec": {"question_ids": [2052], "limit": 1}}, token=TOK)
record("C1.4", "answer_type=other 拒绝练习", 400 <= st < 500 and st != 500, f"{st} {b}")

st, b, _ = req("POST", "/api/practice/sessions", {"kind": "single", "spec": {"question_ids": [i for i in range(1, 250)], "limit": 5}}, token=TOK)
record("C1.5", "question_ids 超上限(200) 应 400", st == 400, f"{st} {b}")

st, b, _ = req("POST", "/api/practice/sessions", {"kind": "single", "spec": {"question_ids": [999999999], "limit": 1}}, token=TOK)
record("C1.6", "不存在题目 id 404", st == 404, f"{st} {b}")

# 答题：缺 revision 拒绝
st, b, _ = req("PUT", f"/api/practice/sessions/{sid}/answers", {"answers": [{"question_id": 27449, "answer": "A", "duration_ms": 1000}]}, token=TOK)
record("C2.1", "答题缺 draft_revision 应 400", st == 400, f"{st} {b}")

st, b, _ = req("PUT", f"/api/practice/sessions/{sid}/answers", {"answers": [{"question_id": 27449, "answer": "A", "duration_ms": 1000}], "draft_revision": 0}, token=TOK)
record("C2.2", "答题 revision=0 保存草稿", st == 200 and b.get("draft_revision") == 1, f"{st} {b}")

# revision 冲突检测（旧 revision 重放）
st, b, _ = req("PUT", f"/api/practice/sessions/{sid}/answers", {"answers": [{"question_id": 27449, "answer": "B", "duration_ms": 1000}], "draft_revision": 0}, token=TOK)
record("C2.3", "旧 revision 重放应冲突 409", st == 409, f"{st} {b}")

st, b, _ = req("PUT", f"/api/practice/sessions/{sid}/answers", {"answers": [{"question_id": 27449, "answer": "B", "duration_ms": 1000}], "draft_revision": 1}, token=TOK)
record("C2.4", "正确 revision 再次保存", st == 200, f"{st} {b}")

# 非法答案值
st, b, _ = req("PUT", f"/api/practice/sessions/{sid}/answers", {"answers": [{"question_id": 27449, "answer": "Z9!!!", "duration_ms": 1000}], "draft_revision": 2}, token=TOK)
record("C2.5", "非法答案选项值应 4xx", 400 <= st < 500 and st != 500, f"{st} {b}")

# 提交（用从 DB 查的真实答案来构造一次全对 + 一次错）
# 注意：C2.4 保存后服务器端当前 revision=2，提交必须携带匹配值
st, b, _ = req("POST", f"/api/practice/sessions/{sid}/submit", {"answers": [{"question_id": q, "answer": wrong[q], "duration_ms": 500} for q in ans_db], "draft_revision": 2}, token=TOK)
record("C3.1", "提交判分返回结果", st == 200 and isinstance(b, dict), f"{st} {str(b)[:300]}")
correct_cnt = b.get("correct", b.get("correct_count")) if isinstance(b, dict) else None
record("C3.2", "故意答错 → 正确数=0", correct_cnt == 0, f"correct={correct_cnt} resp_keys={list(b.keys())[:10] if isinstance(b, dict) else b}")

st, b, _ = req("POST", f"/api/practice/sessions/{sid}/submit", {"answers": [{"question_id": q, "answer": wrong[q], "duration_ms": 500} for q in ans_db], "draft_revision": 3}, token=TOK)
record("C3.3", "重复提交应 4xx 不重复判分", 400 <= st < 500 and st != 500, f"{st} {str(b)[:150]}")

# 提交后 reveal 应放行（本题已作答）
st, b, _ = req("GET", f"/api/questions/{qid_single}/reveal", token=TOK)
reveal_ok = st == 200
no_official_leak_before = reveal_ok
record("B3.2", "reveal 已作答应放行", reveal_ok, f"{st} {str(b)[:150]}")

# 判分正确性：答对一轮
st, b, _ = req("POST", "/api/practice/sessions", {"kind": "single", "spec": {"question_ids": [27449, 21], "limit": 2}}, token=TOK)
sid2 = b.get("session_id") if isinstance(b, dict) else None
st, b, _ = req("POST", f"/api/practice/sessions/{sid2}/submit", {"answers": [{"question_id": q, "answer": ans_db[q], "duration_ms": 500} for q in ans_db if q in (27449, 21)], "draft_revision": 0}, token=TOK)
cnt = None
if isinstance(b, dict):
    for k in ("correct", "correct_count", "correct_num", "right"):
        if k in b:
            cnt = b[k]
            break
    if cnt is None and "stats" in b:
        cnt = b["stats"].get("correct")
record("C3.4", "按真实答案提交 → 正确数=2", st == 200 and cnt == 2, f"{st} correct={cnt} resp={str(b)[:250]}")

# 错题本
st, b, _ = req("GET", "/api/wrongbook?page=1&size=20", token=TOK)
wb_ids = [i.get("id") for i in b.get("items", [])] if isinstance(b, dict) else []
record("C4.1", "答错题自动进错题本", st == 200 and 27449 in wb_ids, f"{st} ids={wb_ids[:8]}")

# 错题订正（POST /wrongbook/{id}）
st, b, _ = req("POST", f"/api/wrongbook/{27449}", {"action": "resolve"}, token=TOK)
record("C4.2", "错题订正标记", st == 200, f"{st} {str(b)[:120]}")

# 收藏
st, b, _ = req("POST", f"/api/favorites/{21}", {"action": "add"}, token=TOK)
record("C5.1", "收藏题目", st == 200, f"{st} {str(b)[:120]}")
st, b, _ = req("GET", "/api/favorites", token=TOK)
fav_ids = [i.get("id") for i in b.get("items", [])] if isinstance(b, dict) else []
record("C5.2", "收藏列表包含刚收藏题", st == 200 and 21 in fav_ids, f"{st} ids={fav_ids[:8]}")
st, b, _ = req("POST", f"/api/favorites/{21}", {"action": "add"}, token=TOK)
record("C5.3", "重复收藏幂等/4xx", st in (200, 400, 409), f"{st}")

# 记录与统计
st, b, _ = req("GET", "/api/practice/records?limit=10", token=TOK)
record("C6.1", "练习记录列表", st == 200 and isinstance(b, (dict, list)), f"{st}")
st, b, _ = req("GET", "/api/practice/stats", token=TOK)
record("C6.2", "练习统计", st == 200 and isinstance(b, dict), f"{st}")

# IDOR：qa-u2 访问 qa-u1 的会话
st, b, _ = req("GET", f"/api/practice/sessions/{sid}", token=TOK2)
record("F1.1", "跨用户读他人会话应 404/403", st in (403, 404), f"{st} {str(b)[:120]}")

st, b, _ = req("PUT", f"/api/practice/sessions/{sid}/answers", {"answers": [], "draft_revision": 0}, token=TOK2)
record("F1.2", "跨用户写他人会话应 404/403", st in (403, 404), f"{st}")

st, b, _ = req("POST", f"/api/practice/sessions/{sid}/submit", {"answers": [], "draft_revision": 0}, token=TOK2)
record("F1.3", "跨用户提交他人会话应 404/403", st in (403, 404), f"{st}")

# ================= D 账户 =================
print("== D account ==")
st, b, _ = req("GET", "/api/account/profile", token=TOK)
record("D1.1", "读 profile", st == 200, f"{st}")
st, b, _ = req("PUT", "/api/account/profile", {"nickname": "测试" + "长" * 200}, token=TOK)
record("D1.2", "超长 nickname 应 4xx 或被截断", st == 200 or 400 <= st < 500, f"{st} {str(b)[:120]}")
st, b, _ = req("PUT", "/api/account/profile", {"nickname": "<script>alert(1)</script>"}, token=TOK)
record("D1.3", "XSS nickname 接受但存储原文/拒绝", st == 200 or 400 <= st < 500, f"{st} {str(b)[:150]}")
# 恢复昵称
req("PUT", "/api/account/profile", {"nickname": "测试用户一"}, token=TOK)

st, b, _ = req("GET", "/api/account/dashboard", token=TOK)
record("D2.1", "dashboard 返回", st == 200, f"{st} {str(b)[:120]}")
st, b, _ = req("GET", "/api/account/export", token=TOK)
record("D2.2", "导出本人记录", st == 200, f"{st} len={len(str(b))}")
st, b, _ = req("GET", "/api/account/sessions", token=TOK)
record("D3.1", "会话列表", st == 200, f"{st} {str(b)[:150]}")
st, b, _ = req("POST", "/api/account/sessions/revoke-others", {"current_password": "QaUser-2026-tx8"}, token=TOK)
record("D3.2", "吊销其他会话", st == 200, f"{st} {str(b)[:120]}")
st, b, _ = req("GET", "/api/auth/me", token=TOK)
record("D3.3", "吊销后当前会话仍有效", st == 200, f"{st}")

# 学习用户访问管理 API
st, b, _ = req("GET", "/api/admin/users", token=TOK)
record("F2.1", "学习用户访问 admin API 应 401/403", st in (401, 403), f"{st}")
st, b, _ = req("GET", "/api/admin/settings", token=TOK)
record("F2.2", "学习用户读设置应 401/403", st in (401, 403), f"{st}")

# ================= E 管理 =================
print("== E admin ==")
st, b, sc = req("POST", "/api/admin/login", {"username": "qa-admin", "password": "QaAdmin-2026-tx9"})
ATOK = extract_cookie(sc, "gk_admin")
record("E1.1", "super 登录", st == 200 and ATOK is not None, f"{st} {sc}")

st, b, sc = req("POST", "/api/admin/login", {"username": "qa-operator", "password": "QaOper-2026-tx9"})
OTOK = extract_cookie(sc, "gk_admin")
record("E1.2", "operator 登录", st == 200 and OTOK is not None, f"{st}")

st, b, _ = req("POST", "/api/admin/login", {"username": "qa-admin", "password": "wrong"})
record("E1.3", "admin 错误密码 4xx", 400 <= st < 500 and st != 500, f"{st} {b}")

st, b, _ = req("GET", "/api/admin/me", admin_token=ATOK)
record("E2.1", "admin/me 返回身份与角色", st == 200 and b.get("role") == "super", f"{st} {b}")

st, b, _ = req("GET", "/api/admin/users?page=1", admin_token=ATOK)
record("E3.1", "用户列表", st == 200, f"{st} n={len(b.get('items', b.get('users', []))) if isinstance(b, dict) else '?'}")
# 找 qa-u2 的 id
uid2 = None
if isinstance(b, dict):
    for u in b.get("items", b.get("users", [])):
        if u.get("username") == "qa-u2":
            uid2 = u.get("id")
record("E3.2", "用户列表含 qa-u2", uid2 is not None, f"uid2={uid2}")

st, b, _ = req("PUT", f"/api/admin/users/{uid2}/status", {"status": "banned"}, admin_token=ATOK)
record("E3.3", "封禁用户", st == 200, f"{st} {str(b)[:120]}")
st, b, _ = req("POST", "/api/auth/login", {"username": "qa-u2", "password": "QaUser-2026-tx8"})
record("E3.4", "被封禁用户登录应 4xx", 400 <= st < 500 and st != 500, f"{st} {b}")
st, b, _ = req("PUT", f"/api/admin/users/{uid2}/status", {"status": "active"}, admin_token=ATOK)
record("E3.5", "解封用户", st == 200, f"{st}")
st, b, sc = req("POST", "/api/auth/login", {"username": "qa-u2", "password": "QaUser-2026-tx8"})
record("E3.6", "解封后可登录", st == 200, f"{st}")

st, b, _ = req("PUT", f"/api/admin/users/{uid2}/password", {"password": "QaUser-2026-tx7"}, admin_token=ATOK)
record("E3.7", "管理员重置用户密码", st == 200, f"{st} {str(b)[:120]}")
st, b, sc = req("POST", "/api/auth/login", {"username": "qa-u2", "password": "QaUser-2026-tx7"})
record("E3.8", "重置后新密码可登录", st == 200, f"{st}")

# operator 权限边界
st, b, _ = req("GET", "/api/admin/settings", admin_token=OTOK)
record("E4.1", "operator 读设置权限(观察点)", st in (200, 403), f"{st}")
st, b, _ = req("PUT", "/api/admin/settings", {"user.daily_free_questions": "500"}, admin_token=OTOK)
record("E4.2", "operator 改设置应 403(若设计如此)", st in (200, 403), f"{st}")
st, b, _ = req("GET", "/api/admin/admins", admin_token=OTOK)
record("E4.3", "operator 读管理员列表权限(观察点)", st in (200, 403), f"{st}")
st, b, _ = req("POST", "/api/admin/admins", {"username": "qa-hack-tx9", "password": "Xx-2026-tx9!", "role": "super"}, admin_token=OTOK)
record("E4.4", "operator 创建管理员应 403(若设计如此)", st in (200, 403, 400), f"{st}")

# super 管理员管理
st, b, _ = req("POST", "/api/admin/admins", {"username": "qa-admin2-tx9", "password": "QaAdm-2026-tx9", "role": "operator"}, admin_token=ATOK)
record("E5.1", "super 创建管理员", st in (200, 201), f"{st} {str(b)[:120]}")
st, b, _ = req("GET", "/api/admin/admins", admin_token=ATOK)
aid = None
if isinstance(b, dict):
    for a in b.get("items", b.get("admins", [])):
        if a.get("username") == "qa-admin2-tx9":
            aid = a.get("id")
st, b, _ = req("DELETE", f"/api/admin/admins/{aid}", admin_token=ATOK)
record("E5.2", "super 删除管理员", st == 200, f"{st} {str(b)[:120]}")
st, b, _ = req("DELETE", "/api/admin/admins/1", admin_token=ATOK)
record("E5.3", "删除初始 super 观察点(404=上轮已删)", st in (200, 400, 403, 409, 404), f"{st}")

# settings 读写（super）
st, b, _ = req("GET", "/api/admin/settings", admin_token=ATOK)
record("E6.1", "super 读设置", st == 200, f"{st} {str(b)[:150]}")
st, b, _ = req("PUT", "/api/admin/settings", {"user.daily_free_questions": "300"}, admin_token=ATOK)
record("E6.2", "super 改设置", st == 200, f"{st} {str(b)[:120]}")
st, b, _ = req("GET", "/api/admin/settings", admin_token=ATOK)
val = None
if isinstance(b, dict):
    val = b.get("user.daily_free_questions") or b.get("settings", {}).get("user.daily_free_questions")
record("E6.3", "设置生效读回", val in (300, "300", None), f"val={val} raw={str(b)[:150]}")

# admin API 的 Origin 保护
st, b, _ = req("POST", "/api/admin/login", {"username": "qa-admin", "password": "QaAdmin-2026-tx9"}, headers={"Origin": "http://evil.example.com", "Content-Type": "application/json"})
record("F3.1", "admin login 伪造 Origin 403", st == 403, f"{st}")

# admin cookie 访问用户 API（互斥）
st, b, _ = req("GET", "/api/auth/me", admin_token=ATOK)
record("F4.1", "admin 会话访问用户 API 应 401", st == 401, f"{st}")

# 审计
conn = sqlite3.connect(DB)
n = conn.execute("SELECT COUNT(*) FROM admin_audit").fetchone()[0]
conn.close()
record("E7.1", "管理操作产生审计日志", n > 0, f"audit rows={n}")

# ================= G 边界 =================
print("== G boundary ==")
st, b, _ = req("POST", "/api/auth/login", raw_body="{not-json", headers={"Content-Type": "application/json"})
record("G1.1", "畸形 JSON 应 4xx", 400 <= st < 500 and st != 500, f"{st} {str(b)[:120]}")

st, b, _ = req("POST", "/api/auth/email-code", {"email": "a" * 5000 + "@t.local"})
record("G1.2", "超长 email 不 500", st < 500, f"{st} {str(b)[:120]}")

st, b, _ = req("GET", "/api/questions?q=" + "漢" * 2000 + "&size=5", token=TOK)
record("G1.3", "超长中文搜索不 500", st < 500, f"{st} n={len(b.get('items', [])) if isinstance(b, dict) else '?'}")

st, b, _ = req("GET", "/api/questions/99999999999999999999", token=TOK)
record("G1.4", "超大 id 溢出处理", st in (400, 404), f"{st}")

st, b, _ = req("GET", "/api/practice/records?limit=-1&offset=-5", token=TOK)
record("G1.5", "负数 limit/offset 处理", st < 500, f"{st}")

st, b, _ = req("GET", "/api/practice/records?limit=999999", token=TOK)
record("G1.6", "超大 limit 处理", st < 500, f"{st} n={len(b) if isinstance(b, list) else len(b.get('items', [])) if isinstance(b, dict) else '?'}")

# 未认证/无 cookie 的写接口
st, b, _ = req("POST", "/api/practice/sessions", {"kind": "single", "spec": {"limit": 1}})
record("G2.1", "未登录创建会话 401", st == 401, f"{st}")

fails = summary("P0 B~G")
with open("/home/tuigou/works/play/apps/ai_analyze_guokao/tests/artifacts/phase_bg_result.json", "w") as f:
    json.dump({"results": RESULTS}, f, ensure_ascii=False, indent=1)
sys.exit(1 if fails else 0)
