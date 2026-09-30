# 网站 API（p3-next-steps A/B/C 实现）

所有业务接口在 `/api` 下，以 JSON 收发。错误格式：`{"error":"错误说明"}`。普通用户通过 HttpOnly Cookie `gk_user` 鉴权；管理员使用独立 Cookie `gk_admin`，两者不互通。会话存储在 SQLite，普通用户 token 只存 SHA-256 摘要，密码使用 bcrypt。写请求检查同源，API 响应不缓存。

## 账户

| 方法 | 路径 | 请求 / 响应 |
|---|---|---|
| POST | `/auth/register` | `{username,password,nickname?}` → 201 `{id,username,nickname}`；注册不自动签发会话 |
| POST | `/auth/login` | `{username,password}` → 用户资料 + Cookie |
| POST | `/auth/logout` | 撤销当前会话 |
| GET | `/auth/me` | 当前用户资料；未登录 401 |
| POST | `/auth/password` | `{current_password,password}`；改密后撤销所有旧会话 |

用户名 3–32 字符，只支持字母、数字、`_`、`-`；密码 8–72 字节；昵称最多 40 字。用户名冲突 409。登录失败统一 401。登录、注册、管理员登录每来源每分钟最多 30 次，密码计算最多 2 个并行任务，超过返回 429。

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
| PUT | `/practice/sessions/:id/answers` | `{answers:[{question_id,answer,duration_ms}]}` 保存草稿；不判分、不解锁解析 |
| POST | `/practice/sessions/:id/submit` | 同上，服务端判分；首次提交写入答案与错题、更新掌握度 |
| GET | `/practice/records?limit=&offset=` | `{total,items}`；包含未完成和已提交练习 |
| GET | `/practice/stats` | answered/correct/wrong/sessions |
| POST | `/practice/stats/rebuild` | 从历史提交记录重新计算当前用户的考点统计 |

单选/判断答案按字母相等，多选按字母集合相等；支持 A–H 等实际选项。客户端可按任意顺序提交多选，服务端归一化。非法字母、不属于本练习的题号、重复题号、负耗时返回 400。

空答案/漏答计入本次总题数、判错，但不计个人累计作答、不加入错题本、也不解锁解析。已作答错题自动收录；后续答对将 `resolved=1`。重复交卷返回原结果，不重复累加统计或错题次数。

作答、交卷状态、错题和考点统计在一次短事务中提交。`practice_session.question_ids` 保存题序；历史展示引用题实体，因此题库内容被修正后历史展示也会更新。草稿保存在 `practice_session.draft`，只作为未提交状态，掌握度依据是 `practice_answer`。

## 错题与收藏

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/wrongbook?page=&size=&status=` | `{total,items,page,size}`；status=pending（默认）/resolved/all |
| POST | `/wrongbook/:id` | `{action:"favorite"}` 手动收藏，不增加 wrong_count |
| POST | `/wrongbook/:id` | `{action:"resolve"}` / `unresolve` / `delete`，仅影响当前用户 |

## 管理与运维

原有 `/admin/login,logout,me,settings,password,test-llm` 保留。新增 `POST /admin/concepts/refresh`：只聚合已有标注，不调用模型。管理员改密撤销旧管理员会话；用户改密不授予管理权限。

`GET /healthz` 无需登录，检查数据库连接，正常 200 `{"ok":true}`，失败 503。

未知 `/api/*` 返回 JSON 404；业务页面由 SPA 路由接管，不把缺失 API 当 HTML 页面返回。

## 迁移与数据口径

v7 增加账户、会话、练习、错题、考点映射、掌握度、FTS5 和草稿字段。迁移 SQL 和版本记录在同一事务中提交，失败可回滚。

考点按每题最后插入的一条标注 `MAX(label.id)` 聚合；不会因为同题参与多个历史批次而重复计数。稳定 ID 的唯一键为 `(module,name)`，已存在 ID 不重建。当前卡片从已有最新标注取参考内容，不是 P5 的完整质量审查与聚合晋级产物。

启动网站自动刷新轻量考点映射；人工更新后可重算个人统计。全量蒸馏、完整聚合、向量检索和在线 AI 功能后置，本次没有执行这些任务。
