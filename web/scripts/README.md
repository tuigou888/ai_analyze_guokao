# 本地 UI 回归

要求 Node 22+、Chrome/Chromium（可用 `CHROME_PATH` 指定）、运行中的 Vite 和本地 Go API。只对**隔离测试数据库**运行：测试会真实创建练习、订正错题、调整测试用户会员/封禁状态、重置测试密码、创建删除测试管理员、保存测试配置。

准备测试账户：一个学习账户、一个 super 和一个 operator。将随机测试凭据存于仓库外权限为 0600 的 JSON，字段为 `username`、`password`、`admin`、`operator`。三个初始测试账户使用同一临时密码。不要填生产凭据。

```sh
cd web
UI_QA_CREDENTIALS=/tmp/study-ui-qa/credentials.json npm run test:ui
```

默认使用 `http://127.0.0.1:5173`，可通过 `UI_QA_URL` 指定其他回环地址；拒绝远程站点。`UI_QA_OUTPUT` 可指定截图及 JSON 结果目录，默认 `../design-review/after`。脚本不输出凭据、Cookie 或密钥。连接测试清空隔离环境的地址/模型后仅验证本地失败反馈，不调用外部模型。

截图覆盖 1920、1440、1024、768、390、360、320px；保存 1440、390、320px PNG。失败时进程非零退出，结果 JSON 只包含已经完成的检查；必须以完整进程退出码为准。

重复运行须为测试身份预留足够每日额度（脚本保留真实服务端 429 规则）。可用 `UI_QA_ONLY=错题订正` 只跑匹配名称的流程，并用 `UI_QA_OUTPUT` 指定独立结果目录。
