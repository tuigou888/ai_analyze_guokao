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

## Task 2：学习分析、成就、复习与导出

**文件：** 新增 `internal/study/dashboard.go`、`internal/study/export.go`、`internal/study/review.go` 及测试；扩展 `internal/serve/account.go` 的看板/CSV/复习处理器。

- [ ] 先写 HTTP 固定日期测试：today/period/lifetime 的正确率、计时不重计、7/30/90范围、午夜、空数据、标签覆盖、streak/徽章、续做与两用户隔离，当前dashboard404应失败。
- [ ] 实现 `Dashboard(ctx,user,days)` 只读一致性事务，JSON 合同至少：timezone,start_date,end_date,days,profile,lifetime,period,previous,today,trend,modules,activity,streak,achievements,review。
- [ ] 计数组统一 `answered,correct,accuracy,duration_ms,sessions`，accuracy 为0–100数值/null；每日点加 date；模块点加 module/sufficient；热力图固定90日；streak加current,longest,practiced_today。
- [ ] `review` 含 unfinished（最多3）、weak_modules/weak_concepts（最多3，至少5次）、labeled_answered/label_coverage、wrong/favorites、recent wrong candidates；最新标签不使用陈旧缓存数。
- [ ] 成就实时计算首答、100/1000次、7/30天、六模块各5次，含id/name/current/target/unlocked。
- [ ] 优先错题创建最多20个可练有效题，按 wrong_count/last_wrong_at排序，返回原Session。无题明确400，不拷贝判分代码。
- [ ] CSV `days=7/30/90/all`，最多5000行；超限400先于发送文件；有BOM、转义与公式注入防护，只导出本人已提交答案和题号/模块/耗时。
- [ ] 运行固定 Clock 统计/安全/CSV/推荐与全量测试，测合成历史多请求，写报告并独立审查。

