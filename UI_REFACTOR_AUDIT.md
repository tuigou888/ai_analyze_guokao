# 行测研习 UI 改版审查

设计依据：`design-reference/行测研习-OpenCode-ClaudeCode-前端改版完整提示词.md`、完整 HTML 原型及 8 张参考截图。参考截图是目标示意，并非原站截图；改版前截图将从本地旧版本另行采集。

## 技术与数据边界

- Vue 3 Composition API、Vue Router、Vite、KaTeX；npm 锁文件。使用原生 CSS，已有 tokens / base / components / layout / pages / account 六层样式。
- App.vue 是公共壳层；Filters、QuestionRows、Pagination 已共享，需扩展，避免引入 UI 框架。
- api.js 集中处理真实同源 `/api` 请求、身份头、401 和账户变化；auth.js 管理响应式身份和资料。保留全部合同。
- 路由守卫区分管理员与学习账户；管理员身份独立，super 才能修改配置和管理员。普通用户隐藏管理入口，不把前端显示当作服务端鉴权。
- 题目筛选和练习数据来自 API；筛选草稿、页码和当前页签是前端状态。个人资料与答题页已有未保存离开保护，必须保留。
- 已有列表 loading / empty / error，多数错误缺少重试；错题移除和取消收藏缺少确认；长题摘要截断且缺少展开。
- Go 有 API、认证、权限、草稿并发、统计合同测试。前端原来只有 build，无测试脚本；添加轻量原生 Node / 浏览器回归，不增加运行时依赖。

## 页面映射

| 页面 / 路由 | 代码 | 原问题 → 新布局 | 业务边界 / 回归 |
| --- | --- | --- | --- |
| 公共外壳 | web/src/App.vue、styles/layout.css | 76px 顶栏、导航无分组、窄屏隐藏底部入口 → 64px 顶栏、224/64px 侧栏、手机抽屉 | 登出前保存、管理员和用户身份隔离 |
| 题库 /questions | views/QuestionList.vue、components/Filters.vue | 长筛选和重复标题 → 搜索+模块，高级筛选、已应用条件、专项入口 | 原查询语义、20条分页、10/20/50抽题、返回筛选 |
| 真题 /papers | views/Papers.vue | 高卡片、长地区枚举 → 紧凑三/二/一列、可搜索地区 | 真实维度映射、组卷题序、分页；无关联会话字段时不伪造继续 |
| 考点 /concepts、/concepts/:id | views/ConceptList.vue、ConceptDetail.vue | 平铺卡片 → 模块/子模块目录+内容双栏 | 真实题量、掌握度、详情/关联题、返回恢复 |
| 错题 /wrongbook | views/WrongBook.vue、QuestionRows.vue | 四按钮竖排 → 重练+收藏+更多，移除确认 | pending/resolved/all、订正/恢复、错误反馈、真实数量 |
| 收藏 /favorites | views/Favorites.vue、QuestionRows.vue | 摘要和操作层次不足 → 共用题目行，取消确认 | 独立收藏接口和日期、单页无禁用翻页 |
| 记录 /records | views/Records.vue | 第五统计卡落单 → 四指标+收藏快捷入口 | 累计口径、真实完成状态、续做与复盘 |
| 答题 /practice | views/Practice.vue | 阅读列、长答题卡和底部操作 → 760–880px 阅读列、桌面卡/手机抽屉、稳定操作条 | 自动保存、revision冲突、计时、提交确认、提交前无答案 |
| 个人 /account | views/Account.vue、components/account/* | 大圆环、资料宽、维护混入安全 → 双目标进度条、720px表单、维护分组 | 四页签、7/30/90日、资料保存与身份隔离、密码/会话 |
| 管理 /admin | views/Admin.vue、Settings.vue、admin/* | 学习导航混用、维护混入账户、创建表单四列 → 独立导航、数据维护、分段配置、两列表单 | super权限、留空保留密钥、原确认策略、查询/保存/测试连接 |

## 实施计划与进度

用户已明确授权按照提供的完整设计直接执行，不重复请求设计批准；当前功能分支 `ui/study-workspace-refactor`，保留用户的未跟踪参考包。

- [x] Phase A：tokens 兼容映射、公共壳层、筛选/组合框、菜单/确认、分页和题目行；构建及基础验证。
- [x] Phase B：题库、真题、考点、错题、收藏、记录、独立答题页；构建、API/浏览器回归。
- [x] Phase C：个人中心、后台、所有响应式断点；构建、Go 回归、真实本地 API 浏览器验证、截图与交付文档。

审查重点：筛选草稿与已生效条件区分；普通用户不显示管理入口；危险动作不误触；未保存资料和答题草稿不因路由丢失；模拟考试提交前不出现答案；320px 不整体横向溢出。

最终验证：三个阶段生产构建通过；Go 全量回归通过；真实 API 浏览器 19 流程、119 视口及确认移除专项通过。独立审查问题均已回修。详见 `UI_REFACTOR_TEST_REPORT.md` 与 `UI_REFACTOR_CHANGELOG.md`。

2026-11-19 用户追加要求：完成外部 LLM/SMTP 与试卷会话关联；随后停止浏览器测试并自行验收。已新增只读试卷进度接口（无迁移），保留旧查询与记录合同；真实外部连接和追加验证见 `UI_FOLLOWUP_VALIDATION.md`。
