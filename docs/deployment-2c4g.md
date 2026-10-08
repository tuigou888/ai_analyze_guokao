# 本地运行与腾讯云宝塔部署手册

本手册适用于本地开发，以及把行测研习部署到 **腾讯云轻量应用服务器 2 核 4GB**。服务器使用宝塔面板管理 Nginx 与证书，Go 网站进程由 systemd 管理。

网站部署流程：本地准备代码和数据库 → 构建 Linux 部署包 → 腾讯云开放所需端口 → 宝塔安装 Nginx → 上传题库、密钥和图片 → 启动 Go 服务 → 宝塔建立反向代理与 HTTPS → 浏览器验收。

安装网站和日常运行不需要启动蒸馏，也不会调用模型 API。完整 API 见 [api-design.md](api-design.md)。本项目当前采用单机 SQLite；该架构不提供多实例横向扩容。

## 1. 运行结构与资源规划

```text
浏览器 --HTTPS--> 宝塔管理的 Nginx --HTTP 回环--> Go 网站（127.0.0.1:8080）
                                                    ├─ SQLite 数据库
                                                    └─ /opt/gk/data/90-图片
```

Go 二进制内嵌 Vue 前端、路由、KaTeX 和字体。2 核 4GB 机器上只安装宝塔面板与 Nginx；不要为了这个网站再安装 PHP、MySQL、Node.js、Redis 或 OCR 模型。项目使用 SQLite，题库和练习记录随库文件保存。

| 项目 | 规模/约束 | 准备方式 |
|---|---:|---|
| Linux amd64 网站包 | 当前约 12 MB | 本地 `make release` |
| 完整 SQLite 数据库 | 当前开发样本约 152 MB | `gk backup` 一致性快照 |
| `90-图片` 原图 | 当前样本约 367 MB | 单独打包上传 |
| Go 服务 | `GOMAXPROCS=2`、`GOMEMLIMIT=768MiB` | 见 `deploy/gk.service` |
| 网站服务限制 | `MemoryHigh=900M`、`MemoryMax=1200M`、`CPUQuota=150%` | 留内存给 Ubuntu、宝塔和 Nginx |
| 定时备份 | 最多 512 MB、CPU 50% | 见 `deploy/gk-backup.*` |

磁盘内容随数据库、图片和备份次数增长，实际套餐的磁盘容量以腾讯云控制台为准。建议给数据迁移、首次 FTS5 索引、数据库快照和日志留出至少数 GB 空间。当前开发机的数据大小只是参考，不是所有用户数据的固定上限。

## 2. 本地电脑准备环境

源码构建需要：

- Git
- Go **1.26 或更新的兼容版本**（见根目录 `go.mod`）
- Node.js **受维护的 LTS 版本**与 npm；当前建议 Node.js 24 LTS
- Bash、Make。Linux / macOS 可使用系统终端；Windows 建议安装 WSL2 + Ubuntu 后按 Linux 命令操作

Go 安装包见 [Go 官方下载页](https://go.dev/dl/)。Node.js 版本状态见 [Node.js 官方发行周期](https://nodejs.org/en/about/previous-releases)。本地用 `node --version`、`npm --version`、`go version` 检查工具是否已安装。

生产服务器只运行构建好的 Go 程序，不需要安装上述编译工具或 Node.js。

## 3. 本地首次启动

先取得项目源码，并进入项目根目录。项目题库位于单独的 `data/` 目录，可从既有本地副本迁移；全新环境可取得源数据：

```bash
git clone https://github.com/ERRRC/xingcezhenti.git data
```

完整数据集约 816 MB，拉取与建库需要网络和足够磁盘空间。首次建库执行：

```bash
make web
make reingest
make admin-init
make build
```

这四条命令依次构建前端、创建 SQLite 数据库、创建管理员并重新编译 Go 程序；源码中的 `.gitkeep` 也允许独立编译后端 CLI，网站交付仍必须先构建前端。然后启动服务：

```bash
./var/gk serve --db var/db/gk.sqlite --data data --addr 127.0.0.1:8081
```

打开用户入口 <http://127.0.0.1:8081/login>，首次注册普通用户。管理员入口是 <http://127.0.0.1:8081/admin/login>。`make admin-init` 和 `gk admin create` 会在终端交互输入口令，不要把密码写在命令参数里。

> `make reingest` 带 `--reset`，现在拒绝覆盖已有数据库及 WAL/SHM。重新建题库请指定新路径，例如 `make reingest DB=var/db/gk-new.sqlite`；新题库不含原账户、练习、错题或标注，不能直接替换线上库。程序升级应备份后启动新程序，无需重建。

从未经 OCR 的原始数据首次建库时，题目、选项和答案可用；`explanation_with_formula` 等 OCR 回填字段不会凭空生成。若本机已有完成 P2 的数据库，使用下文的快照步骤迁移数据库以保留 OCR 结果。

## 4. 本地前端热更新

准备好题库、图片、密钥和管理员后，可以分开运行 API 和 Vite。先构建前端依赖与 embed 目录，然后启动 API。

终端一（项目根目录）：

```bash
make serve
```

终端二：

```bash
cd web
npm ci
npm run dev
```

在浏览器打开 <http://127.0.0.1:5173>。Vite 把 `/api` 请求转发到本地 Go 服务 `127.0.0.1:8081`；前端源码保存在 `web/src/`。需要验证嵌入式生产产物时运行 `make web`，再运行 `make build`，并直接访问 Go 服务的 8081 端口。

后端默认绑定回环地址，仅供本机访问。局域网设备不需要访问时，保持 `127.0.0.1`，不要改成 `0.0.0.0`。

本地端口为 8081，生产 systemd/Nginx 配置端口为 8080，请勿混用。

## 5. 生成部署文件与数据库快照

所有构建和数据打包都在本地电脑完成。先构建程序：

```bash
make release
```

输出：

```text
var/gk-linux-amd64.tar.gz
```

压缩包包含 Go 二进制、systemd 模板、可选的 Caddy 示例、taxonomy 与部署文档。宝塔按本手册用自己的 Nginx 反向代理，不需要运行包内的 Caddy 示例。压缩包**不包含**题库、图片和密钥。

### 5.1 使用已有题库（推荐）

在原开发机使用本机程序生成并验证完整备份（Linux/macOS 本机编译，不运行 Linux 发布二进制）：

```bash
go build -buildvcs=false -o var/gk ./cmd/gk
./var/gk backup --db var/db/gk.sqlite --bundle var/deploy-backup
./var/gk backup --verify-bundle var/deploy-backup
```

在 Windows PowerShell 中如需从本机数据库制作快照，可构建 Windows 可执行文件：

```powershell
cd web
npm ci
npm run build
cd ..
go build -buildvcs=false -o var/gk.exe ./cmd/gk
.\var\gk.exe backup --db var/db/gk.sqlite --bundle var/deploy-backup
.\var\gk.exe backup --verify-bundle var/deploy-backup
```

Windows 使用生成后的 `var/deploy-backup/gk.sqlite`、`secret.key` 和 `complete.json`，通过 Windows OpenSSH 的 `scp` 或 WinSCP 私下传输；不要直接复制仍在使用的数据库。macOS 使用本机编译的程序；Windows 路线本轮仅完成交叉编译校验，原生运行仍需在 Windows 验收。Windows 上请另用 NTFS 权限限制备份目录访问，POSIX 的 0600 不是 Windows ACL。

快照保留数据库里的用户、管理员、题目、P3 标注、设置和 OCR 回填内容，也包含 SQLite WAL 中已提交的数据。它以 0600 权限新建目标文件，不允许覆盖已存在的文件。在线库不要用文件管理器直接复制 `.sqlite` 代替快照；SQLite 对 `VACUUM INTO` 的说明见[官方文档](https://www.sqlite.org/lang_vacuum.html)。

### 5.2 打包题目图片与密钥

```bash
tar -czf var/gk-images.tar.gz -C data 90-图片
```

最终图片目录应为 `data/90-图片/题目图/` 和 `data/90-图片/公式图/`。网站部署只需图片目录，无需把几百 MB 的 Markdown 源笔记上传服务器。

使用完整备份里的 `var/deploy-backup/secret.key`，它是与快照匹配的有效密钥。`GK_SECRET_KEY` 模式会导出规范化的有效密钥，无需将原口令复制进命令行或聊天；备份进程必须收到原有效环境变量。缺失/错误密钥会中止并清理本次目录。图片与批次文件仍需另行备份。

不要把 `var/secret.key` 放进 Git、`var/gk-linux-amd64.tar.gz`、Web 根目录或聊天记录。通过 SSH/SCP 私下传输。

### 5.3 将文件上传到服务器

在本地执行；以下假设服务器 SSH 账号为 `ubuntu`，端口为 22：

```bash
scp var/gk-linux-amd64.tar.gz ubuntu@SERVER_IP:/tmp/
scp var/deploy-backup/gk.sqlite ubuntu@SERVER_IP:/tmp/deploy-gk.sqlite
scp var/gk-images.tar.gz ubuntu@SERVER_IP:/tmp/
scp var/deploy-backup/secret.key ubuntu@SERVER_IP:/tmp/gk-secret.key
```

将 `SERVER_IP` 替换为轻量服务器公网 IP。用户名以腾讯云实例连接页为准，Ubuntu 镜像通常使用 `ubuntu`。Windows 可用 Windows OpenSSH 的 `scp` 或 WinSCP；SSH 非 22 端口使用 `scp -P 端口` 和 `ssh -p 端口`。

SSH 上传落在 `/tmp` 后只在服务器本地安装权限受限的副本，传输完成后删除临时明文文件。

## 6. 腾讯云轻量服务器与防火墙

### 6.1 选择/确认实例

在腾讯云轻量应用服务器控制台确认实例为 Linux、2 核 4GB，并记录公网 IP、系统版本、SSH 用户、SSH 端口和可用磁盘。建议使用全新 Ubuntu LTS 系统；已有运行环境或面板的服务器不要直接运行一键安装脚本，先核对它是否会覆盖 Nginx、数据库或已有站点。

**重装系统会清除原盘数据。** 仅对新建实例或已有外部备份且明确要重装的实例操作。

如果服务器位于中国内地地域且用域名对外提供网站，腾讯云要求先完成 ICP 备案并取得备案号，再开通域名访问；若尚未备案，先按[轻量应用服务器 ICP 备案说明](https://cloud.tencent.com/document/product/1207/45756)办理。香港及境外地域的备案条件不同，以腾讯云对应规则为准。

### 6.2 配置腾讯云实例防火墙

腾讯云轻量应用服务器的防火墙规则控制公网入站流量。Linux 镜像通常默认开放 SSH 22 与 HTTP 80；需要根据站点配置增加 HTTPS 443。面板端口必须以宝塔安装结束后显示的实际端口为准。[腾讯云防火墙文档](https://cloud.tencent.com/document/product/1207/44577) 也建议按需要设置来源地址。

| 端口 | 用途 | 来源 |
|---|---|---|
| SSH 端口，默认 22/TCP | SSH 登录及上传 | 只允许自己的固定公网 IP（如环境允许） |
| 80/TCP | HTTP 与证书验证 | `0.0.0.0/0`；启用公网 IPv6 时按需设置 IPv6 |
| 443/TCP | HTTPS 网站 | `0.0.0.0/0`；启用公网 IPv6 时按需设置 IPv6 |
| 宝塔安装输出的端口 | 面板登录 | 仅自己的固定公网 IP；登录验收后可继续收紧 |
| 8080/TCP | Go 网站回环端口 | **不要在腾讯云防火墙开放** |

腾讯云控制台防火墙与宝塔内的操作系统防火墙是两处不同规则。先在腾讯云开放 SSH 并从本地验证 SSH 登录，再安装/配置宝塔防火墙；不要在启用操作系统防火墙后才想起放通 SSH。

## 7. 在干净 Ubuntu 系统上安装宝塔

宝塔官方安装指引要求在没有安装 Web 环境的干净 Linux 上安装，并在访问前放通安装程序实际给出的面板端口。按[宝塔官方快速安装文档](https://docs.bt.cn/getting-started/quick-installation-of-bt-panel/) 获取适合当前支持发行版的安装命令。

在 SSH 登录后的服务器终端，以 root 身份或带 `sudo` 执行官方安装命令。官方文档当前提供的正式版安装方式如下；安装脚本内容可能调整，执行前请再次核对宝塔官方页面：

```bash
if [ -f /usr/bin/curl ]; then
  curl -sSO https://download.bt.cn/install/install_panel.sh
else
  wget -O install_panel.sh https://download.bt.cn/install/install_panel.sh
fi
bash install_panel.sh docscenter
```

阅读安装器提示后确认将面板安装到 `/www`。完成后在终端保存面板地址、随机入口、管理员账号和密码；不要将这些信息发到公共位置。若访问失败，先在腾讯云防火墙中把安装器报告的**实际面板端口**仅开放给自己的公网 IP，再登录。

宝塔安全设置建议：修改默认面板密码；保留随机面板入口；打开面板 IP 访问限制（有固定管理 IP 时）；面板端口不要对全网开放。面板端口不是网站的 80/443，也不是 Go 服务的 8080。宝塔面板端口的查看和修改方法见[官方端口说明](https://docs.bt.cn/getting-started/edit-panel-port/)。

### 7.1 在宝塔安装 Nginx

登录宝塔后进入“软件商店”或首次安装向导，仅安装 Nginx。选择能在 2 核 4GB 机器上稳定运行的 Nginx 版本即可。

不要为这个项目安装 MySQL、Apache、PHP、FTP、phpMyAdmin、Java 环境或 Redis；项目在 SQLite 中保存数据。宝塔面板与 Nginx 自身也会占用内存，因此不在服务器上编译 Go、安装 `web/node_modules` 或运行 Vite。部署包带有可选的 `deploy/Caddyfile` 示例；宝塔路径仅使用面板管理的 Nginx，不要同时启动 Caddy。

如果宝塔系统防火墙插件启用，再确认其中允许 SSH、80、443 和仅限自己访问的面板端口。不要添加公网 8080 规则。

## 8. 在服务器安装 Go 程序与数据

以下命令均在服务器执行，假设文件已上传到 `/tmp`，系统为 Ubuntu/Debian。首装步骤只用于新环境；已有数据时不要覆盖数据库和密钥。

### 8.1 创建服务账号和目录

```bash
sudo useradd --system --home-dir /opt/gk --shell /usr/sbin/nologin gk
sudo install -d -o root -g root -m 755 /opt/gk
sudo install -d -o gk -g gk -m 700 /opt/gk/var
sudo install -d -o gk -g gk -m 700 /opt/gk/var/db /opt/gk/var/runs /opt/gk/var/logs /opt/gk/var/backups
sudo install -d -o root -g root -m 755 /opt/gk/data
```

如果显示用户 `gk` 已存在，先确认它确实是本项目的服务用户，不要重复创建。解压本地构建包并安装图片：

```bash
sudo tar -xzf /tmp/gk-linux-amd64.tar.gz -C /opt/gk
sudo tar -xzf /tmp/gk-images.tar.gz -C /opt/gk/data
sudo chmod 755 /opt/gk/gk
sudo chown -R root:gk /opt/gk/data
sudo find /opt/gk/data -type d -exec chmod 755 {} +
sudo find /opt/gk/data -type f -exec chmod 644 {} +
```

这会得到 `/opt/gk/data/90-图片/题目图/` 和 `/opt/gk/data/90-图片/公式图/`。图片归服务组读取，网站通过需登录的 Go 媒体路由提供图片；不把图片目录映射到 Nginx 的公开静态根目录。

安装数据库和密钥副本：

```bash
sudo install -o gk -g gk -m 600 /tmp/deploy-gk.sqlite /opt/gk/var/db/gk.sqlite
sudo install -o gk -g gk -m 600 /tmp/gk-secret.key /opt/gk/var/secret.key
```

首次正式部署应从包含题库内容的快照开始。若线上数据库已存在，先用线上一致性备份，并把经过验证的数据库文件安装到该位置；不要上传空数据库覆盖线上数据。首次启动会自动执行到 v12 的追加数据库迁移、创建中文搜索索引并刷新已有考点映射，这一步可能比以后启动慢。

### 8.2 确认管理员账号

如果数据库快照已包含管理员，直接使用其已知账号登录。首次创建管理员（交互输入密码）：

```bash
cd /opt/gk
sudo -u gk ./gk admin create --db /opt/gk/var/db/gk.sqlite --user admin
```

如管理员已经存在但密码遗失，可在服务器用以下命令重设；输入的新密码至少 8 位，并会注销旧管理员会话：

```bash
sudo -u gk ./gk admin passwd --db /opt/gk/var/db/gk.sqlite --user admin
```

服务启动前检查 `gk` 用户可写 `var/`，可读 `/opt/gk/gk`、`taxonomy/` 和 `data/90-图片/`。不要将整个 `/opt/gk` 改成服务用户可写；应用只需要写 `/opt/gk/var`。

## 9. 启动 Go 服务

包内已提供 systemd 服务文件。复制后启用：

```bash
sudo cp /opt/gk/deploy/gk.service /etc/systemd/system/gk.service
sudo systemctl daemon-reload
sudo systemctl enable --now gk
sudo systemctl status gk --no-pager
```

确认网站只监听本机回环地址且健康端点正常：

```bash
sudo ss -lntp | grep ':8080'
curl --fail http://127.0.0.1:8080/healthz
```

预期健康结果：`{"ok":true}`，监听应为 `127.0.0.1:8080`。宝塔 Nginx 在本机连接该地址。若无法启动：

```bash
sudo journalctl -u gk -n 100 --no-pager
```

服务在没有管理员账号时会主动退出；日志中 v7 migration 错误通常说明磁盘空间、文件权限或源数据库版本有问题。修复前先保留源数据库快照。

## 10. 宝塔建站、反向代理和 HTTPS

### 10.1 域名准备

有备案要求的内地服务器须先完成 ICP 备案。把站点域名的 A 记录解析到腾讯云公网 IP。不要配置错误的 AAAA 记录；若不使用 IPv6，请不要把域名指向不可达的 IPv6 地址。等待 DNS 解析生效后再申请 HTTP 文件验证证书。

### 10.2 在宝塔创建站点

在宝塔进入“网站”→“添加站点”，填入域名和网站根目录，例如 `/www/wwwroot/practice.example.com`。此站点由 Nginx 反代 Go 提供页面，不需要 PHP 项目环境和网站数据库。将站点 PHP 设置为“纯静态”或选择不启用 PHP；不要初始化 MySQL 数据库。

先不要开启强制 HTTPS 或其他 301 跳转。进入该站点“SSL”页，选择 Let's Encrypt 证书，选择已解析的域名和文件验证，申请并部署证书。域名、80 端口和证书验证路径就绪后，启用强制 HTTPS。宝塔的 SSL 菜单/验证方式可能随面板版本变化，按[官方证书流程](https://docs.bt.cn/user-guide/site/php/site-config/ssl)操作；证书申请前若已配置其他反代或跳转，可能导致验证失败。

### 10.3 配置反向代理

在站点设置的“反向代理”页新增代理：

| 字段 | 设置 |
|---|---|
| 代理目录 | `/` |
| 目标 URL | `http://127.0.0.1:8080` |
| 发送域名 / Host | `$host` 或面板提供的“发送域名”默认值 |
| 缓存 | 先关闭 |
| WebSocket | 无需启用 |

宝塔文档中的反向代理流程见[官方配置指南](https://docs.bt.cn/user-guide/site/php/site-config/reverse-proxy/)。保存后确认 Nginx 配置通过面板语法检查，并从服务器访问 Go 健康端点。

**确认 Nginx 传递来源信息。** 登录 Cookie 始终带 Secure。完整同源校验只接受可信代理的 HTTPS 来源头；也可用 `--public-origin https://你的域名` 固定外部 Origin。打开代理的 `location /` 配置块确认以下指令：

```nginx
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Forwarded-For $remote_addr;
proxy_set_header Host $http_host;
```

Go 的 `--trusted-proxies` 默认不信任任何转发头。提供的 systemd 服务仅信任本机回环代理；不要配置公网宽泛网段。Nginx 应覆盖客户端传入的 X-Forwarded-For（如上），不能直接透传。Go 同时按来源地址和用户名限制认证请求。

若宝塔自动生成的反代配置已经包含这条，不要重复添加；若未包含，把它添加到已经存在的 `/` 代理配置块中，而不是另建一个相同的 `location /`。保存后使用宝塔的“配置修改/保存”按钮并确认 Nginx 重载成功。也可以用 HTTPS 登录后查看响应头，确认 `Set-Cookie` 含 `Secure`。

反代完成后访问 `https://你的域名/healthz` 应返回 `{"ok":true}`。关闭公开的 8080 规则；网站入口只留 HTTPS 443，HTTP 80 可保留作 HTTPS 跳转和证书续期。

> 经宝塔反代的登录请求在当前版本会从 Go 看起来像同一台 Nginx。服务的登录限流按 TCP 来源计算，所以所有经该代理的登录/注册请求共用每分钟 30 次的限制。此配置针对当前单实例和低流量演示场景；若出现共享限流，应先调整服务端的可信代理处理，再变更限流方式。

## 11. 首次线上验收

确认服务器健康后，在公网 HTTPS 域名执行以下检查：

1. 打开 `/login` 注册普通用户并登录。
2. `/questions` 可以筛选模块、年份、地区和考试类型，中文关键词能搜索题干。
3. 开始专项练习，答题后刷新；登录后能恢复草稿。提交后看到判分和解析。
4. 验证多选、判断题和陕西 A–H 选项题；无答案题不可练习。
5. 答错题后查看 `/wrongbook`；重做正确后题目标记为已订正。
6. 检查 `/papers`、`/concepts` 和 `/records` 页面。
7. 普通用户不能进入管理员设置；管理员只能从 `/admin/login` 登录。
8. 解析公式正常显示，图片请求正常，手机页面没有横向溢出。
9. 用户未作答时手动请求 `/api/questions/题号/reveal` 应得 403；提交非空答案后才能读取解析。
10. 浏览器开发工具查看 `Set-Cookie` 带有 `HttpOnly`、`Secure` 和 `SameSite` 属性。

题库列表接口不会返回 `answer`、官方解析、答案标志或推理链。网站的“测试连接”按钮会主动发送一次模型请求；不配置/不点击测试连接时，网站运行、用户做题和题库查询不会调用模型。

## 12. 备份、恢复和升级

### 12.1 启用每日 SQLite 快照

项目的 `gk backup --bundle` 命令使用 SQLite 一致性快照，目标目录必须不存在。备份任务保存在 `var/backups/UTC时间/`，包含 `gk.sqlite`、实际生效的 `secret.key` 和 `complete.json`。只有快照完整性、密钥解密和校验值完成后才报告成功；没有完成标记的目录禁止恢复。`--out` 只生成数据库快照，不是完整恢复备份。要在宝塔后台手工触发，也可以进入终端运行部署服务：

```bash
sudo cp /opt/gk/deploy/gk-backup.service /etc/systemd/system/
sudo cp /opt/gk/deploy/gk-backup.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now gk-backup.timer
sudo systemctl start gk-backup.service
sudo systemctl list-timers gk-backup.timer
```

这些操作需要通过 SSH 执行；宝塔面板的“计划任务”也可配置每日运行 `/bin/sh /opt/gk/deploy/backup.sh`，任务用户选 `gk`。不要同时启动两个相同时间的任务。脚本不自动删除旧快照；定期将关键备份传到另一台机器，并按自己需要的保留周期清理。磁盘写满会导致数据库无法写入。

若使用 `GK_SECRET_KEY`，Go 网站与 systemd 备份服务均读取私有 `/opt/gk/var/gk.env`（`gk:gk`、0600），在其中配置同一密钥；不要写进公开服务模板。新增模板仅声明可选 EnvironmentFile，不会自行创建或改动现有密钥。宝塔任务不会自动读取 systemd 环境，请使用 `sudo systemctl start gk-backup.service` 调度同一个备份服务，或由受限任务环境提供有效密钥。存在旧文件但未传入实际环境密钥时，解密校验会失败；不要把它视为完整成功。

图片首次迁移后可单独备份；只在图片库发生更新时重新打包。若开始蒸馏，另外备份 `var/runs/<批次名>/`，尤其是 `bucket_owner.json`。

### 12.2 恢复

先在异机或隔离环境运行 `./gk backup --verify-bundle 备份目录`，确认校验值、数据库完整性和密钥解密通过；不要在原备份上迁移或启动服务，应复制到新的恢复路径。恢复环境若设置了 `GK_SECRET_KEY`，必须删除该覆盖或改为与备份密钥相同的值，否则仍会覆盖文件密钥。

停掉网站，保留当前数据库、`-wal` 和 `-shm` 文件作为回滚副本，然后安装已确认的 `gk.sqlite` 和对应的 `secret.key`，最后启动 `gk` 并检查 `/healthz`。恢复前不要把新旧 WAL/SHM 混在一起。快照、密钥必须来自同一次备份。

完整数据库恢复会回滚密码和会话状态，现有隔离恢复测试刻意验证原 Cookie 可恢复认证。现代退出后残留的无效 HttpOnly Cookie，如果对应已撤销会话被旧快照恢复，可能重新可用；正式回滚或灾难恢复应按恢复策略统一撤销历史会话并要求重新登录。这里明确运维恢复边界，不改变现有恢复合同，本轮没有执行正式库恢复或会话清理。

### 12.3 升级

本地执行 `make release`，将新包上传到服务器。先触发一次备份，再停止 Go 服务；将旧二进制复制到 `/opt/gk/var/gk.previous`，解压新包，检查文件可执行权限，启动服务并重新验收。保留并继续使用 `/opt/gk/var/db/gk.sqlite`、`secret.key` 和 `data/`，不要解压/覆盖数据库或密钥。

若程序升级后执行了数据库 schema 迁移，回滚时同时恢复升级前 SQLite 快照与旧二进制。不要仅把旧程序复制回来后直接搭配未知的新 schema 运行。

本轮升级要求 Go 1.26.8 或更高安全补丁，自动执行 v9/v10/v11 迁移，新增选项 OCR 文本、用量账本及草稿版本锁，并恢复历史可知计数。草稿保存/交卷请求必须携带 `draft_revision`，前后端须同时升级，旧页面应刷新；并发冲突返回 409，不能自动以旧答案重试覆盖。历史缺失用量无法补算，报告会标记未知成本。密钥不存在但已有加密配置时会拒绝启动，必须恢复匹配密钥，不能以新生成密钥代替。

### 12.4 个人中心 v12 升级

新程序继续追加 v12：创建并回填 `user_profile` 默认资料/偏好/目标，追加 `app_session.public_id` 和可空设备/登录时间/脱敏 IP 元数据，以及个人统计和会话索引。旧昵称仍在 `app_user`，密码、token 摘要、练习、草稿 revision 与密钥不被改写。历史会话显示未知信息；个人资料版本独立于草稿版本。

发布前在隔离副本运行新二进制，确认启动后迁移至 v12；用已有普通用户登录检查默认资料及原草稿，保存非默认偏好并刷新，确认看板、续做、CSV 和会话。生成完整 bundle，先执行 `backup --verify-bundle`，再复制快照与匹配密钥至新的恢复目录；恢复环境清理冲突 `GK_SECRET_KEY`，验证原 Cookie、资料 revision、提交记录及再次保存。禁止在原备份目录直接启动服务或执行迁移。

升级线上服务仍按 12.3 停服务/保留旧程序/安装新包执行，不重建题库、不更换有效密钥；前端必须随新二进制更新，浏览器刷新。回滚使用升级前数据库快照、匹配密钥和旧二进制；v12 新增资料与会话显示数据可能不在升级前快照中，需明确回滚恢复点，不进行手写删表降级。

本地 `make release` 包含个人中心使用说明和集成交付报告，不包含数据库、密钥、备份、测试 fixture 或题库图片。传包前列出 `tar -tzf var/gk-linux-amd64.tar.gz` 并计算 `sha256sum var/gk-linux-amd64.tar.gz`。本轮只完成本地隔离库迁移/恢复、Chrome 页面回归与跨平台编译；线上 HTTPS/实际代理、压力/长时间运行、Windows/macOS 原生运行及异机完整恢复须在目标环境执行。完整证据见 [Task4 报告](personal-center-task4-report.md)。

个人中心最终修复增加普通用户页面身份前提，无新增迁移或依赖。部署时必须使用包含新嵌入前端和服务端的新二进制，并刷新已有页面；反向代理必须透传 `X-GK-Expected-User` 请求 header；该前提只拒绝不授权，不能用作鉴权或改写用户选择。正式代理环境仍未测。服务器无 header 保持旧 API 兼容，所以旧缓存页面不具备新页面的账号前提保护；发布后请重新进入页面。出现 `account_changed` 409 时保留输入并重新进入，不应清除当前有效 Cookie。新本地包与旧封存包的证据分开记录，见 [最终修复记录](personal-center-final-fix-report.md)；未执行线上部署。

## 13. 服务器网站验收后再做蒸馏

按用户要求，必须等网站部署完成、HTTPS、题库、用户练习和备份均验收后再启动蒸馏。**安装网站和每日备份不会运行蒸馏；本手册不会自动创建或启用蒸馏任务。**

之后若决定在这台 2 核 4GB 服务器上运行，把 API 地址、模型和 Key 配置妥当后，手动安装已有模板：

```bash
sudo cp /opt/gk/deploy/gk-distill.service.example /etc/systemd/system/gk-distill.service
sudoedit /etc/systemd/system/gk-distill.service
sudo systemctl daemon-reload
```

编辑批次名和模型名后，仅在维护时手动运行 `sudo systemctl start gk-distill`。模板采用一个模型、并发 1、CPU 50%、内存上限 900 MB，没有开机自启。全量处理耗时会高于历史多模型吞吐测试，端点额度与服务器实测资源都会影响工期。

查看服务资源占用：

```bash
sudo systemctl show gk -p MemoryCurrent -p CPUUsageNSec
sudo systemctl show gk-distill -p MemoryCurrent -p CPUUsageNSec
free -h
```

如果网站响应变慢或可用内存不足，先 `sudo systemctl stop gk-distill`。不要删除运行批次目录；保留相同 run_id 和桶归属文件可续跑。

续跑要求提示词/规范表、题集内容、模型与端点保持一致。本轮提示词升为 v2，旧 v1 批次需使用新 run_id。相同数据库和批次通过 `.gk-run-locks` 内核锁排斥并发运行，不要删除锁文件；进程退出（包括异常被杀）会自动释放锁。部分失败命令现在返回非零，查看 `distill report` 后决定续跑；不能将 systemd 退出状态当成已处理全部题目。

## 14. 常见故障

| 现象 | 检查与处理 |
|---|---|
| 无法 SSH 连接 | 确认腾讯云防火墙放通正确的 SSH 端口和来源 IP，用户名以实例系统镜像提示为准 |
| 宝塔登录失败 | 核对安装输出的随机地址、入口和端口；检查 Tencent firewall 对面板实际端口限制是否包含自己的 IP |
| Nginx 502 | `systemctl status gk`；执行 `curl http://127.0.0.1:8080/healthz`；确认反代目标和 Go 服务监听地址一致 |
| 用户登录后跳回登录页 | 确认证书有效，并检查 Nginx `proxy_set_header X-Forwarded-Proto $scheme;` 与 Cookie `Secure` |
| HTTPS 证书申请失败 | 检查备案/域名解析、80/443、AAAA、已有强制跳转和反代；先撤销冲突跳转再申请 |
| SQLite read-only / database locked | 检查 `/opt/gk/var` 及 DB 文件属主为 `gk:gk`，目录可写，磁盘未满 |
| API Key 解密失败 | 数据库和 `secret.key` 必须匹配；如果使用 `GK_SECRET_KEY`，恢复同一环境变量值 |
| 图片 404 | 图片目录层级需为 `90-图片/题目图`、`90-图片/公式图`；确认 `--data /opt/gk/data` |
| 登录 429 | 当前反代后登录请求共享限流，每分钟上限共用；稍后再试，并检查是否有自动化重试 |
| 首次启动停很久 | v7 首次迁移会建立中文 FTS 索引；查看 `journalctl -u gk`、磁盘余量和 CPU/内存，不要直接杀掉迁移中的数据库进程 |
| 宝塔安装后内存偏紧 | Nginx 保持最小安装；暂停蒸馏；检查面板插件与系统占用，保留操作系统可用内存 |
