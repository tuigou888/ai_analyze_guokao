# 全站前端公共样式体系实施计划

> **面向 AI 代理的工作者：** 使用 `executing-plans` 在当前隔离 worktree 中逐任务实施。步骤使用复选框（`- [ ]`）跟踪进度。不要添加或运行测试；按全局约束执行构建与静态审查。

**目标：** 将 Vue 前端的通用视觉标准集中到共享 tokens、基础样式和组件样式中，让页面样式只负责独有布局与数据表达。

**架构：** 把现有全局 CSS 拆成 tokens、base、components、layout、pages 五层；个人中心保留 feature stylesheet，但公共视觉属性引用共享层。设置页移除重复的通用控件、按钮、卡片和提示框规则。

**技术栈：** Vue 3、Vite、原生 CSS。

**规格：** [全站前端公共样式体系设计规格](../specs/2026-10-08-frontend-style-system-design.md)

## 全局约束

- 保留现有蓝白浅色视觉，不增加 UI 库、CSS 预处理器或构建依赖。
- 保留页面内容、路由、交互、API、ARIA 关系和用户可调的练习字号。
- 公共控件高度使用 46px，控件圆角使用 8px；页面规则不重新定义这些公共视觉属性。
- 共享断点使用 1180px、860px、580px；仅在图表内容有明确需要时保留并注明 feature 断点。
- 数据驱动的 Vue `:style` 值保留；其静态颜色、圆角、间距和字号使用共享变量。
- 不添加或运行测试。完成每个任务后检查构建和差异；最终运行 `npm run build` 与 `git diff --check`。

## 审查重点（Review Focus）

1. 样式导入顺序变化不能让公共 tokens 被旧规则覆盖；检查 `main.js` 顺序和生产构建结果。
2. 键盘焦点、禁用、只读和 `aria-invalid` 控件仍需有清楚状态；检查公共控件样式与已有表单语义。
3. 设置页 scoped CSS 不应继续覆盖全站按钮、卡片、提示框和控件；检索并复核剩余规则是否仅为布局。
4. 个人中心卡片、图表选择器、热力图及数据表仍需复用公共视觉，同时保留图表自身布局与滚动。
5. 375px 小屏下筛选、设置表单和个人中心不能产生整页横向溢出；环境可用时做视口检查，否则记录浏览器不可用。

---

### 任务 1：拆分全局样式层并迁移共享基础规则

**文件：**

- 创建：`web/src/styles/tokens.css`
- 创建：`web/src/styles/base.css`
- 创建：`web/src/styles/components.css`
- 创建：`web/src/styles/layout.css`
- 创建：`web/src/styles/pages.css`
- 修改：`web/src/main.js`
- 删除：`web/src/style.css`

- [x] **步骤 1：定义语义 tokens**

在 `tokens.css` 中定义一组来源唯一的语义变量：

```css
--color-page: #f2f5f9;
--color-surface: #ffffff;
--color-surface-subtle: #f4f7fb;
--color-border: #dce4ef;
--color-text: #20324b;
--color-text-muted: #58697f;
--color-primary: #215eac;
--color-primary-hover: #174e94;
--color-primary-soft: #e7f0fd;
--color-success: #167052;
--color-warning: #8b590d;
--color-danger: #b73543;
```

同时定义以下变量：

```css
--color-success-surface: #eaf7f0;
--color-success-border: #c1dfd0;
--color-warning-surface: #fff7e6;
--color-warning-border: #eed9ad;
--color-danger-surface: #fff0f2;
--color-danger-border: #ebc1c8;
--color-info: #395578;
--color-info-surface: #eaf1fb;
--color-info-border: #cbdcf0;
--font-body: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei', sans-serif;
--font-mono: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
--font-size-2xs: 10px;
--font-size-xs: 11px;
--font-size-sm: 12px;
--font-size-label: 13px;
--font-size-control: 14px;
--font-size-body: 15px;
--font-size-body-large: 16px;
--font-size-title-small: 18px;
--font-size-title: 22px;
--font-size-display: 30px;
--space-1: 4px;
--space-2: 8px;
--space-3: 12px;
--space-4: 16px;
--space-6: 24px;
--space-8: 32px;
--space-10: 40px;
--space-12: 48px;
--radius-small: 4px;
--radius-control: 8px;
--radius-panel: 12px;
--radius-round: 999px;
--control-height: 46px;
--border-width: 1px;
--focus-ring: 0 0 0 3px rgba(33,94,172,.16);
--transition-fast: .16s ease;
```

`--font-size-2xs` 到 `--font-size-display` 作为基线角色；图表刻度、头像字形和真实数据展示允许保留组件专属字号。间距变量名中的编号对应 4px 倍数，24/32/40/48 分别使用 `--space-6/8/10/12`。为迁移旧规则，临时保留 `--bg`、`--panel`、`--panel-2`、`--line`、`--text`、`--muted`、`--accent`、`--accent-soft`、`--ok`、`--warn`、`--err`、`--mono`、`--radius` 等别名，并让它们引用新的语义 tokens；任务 3 逐一替换使用点并删除别名。

- [x] **步骤 2：迁移 reset 与基础排版**

把 `style.css` 的 `box-sizing`、body、正文/标题、链接、通用 focus、跳转链接和 reduced-motion 规则迁入 `base.css`，颜色、字体和字号改用 `tokens.css`。

- [x] **步骤 3：迁移共享组件视觉**

把通用按钮及 primary/text/ghost 状态、表单控件、字段标签/提示/错误状态、卡片表面、notice 状态、tag、tabs、pagination、code 和数据表规则迁入 `components.css`。输入控件沿用已有 46px 高度、8px 圆角、统一焦点环和状态规则。`.panel` 保留其内容结构，不要求内部列表增加 padding。

- [x] **步骤 4：迁移 shell 与页面布局**

把 `.app-shell`、sidebar、topbar、workspace、main/footer 等跨页外壳规则移入 `layout.css`；把题库、练习、试卷、考点、记录、登录和管理区域的 grid/flex、内容宽度及响应式布局移入 `pages.css`。只迁移规则，不改变页面 DOM 和行为。

- [x] **步骤 5：固定 CSS 导入顺序并清理旧入口**

在 `main.js` 依次导入 `tokens.css`、`base.css`、`components.css`、`layout.css`、`pages.css`，删除旧 `style.css` 导入及文件。此时个人中心旧样式仍由 `App.vue` 加载。

- [x] **步骤 6：检查迁移结果**

运行 `npm run build`，预期 Vite 生产构建以退出码 0 完成；运行 `git diff --check`，预期无输出且退出码为 0。检查页面布局类在 `pages.css` 均有对应规则。

- [x] **步骤 7：提交本任务**

提交信息：`style: extract shared frontend css layers`。只 stage 本任务涉及的文件。

### 任务 2：统一设置页和个人中心公共组件样式

**文件：**

- 创建：`web/src/styles/account.css`
- 修改：`web/src/main.js`
- 修改：`web/src/App.vue`
- 修改：`web/src/views/Settings.vue`
- 修改：`web/src/components/account/ProfileForm.vue`
- 修改：`web/src/components/account/Overview.vue`
- 修改：`web/src/components/account/charts/TrendChart.vue`
- 修改：`web/src/components/account/charts/ModuleChart.vue`
- 修改：`web/src/components/account/charts/ActivityCalendar.vue`
- 删除：`web/src/components/account/account.css`（迁移完成后删除旧路径）

- [x] **步骤 1：迁移个人中心 feature 样式**

把旧 `components/account/account.css` 格式化迁入 `styles/account.css`，保留 `.pc-*` 图表、热力图和个人中心布局规则。将 `.pc-card`、`.pc-tag` 的背景、边框、文字、圆角等共享外观改为 `components.css` 中的共用规则；个人中心文件只保留 margin/padding 变体、布局和图表专属视觉。

- [x] **步骤 2：统一导入个人中心样式**

在 `main.js` 的 `pages.css` 后导入 `account.css`；移除 `App.vue` 对旧 account.css 的导入，删除旧文件，确保公共层先于 feature 层加载。

- [x] **步骤 3：清理设置页 scoped 样式**

从 `Settings.vue` scoped style 移除 `button`、`button.primary`、`.card`、`.notice`、控件、`h2/h3`、`.small`、`.muted` 和通用 `table/td/code` 外观规则。保留并改名为 `.settings-grid`、`.settings-row`、`.settings-actions` 等只负责布局的规则；设置页卡片、notice、ghost 按钮和表格使用共享组件类。设置帮助文字使用共享字段提示样式。

- [x] **步骤 4：让个人中心表格复用公共数据表**

在 Overview、TrendChart、ModuleChart、ActivityCalendar 的表格增加 `.data-table` 与需要的紧凑变体；`account.css` 只保留滚动容器、最大高度和 sticky header 的布局规则。Settings 的成本参考表使用同一 `.data-table` 基础类，首列宽度可保留为 `.settings-reference-table` 的布局规则。

- [x] **步骤 5：检查通用样式覆盖**

运行 `rg -n '(^|[}[:space:]])(button|input|select|textarea|\.notice|\.card)[[:space:]]*\{' web/src --glob '*.{css,vue}'`，确认设置页与 account feature stylesheet 没有独立公共外观定义；对命中项逐条区分组件规则与纯布局规则。运行 `npm run build`，预期构建退出码为 0。

- [x] **步骤 6：提交本任务**

提交信息：`style: use shared components in settings and account`。只 stage本任务涉及的文件。

### 任务 3：映射页面视觉值到 tokens 并完成全站审计

**文件：**

- 修改：`web/src/styles/tokens.css`
- 修改：`web/src/styles/base.css`
- 修改：`web/src/styles/components.css`
- 修改：`web/src/styles/layout.css`
- 修改：`web/src/styles/pages.css`
- 修改：`web/src/styles/account.css`
- 按需修改：`web/src/views/Settings.vue`

- [x] **步骤 1：迁移重复颜色、字号、间距与圆角**

对全站 CSS 做逐项语义映射：重复品牌/状态颜色使用语义颜色变量；重复字号使用 typography 角色；重复组件/区域间距使用 spacing tokens；重复圆角使用 radius tokens。图表系列、热力图等级和头像色使用有名称的语义变量。保留 grid 轨道、图表坐标、SVG 宽高、头像尺寸等必须独立的几何值。

- [x] **步骤 2：统一断点**

把跨页断点收敛至 1180px、860px、580px。仅在个人中心图表列因实际内容宽度必须提前折叠时保留 1100px，并在 `account.css` 注释说明；不保留无内容理由的 600px 整页断点。

- [x] **步骤 3：移除迁移别名并复核层叠**

删除 `tokens.css` 中的旧变量别名。按 `main.js` 导入顺序复核公共组件属性没有被 feature 规则覆盖；页面级样式只能设置布局、尺寸约束、数据视觉和公共组件的明确变体。

- [x] **步骤 4：完成静态覆盖审计**

检索 `web/src` 中的颜色字面量、`font-size`、`border-radius`、间距属性和 `<style>` 块。确认剩余字面量仅是专属几何值、头像/图表数据语义值或已注明的例外；确认动态 `:style` 仅处理学习进度、图表数据和用户阅读偏好。

- [x] **步骤 5：最终验证**

运行 `npm run build`，预期 Vite 构建退出码为 0；运行 `git diff --check`，预期无输出且退出码为 0。浏览器可用时在桌面与 375px 视口查看登录、题库筛选、设置和个人中心；若浏览器不可用，记录具体限制，不宣称完成截图验收。

- [x] **步骤 6：提交最终整理**

提交信息：`style: standardize frontend visual tokens`。只 stage 本计划涉及的源码文件；不包含构建产物。
