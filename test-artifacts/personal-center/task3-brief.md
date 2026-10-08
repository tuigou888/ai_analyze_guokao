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

## Task 3：个人中心页面与可视化

**文件：** 替换 `web/src/views/Account.vue` 为页面组合；新增 `web/src/components/account/` 下 Overview/Review/ProfileForm/Security 和 charts 独立组件；修改 api/auth/router/App/QuestionList/Practice、增加独立 account CSS。仅修改本任务前端文件及前端验收脚本。

- [ ] 先写真实浏览器验收脚本（临时服务），当前页面不存在总览和页签/图表，应失败；不得用假图表数据冒充功能。
- [ ] API 客户端统一 Profile/Session/Dashboard 真实 DTO，导出用同源 Cookie、检查失败，不在URL放凭据；复用已有401处理。
- [ ] `/account` 四本地页签：学习总览、复习助手、资料与偏好、账户安全；顶部/侧栏入口。保持原改密标签和统计重算功能可访问。
- [ ] 四类SVG/CSS图形：双趋势、六模块画像、90日热力图、目标环；有键盘/鼠标具体值、文字摘要和数据表；0样本为空而非0分。
- [ ] 资料/目标/偏好表单、8头像、日期、字段错误、保存版本、409输入保留与确认重载；离开/切页签保护；保存中防重复。
- [ ] 成就、倒计时、续做、优先错题、薄弱模块/考点、收藏与记录入口接入有效练习；无数据/不足样本有说明。
- [ ] 安全页会话分页/撤销/退出其他确认，当前会话保留；改密原逻辑与重算统计保持兼容。
- [ ] 保存后同步顶部昵称/头像；偏好缓存按用户ID隔离，退出清理；显式题库URL筛选优先，练习字号按保存值应用。
- [ ] 375px/桌面/键盘/无障碍/空状态/失败重试/范围切换/多标签冲突/用户切换回归与生产构建；写报告，独立审查。

