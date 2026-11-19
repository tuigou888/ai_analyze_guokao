# Docker 部署权限预检

适用于本仓库的 Ubuntu/Linux 宿主机和 Docker Compose。镜像固定使用 UID/GID `10001:10001`，数据通过 `./var:/opt/gk/var` 挂载。镜像内的 chown 不会修改宿主机文件；恢复备份或复制数据后，需要重新确认宿主机属主。

## 统一入口

在项目目录运行：

```bash
sudo sh deploy/docker-run.sh start
sudo sh deploy/docker-run.sh restart
sudo sh deploy/docker-run.sh rebuild
```

三个启动流程都会：

1. 校验 Compose 配置，不打印包含密钥的展开配置。
2. 为宿主机数据目录准备 `10001:10001` 权限。
3. 使用目标镜像中的实际用户运行一次权限预检，且不映射服务端口、不启动 Go 应用。
4. 预检通过后启动 gk，并等待健康检查。

`restart` 和 `rebuild` 先完成预检，再停止原 gk。`rebuild` 还会先完成镜像构建。构建、修权或预检失败时，脚本非零退出，不会继续停止或启动服务。只操作 gk，不使用 compose down，也不停止项目中的其他服务。

预检通过不能保证所有启动条件成立；数据库完整性、密钥是否与加密配置匹配、管理员是否存在等仍由 Go 服务校验，最后由 `--wait` 检查健康状态。

## 单独准备和检查

```bash
# 创建必要目录，修正宿主机权限；不创建数据库或密钥
sudo sh deploy/docker-run.sh prepare

# 仅检查当前实际容器用户及数据访问权限，不修改宿主机文件权限
sudo sh deploy/docker-run.sh check
```

宿主机已有 Docker 访问权限时，非 root 操作员也可执行脚本，权限准备阶段会调用 sudo。直接使用 sudo 运行完整脚本更适合当前宝塔部署环境。脚本可从任意工作目录调用，数据位置始终相对于脚本所在项目。

## 修正范围

| 路径 | 属主 | 权限/处理 |
| --- | --- | --- |
| `var` | 10001:10001 | 750 |
| `var/db` 及子目录 | 10001:10001 | 700 |
| `var/db` 中的普通文件，包括 SQLite/WAL/SHM | 10001:10001 | 600 |
| 已有 `var/secret.key` | 10001:10001 | 600；内容不变 |
| `var/logs`、`var/backups`、`var/runs` 顶层目录 | 10001:10001 | 700；不递归改写已有备份或日志内容 |

不会递归修改题库图片、旧发布包、代码或 `.env`。涉及数据目录、密钥的符号链接，以及数据库目录/密钥的硬链接，会被拒绝；请确认实际路径并使用独立文件后再执行，避免 root 修权作用到项目外。

权限操作会改变属主、模式和元数据时间，不修改数据库、草稿、用户、密钥或配置内容。脚本不自动创建备份，升级前仍需按原部署手册完成数据库与有效密钥的一致性备份。

## 启动保护与密钥模式

- 数据库必须是非空普通文件；缺失时拒绝启动，避免路径错误或误删后产生空站。
- 数据库及其目录必须可读写，已有 WAL/SHM 也必须可读写。
- 目标镜像的运行 UID 和 GID 必须均为 10001；不匹配时拒绝启动。
- 文件密钥模式下，密钥必须是非空、可读的普通文件；缺失时不会生成替代密钥。
- 原有 `GK_SECRET_KEY` 环境密钥模式继续支持，按 Compose 实际解析的容器环境判断，不打印其值；纯空白（包含 Go 会裁剪的 Unicode 空白）不视为有效密钥。
- 预检不打开 SQLite、不执行迁移、不调用配置写入，不生成新密钥。

通过 shell 环境注入 `GK_SECRET_KEY` 且需要 sudo 时，应按 sudo 策略保留该变量，例如 `sudo --preserve-env=GK_SECRET_KEY sh deploy/docker-run.sh start`；或者由权限受限的 `.env` 提供。脚本不主动解析或输出密钥。

直接运行 `docker compose up` 会绕过宿主机准备和预检。日常更新统一使用上述脚本。

## 首次安装

`start` 是已有数据的启动入口。首次安装先准备目录，并按原手册准备题库和管理员：

```bash
sudo sh deploy/docker-run.sh prepare
sudo docker compose build gk
sudo docker compose run --rm gk admin create \
  --db /opt/gk/var/db/gk.sqlite --user admin
```

只有确认是首次初始化、数据库没有需要保留的加密配置时，才显式初始化密钥：

```bash
sudo docker compose run --rm --no-deps gk config list >/dev/null
sudo sh deploy/docker-run.sh start
```

已有正式数据库缺少密钥时，应恢复与数据库配对的原密钥，或者提供原有效环境密钥。Go 本身也会拒绝为已含加密配置的数据库生成替代密钥。

## 当前宝塔服务器的使用方式

代码更新后，在服务器执行：

```bash
cd /www/wwwroot/gk.951357.xyz/ai_analyze_guokao
git pull --ff-only
# 先按部署手册完成一致性备份
sudo sh deploy/docker-run.sh rebuild
sudo sh deploy/docker-run.sh status
curl -fsS http://127.0.0.1:12001/healthz
```

保留原 `.env` 中的 `DOMAIN=gk.951357.xyz`、`GK_HOST_PORT=12001` 和宝塔反代 `127.0.0.1:12001`。

查看本次启动后的日志，避免把历史报错误认为复发：

```bash
gk_started_at=$(sudo docker inspect --format '{{.State.StartedAt}}' gk)
sudo docker logs --since "$gk_started_at" gk
```

## 回归测试

```bash
make test-deploy
# 等价于 python3 test-artifacts/docker_run_regression.py
```

测试使用临时目录、合成文件和记录命令的 Docker/sudo/属主操作替身；预检 shell 实际读取临时文件，检查真实文件模式和内容哈希。覆盖正常启动、构建/权限/预检失败不停止原服务、缺失库/密钥、环境密钥、空白密钥、链接拒绝、UID 不符、只读检查及非 root 调用。不需要生产数据或 Docker daemon，也不会运行浏览器测试。
