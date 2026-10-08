# 网站 API（p3-next-steps A/B/C 实现）

所有业务接口在 `/api` 下，通常以 JSON 收发；个人记录导出成功为 `text/csv` 附件。错误格式：`{"error":"错误说明"}`。普通用户通过 HttpOnly Cookie `gk_user` 鉴权；管理员使用独立 Cookie `gk_admin`，两者不互通。会话存储在 SQLite，普通用户 token 只存 SHA-256 摘要，密码使用 bcrypt。写请求检查同源，API 响应不缓存。

页面可对普通用户业务请求和 `/auth/logout` 携带 `X-GK-Expected-User: <已验证用户id>`（规范正整数，仅一个值）。服务器先检查 Origin（写请求）和 Cookie，再与该 Cookie 的已验证用户比较；不符为 409 `{"code":"account_changed","error":"登录账户已变化，请记录未保存输入后重新进入个人中心"}`，在资料、CSV、会话、改密、重算统计及其他普通用户业务执行前拒绝。非法前提为 400，身份无效仍为 401；400/409 不修改有效 Cookie。该 header 只能拒绝，所有查询和写入对象始终从已验证 Cookie 获取；不接受它作为用户选择/授权，也不向 ProfileUpdate 加身份字段。

带前提的 logout 成功仅撤销已验证 Cookie 对应 token，不返回删除 Cookie 的 Set-Cookie，防止迟到 A 退出响应删除后来 B 的 Cookie；新页面随后清理本地身份并通知其他标签。旧 HttpOnly Cookie 可留到过期或下次登录，但对应 token 已失效，不再能认证，不产生新凭据。不携带该前提的原客户端继续使用原合同，logout 保持原幂等及删除 Cookie。管理员和公开登录/注册/身份发现接口不采用此前提。页面将前提固定于发起表单的原账号，跨标签通知只失效旧页面假设并提示重新进入，不授予新账号身份；通知未到也由服务器拒绝错账号请求。迟到的身份不符响应只失效其发起账号，不能清除后来登录账号的偏好。

## 账户

| 方法 | 路径 | 请求 / 响应 |
|---|---|---|
| POST | `/auth/register` | `{username,password,nickname?}` → 201 `{id,username,nickname}`；注册不自动签发会话 |
| POST | `/auth/login` | `{username,password}` → 用户资料 + Cookie |
| POST | `/auth/logout` | 撤销当前会话 |
| GET | `/auth/me` | 当前用户资料；未登录 401 |
| POST | `/auth/password` | `{current_password,password}`；改密后撤销所有旧会话 |

用户名 3–32 字符，只支持字母、数字、`_`、`-`；密码 8–72 字节；昵称最多 40 字。用户名冲突 409。登录失败统一 401。登录、注册、管理员登录每来源每分钟最多 30 次，密码计算最多 2 个并行任务，超过返回 429。

## 个人中心（v12，需要普通用户 Cookie）

| 方法 | 路径 | 请求 / 响应 |
|---|---|---|
| GET | `/account/profile` | 本人 Profile；缺行兼容默认值，GET 不隐式写入 |
| PUT | `/account/profile` | 完整 ProfileUpdate + revision → 保存后的 Profile；400 校验错误，409 旧版本 |
| GET | `/account/dashboard?days=30` | days 仅 7/30/90；Dashboard |
| GET | `/account/sessions?page=1` | `{items,total,page,size:20}`，本人未过期会话 |
| DELETE | `/account/sessions/{public_id}` | 撤销本人其他会话；当前为 400，他人/不存在为 404 |
| POST | `/account/sessions/revoke-others` | 空 body 或 `{}` → `{revoked}`，保留当前 Cookie |
| POST | `/account/review/wrongbook` | 空 body 或 `{}` → 原 Session；本人待订正自动错题最多 20 道，无候选 400 |
| GET | `/account/export?days=30` | days 仅 7/30/90/all；带 BOM 的 UTF-8 CSV，固定附件名 `study-records.csv`；超过 5,000 行整体 400 |

Profile 字段：`username,nickname,bio,avatar_id,created_at,updated_at,daily_questions,daily_minutes,exam_name,exam_date,default_limit,default_module,reading_size,revision`。ProfileUpdate 只接受 `nickname,bio,avatar_id,daily_questions,daily_minutes,exam_name,exam_date,default_limit,default_module,reading_size,revision`，每个字段必填且不可 null；额外字段（包括 user_id 和大小写别名）拒绝。revision 是独立于草稿版本的整数；昵称与资料在同一事务中保存。409 后保留输入，由用户确认重新加载，不自动重试旧数据。

昵称/简介/考试名称最多 40/200/40 个 Unicode 字符；头像 0–7；每日作答 5–200、分钟 5–180；默认题量 10/20/50、模块为空或题库实际模块、字号 16/18/20。新考试日期须为北京时间未来日期且最多五年后；已存到期日期可原样保留更新其他资料。默认头像 0、作答 20、分钟 30、题量 20、全部模块、字号 16、revision 0。用户名/注册时间只读。

Dashboard 字段：`timezone,start_date,end_date,days,profile,lifetime,period,previous,today,trend,modules,activity,streak,achievements,review`。计数组为 `answered,correct,accuracy,duration_ms,sessions`；accuracy 是 0–100 数值或 null。trend 长度为 days，activity 固定最近 90 日，都含 date；modules 含 module/sufficient；streak 含 current/longest/practiced_today；achievements 含 id/name/current/target/unlocked。review 含 `unfinished,weak_modules,weak_concepts,labeled_answered,unmapped_labeled_answered,label_coverage,wrong,favorites,wrong_candidates`，空数组为 `[]`；续做最多 3 份、薄弱模块/可练考点最多 3 项且至少 5 次样本。缺稳定映射的标签仍计覆盖，不生成无效考点入口。详细统计口径见 [个人中心说明](personal-center.md)。

会话 item 只含 `public_id,current,device_label,ip_hint,created_at,expires_at`；历史元数据为 null，不返回 token/摘要。IP 存储前脱敏，来源使用可信代理规则。所有对象按已验证 Cookie 的用户过滤，客户端 user_id 不参与查询；管理员 Cookie 不通用。PUT/POST 要求 application/json，所有写接口含无 body DELETE 均检查 Origin，写入繁忙沿用 503/Retry-After。

CSV 每行：提交时间、练习类型、练习 ID、题号、模块、本人答案、正确 0/1、答案记录耗时。只导出本人已提交记录，不包含解析、草稿、密码、会话、密钥或其他账号；规范转义并防公式注入，超限不截断。

## 查询与搜索（需要普通用户登录）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/filters` | `modules,years,regions,exam_types,variants` 实际筛选值 |
| GET | `/papers` | 按模块、年份、地区、考试类型、卷别筛选；返回原卷模块，不合并不同模块为一套完整行测卷 |
| GET | `/questions` | 分页题库；返回 `{total,items,page,size}` |
| GET | `/questions/:id` | 题干、原始材料、OCR 文本、选项、题型、来源与分类摘要 |
| GET | `/questions/:id/reveal` | 用户有该题非空提交记录后返回答案、公式解析、完整标注；否则 403 |
| GET | `/search` | `{q,mode=keyword或concept,module,limit}`；返回 question_id/brief/module/tertiary/score/highlights；暂不提供语义搜索 |
| GET | `/concepts` | 按 module/secondary 筛选考点，包含真实题量和个人作答统计 |
| GET | `/concepts/:id` | 参考考点卡、易混信号、样例题；不公开样例题答案或推理链 |

题库支持：`module,year,region,exam_type,variant,paper_id,concept_id,q,page,size,order`。`size` 最大 100，默认 20；`order` 支持默认 ID 倒序、`oldest`、`random`。`paper_id` 筛选按原卷题号排序。

中文长度 ≥3 的连续关键词使用 FTS5 trigram，1–2 字词使用 `instr` 子串查找；keyword 模式也匹配考点名称，concept 模式只匹配已有考点标注。搜索只索引题干，解析与答案不进入索引。关键词作为引用的 FTS 短语处理，用户输入不能变成 FTS 运算表达式。当前 `score=1` 表示命中，不表示向量相似度。

题目查询中的 `answer_type` 是公开的题型；`answer`、`is_correct`、官方解析、推理链、最快解法均不公开。选项 DTO 不含原库的 `is_correct` 列。判断题由后端生成 A=正确/B=错误的展示选项。

原始题干/材料保留图片占位符，前端从 `/media/题目图/文件` 或 `/media/公式图/文件` 读取图片；不会把图形推理原图替换成 OCR 文本。媒体接口也需要普通用户身份，并限制目录、扩展名及路径越界。

## 练习

创建请求示例：

```json
{
  "kind": "single",
  "spec": { "module": "资料分析", "year": 2026, "limit": 50 }
}
```

`kind` 支持：

- `single`：指定 `question_ids`，或按筛选条件随机抽题。
- `paperset`：必须指定 `paper_id`，按原卷模块题序组卷，最多 200 题。
- `concept`：必须指定 `concept_id`，从已有标注覆盖的题目抽取。
- `wrongbook`：从当前用户待订正题抽取；也可以指定某个题号重练。

`spec` 继承题库筛选字段，并支持 `limit`（默认 20，上限 200）和 `question_ids`（最多 200、不能重复）。缺少标准答案的 `other` 题禁止加入练习，随机和原卷组卷自动排除。

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/practice/sessions` | 创建练习，冻结题序和条件；响应含 session_id/questions，但无标准答案 |
| GET | `/practice/sessions/:id` | 当前用户恢复草稿或复盘；其他用户 404 |
| PUT | `/practice/sessions/:id/answers` | `{draft_revision,answers:[{question_id,answer,duration_ms}]}` 保存草稿；不判分、不解锁解析 |
| POST | `/practice/sessions/:id/submit` | 同上，服务端判分；首次提交写入答案与错题、更新掌握度 |
| GET | `/practice/records?limit=&offset=` | `{total,items}`；包含未完成和已提交练习 |
| GET | `/practice/stats` | answered/correct/wrong/sessions |
| POST | `/practice/stats/rebuild` | 从历史提交记录重新计算当前用户的考点统计 |

单选/判断答案按字母相等，多选按字母集合相等；支持 A–H 等实际选项。客户端可按任意顺序提交多选，服务端归一化。非法字母、不属于本练习的题号、重复题号、负耗时返回 400。

写入容量耗尽时返回 503 与 `Retry-After: 1`；交卷等待遵循请求取消，并有 10 秒写入预算。用户可保留草稿后重试，不因繁忙被登出。

空答案/漏答计入本次总题数、判错，但不计个人累计作答、不加入错题本、也不解锁解析。已作答错题自动收录；后续答对将 `resolved=1`。重复交卷返回原结果，不重复累加统计或错题次数。

作答、交卷状态、错题和考点统计在一次短事务中提交。`practice_session.question_ids` 保存题序；历史展示引用题实体，因此题库内容被修正后历史展示也会更新。草稿保存在 `practice_session.draft`，只作为未提交状态，掌握度依据是 `practice_answer`。

## 错题与收藏

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/wrongbook?page=&size=&status=` | `{total,items,page,size}`；status=pending（默认）/resolved/all |
| POST | `/wrongbook/:id` | `{action:"favorite"}` 手动收藏，不增加 wrong_count |
| POST | `/wrongbook/:id` | `{action:"resolve"}` / `unresolve` / `delete`，仅影响当前用户 |

## 管理与运维

原有 `/admin/login,logout,me,settings,password,test-llm` 保留。`POST /admin/password` 要求 `{current_password,password}`，原密码验证后事务内更新并撤销会话。新增 `POST /admin/concepts/refresh`：只聚合已有标注，不调用模型；用户改密不授予管理权限。写请求必须携带完整同源 Origin 及 application/json。

`GET /healthz` 无需登录，检查数据库连接，正常 200 `{"ok":true}`，失败 503。

未知 `/api/*` 返回 JSON 404；业务页面由 SPA 路由接管，不把缺失 API 当 HTML 页面返回。

## 迁移与数据口径

v7 增加账户、会话、练习、错题、考点映射、掌握度、FTS5 和草稿字段。迁移 SQL 和版本记录在同一事务中提交，失败可回滚。

v9 增加 option.content_with_text，v10 增加 label_call 及批次历史基线/完整性字段，v11 增加 practice_session.draft_revision；程序启动自动迁移，升级前备份。蒸馏费用未知时报告明确标记，不将缺失 usage 当作零消耗。

v12 追加 user_profile（默认值回填、revision）、app_session 独立 public_id 与可空显示元数据，以及个人统计/活跃会话索引；不修改既有 token 摘要、密码和练习。完整恢复需使用同次 bundle 的数据库与有效密钥，升级/回滚见 [部署手册](deployment-2c4g.md)。

考点按每题最后插入的一条标注 `MAX(label.id)` 聚合；不会因为同题参与多个历史批次而重复计数。稳定 ID 的唯一键为 `(module,name)`，已存在 ID 不重建。当前卡片从已有最新标注取参考内容，不是 P5 的完整质量审查与聚合晋级产物。

启动网站自动刷新轻量考点映射；人工更新后可重算个人统计。全量蒸馏、完整聚合、向量检索和在线 AI 功能后置，本次没有执行这些任务。

## 第二轮修复后的练习写入契约

`GET /api/practice/sessions/{id}` 返回整数 `draft_revision`。`PUT .../answers` 与 `POST .../submit` 均需携带 `{ "draft_revision": 当前版本, "answers": [...] }`；缺失/负数版本返回 400。

保存使用原子版本比较并递增，成功返回 `{ "ok": true, "draft_revision": 新版本 }`。陈旧草稿和陈旧交卷返回 409，不改变服务器答案。前端串行执行保存并采用上次成功响应的版本；冲突时保留当前选择、暂停自动保存和交卷，提示先记录本页作答，再经确认加载服务器草稿。不可对冲突无条件更新版本并重发旧快照。

已经提交的同一练习重复交卷返回原判分，不再次写答案、错题或统计。独立 reveal 与练习复盘统一为：本人非空且已提交的答案允许查看解析，答错同样允许；草稿、漏答和他人答案不能解锁。普通用户错误原密码是 400 业务校验错误，401 继续仅表示身份验证失败。
