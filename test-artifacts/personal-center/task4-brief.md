# 完整个人中心实现计划

> 按 subagent-driven-development 顺序实现并逐项审查；用户已要求开始代码开发，生成计划后直接执行。保留现有工作区修复，不提交、推送或部署。

**目标：** 交付真实数据驱动的个人学习工作台。
**技术栈：** Go/Chi/SQLite，Vue 3/SVG/CSS；不引入图表或存储依赖。
**规格：** [个人中心设计](../specs/2026-10-04-personal-center-design.md)。

## 全局约束与接口

- 追加 v12，保留 v1–v11，所有测试写临时库；禁止操作正式 DB/密钥。
- 后端只根据 Cookie 中已验证身份访问个人数据，保持 Origin、草稿 revision、交卷幂等。
- 统计北京时间；分母 0 返回 null；模块/考点少于 5 次显示不足；默认 30 天，可选 7/30/90。
- 8 个预置头像编号 0–7；字号 16/18/20；题量 10/20/50；默认头像 0、字号16、题量20、作答目标20、分钟目标30。
- `Profile` JSON 字段：username,nickname,bio,avatar_id,created_at,updated_at,daily_questions,daily_minutes,exam_name,exam_date,default_limit,default_module,reading_size,revision。整数头像编号，int64 revision。
- `ProfileUpdate` 只有可编辑字段与 revision。保存 POST/PATCH 不新增：使用 PUT `/api/account/profile`。
- `Service.Clock func() time.Time` 为可选测试时钟，`clockNow()` 内部默认真实时间；不改变已有调用的 DB 字段。
- 所有源码/测试随工作区交付；流程审查使用每任务修改文件快照 diff，避免把既有未提交修复算作本轮新增。

## Review Focus

1. 两账号隔离：profile/统计/CSV/会话/复习候选都必须按用户过滤。
2. 午夜/空答案/重复提交：无错日、重复计时和 NaN；固定 Clock 的统计回归覆盖。
3. profile 旧版本和离开页面：保留本地输入，禁止静默覆盖；HTTP CAS + 浏览器延迟保存覆盖。
4. 会话撤销：不得泄漏 token/hash、误撤销当前/他人、伪造 XFF 地址；有真实两 Cookie 验证。
5. CSV/稀疏标签：无公式注入或静默截断、未解锁答案/草稿泄漏；推荐明确样本限制。

## Task 4：集成验收与交付

**文件：** 新增 `test-artifacts/personal_center_regression.py` 和个人中心证据目录、`docs/personal-center.md`；更新 API/部署/README/Makefile 发布文档，完成实现进度文件。

- [ ] 核对规格逐条覆盖，追加跨任务缺失测试；既有24项浏览器回归须通过。
- [ ] 运行 Go 全量、核心包race/vet、前端构建、Linux与Windows/macOS编译；v11真实结构副本v12升级与完整备份恢复验收。
- [ ] 仅在临时DB/合成密钥环境启动浏览器验收服务，记录截图/JSON/命令与结果，结束关闭本任务服务。
- [ ] 最终独立宽范围代码审查，修复有效严重发现并复测相关用例。
- [ ] 更新源码/文档与本地Linux发布包，校验内容不含数据库、密钥、备份；记录SHA与未测生产边界。不自动提交或远程部署。

## 本次分派的执行上下文

- Task 1/2 已通过独立审查，Task 3 的最新结论以 task3-review.md 为准。禁止重新实现已完成功能；如集成出现产品缺陷，先报告根因与证据，再由主代理安排修复。
- 原有未提交修改必须保留。Git 区域只读，使用 `_work/task4-before/` 源文件快照生成本任务 diff，不提交、不推送、不部署。不要派子智能体。
- 根代理新跑 Go 全量、核心四包 race、Go vet 均通过；完整命令及输出在 `task4-root-precheck.json`。新增 Go 测试后补跑相关包和必要全量，不重复未受影响的重型 race。
- 临时服务在 18083，root 启动会话 11444，使用 `_work/task3.sqlite` 和合成密钥；正式 DB 与真实密钥禁止操作。前端修复后服务二进制可能由 Task 3 实现者更新，以最新报告为准。测试结束由主代理关闭拥有的服务。
- 既有 v11→v12 真实结构测试是 `internal/store/personal_migration_test.go`。补充重点是完整 bundle 恢复后保留非默认 profile/偏好/revision、公开会话与已提交练习，并能继续按原 Cookie 鉴权、按原资料版本更新；全部 fixture 在 t.TempDir。
- 旧浏览器 18 项位于 `test-artifacts/ui_regression.py`，管理员 fixture 为 webtester/ui-test-password，脚本会改管理员密码；只能在隔离库准备账号，不能放宽旧断言。另 6 项位于 round2_fixes_ui.py。
- 交付需要 docs/personal-center.md、API 合同、README入口、部署 v12 升级/回滚说明、Makefile发布文档列表。保留历史审查报告的原始结论，新增记录说明本轮证据。
- 保存逐条规格覆盖矩阵、命令与退出码、包清单和 SHA-256；Linux包不得含数据库、密钥、备份或测试 fixture。跨编译不等同于 Windows/macOS 原生运行验收，线上 HTTPS/压力/异机恢复仍明确为未测。
- 输出报告 `docs/personal-center-task4-report.md` 与 diff `test-artifacts/personal-center/task4-review.diff`。仅将实现状态标记“待终审”；主代理安排独立任务/全功能终审并关闭进度。
