# OCR 独立环境与验收

网站运行只需要 Go 程序、SQLite 和图片目录。以下环境用于离线 OCR，不要在网站服务器上盲目安装 GPU 依赖或同时运行全量识别。

安装命令在源码工作区执行；网站部署包不包含 Python 工具和依赖锁定文件，不能在部署包目录直接运行这些命令。

## 已验证基线

- Linux x86_64、Python 3.12.14、Paddle CPU 3.3.1、PaddleOCR 3.7.0、PaddleX 3.7.2。
- 版本输入：`tools/requirements-ocr-cpu.in`；包括 69 个传递依赖的精确版本锁：`tools/requirements-ocr-cpu.lock`。
- PP-FormulaNet_plus-M 和 PP-OCRv5_server_det/rec 缓存模型完成 3 张真实图片推理；公式、单字符图片和资料分析表格均返回成功。
- 公式样本与旧 OCR 一致；表格 25 个数值经原图核对一致。单字符样本输出 Greek ν，与旧记录不同，不能把旧 OCR 当作绝对真值；此验证不代表全量准确率。

## 安装到独立环境

使用干净虚拟环境，不使用 `--system-site-packages`，也不要同时安装 paddlepaddle 与 paddlepaddle-gpu。验证时临时环境继承了现有普通依赖，并单独覆盖 CPU、NumPy 和安全补丁；下列锁定列表包含该流程实际需要的依赖闭包。

```bash
python3.12 -m venv .venv-ocr
.venv-ocr/bin/python -m pip install --no-deps \
  --index-url https://www.paddlepaddle.org.cn/packages/stable/cpu/ paddlepaddle==3.3.1
.venv-ocr/bin/python -m pip install --no-deps -r tools/requirements-ocr-cpu.lock
.venv-ocr/bin/python -m pip check
```

CPU wheel 来源为 [Paddle 官方安装源](https://www.paddlepaddle.org.cn/documentation/docs/zh/install/pip/linux-pip.html)。锁定文件是精确版本锁，没有声称具备 artifact hash 锁定；严格的可重现发布还应固定 wheel 哈希或构建不可变镜像。

本机原 Conda NumPy 使用 MKL，与 Paddle 推理库同时加载时出现 `Intel oneMKL function load error`。临时环境使用同版本 PyPI NumPy wheel 后推理通过；不要将原 Conda/GPU 环境直接复制为可用基线。

## 运行

先通过 `gk media index` 生成图片哈希。CPU 环境可使用以下设置，单张验证后再扩大批次：

```bash
CUDA_VISIBLE_DEVICES=-1 OMP_NUM_THREADS=1 PADDLE_PDX_ENABLE_MKLDNN_BYDEFAULT=False \
  ./var/gk media ocr-formula --python .venv-ocr/bin/python --batch 1 --limit 1
CUDA_VISIBLE_DEVICES=-1 OMP_NUM_THREADS=1 PADDLE_PDX_ENABLE_MKLDNN_BYDEFAULT=False \
  ./var/gk media ocr-text --python .venv-ocr/bin/python --batch 1 --limit 1
```

首台机器没有缓存权重时需要从官方模型源下载；有缓存且需要离线运行时可设置 `PADDLE_PDX_DISABLE_MODEL_SOURCE_CHECK=True` 和 `HF_HUB_OFFLINE=1`。文本 worker 显式使用 PP-OCRv5，避免升级包后默认模型变成 v6，但产物仍被标为 v5。

worker 非零退出、结果缺失、图片识别失败会使 Go 命令返回错误。已有正常结果保留，未完成任务可以重跑；未知或重复 SHA、非法状态不允许回填其他图片。

## 安全审计结果（2026-10-02）

原环境发现 AnyIO 4.12.1、Protobuf 4.25.8 的已知漏洞，审计输出包含 4 条记录（Protobuf 同一公告重复），涉及 3 个独立公告。独立基线锁定 AnyIO 4.14.2、Protobuf 6.33.5，并完成推理回归。

- AnyIO TLS IDNA 和进程池 stderr 问题：[TLS 公告](https://github.com/agronholm/anyio/security/advisories/GHSA-82r6-8w77-94w6)、[进程池公告](https://github.com/agronholm/anyio/security/advisories/GHSA-5p39-cfhj-2xmp)。这些是依赖漏洞，不据此断言本项目存在可从网站入口利用的路径。
- Protobuf 递归深度问题：[维护者公告](https://github.com/protocolbuffers/protobuf/security/advisories/GHSA-7gcm-g887-7qv7)。

对 CPU 锁定列表执行 `pip-audit --disable-pip --no-deps -r tools/requirements-ocr-cpu.lock`，69 个包全部完成查询、0 个已知漏洞、0 个跳过。结果：`test-artifacts/followup-20261001/ocr-cpu-audit.json`。模型样本结果：同目录 `ocr-results.json`。

GPU 尚未验证：当前驱动不可用，且原 GPU wheel 的 12 项 CUDA 依赖版本不匹配。GPU 环境需根据实际硬件/驱动另行生成并验收，不能套用 CPU 锁定文件。
