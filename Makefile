DB   ?= var/db/gk.sqlite
DATA ?= data

.PHONY: help build test test-short ingest reingest stats fmt vet clean \
        media-index media-ocr media-ocr-text media-backfill media-report media-sample media \
        web web-install serve admin-init config release backup distill-smoke distill-mvp distill-report

help:
	@grep -E '^[a-zA-Z_-]+:' Makefile | grep -v '^\.PHONY' | cut -d: -f1 | sort | sed 's/^/  make /'

build:
	go build -buildvcs=false -o var/gk ./cmd/gk

test:
	go test ./...

# 跳过真实数据集扫描的快速回归（CI 上无 data/ 时用这个）
test-short:
	go test -short ./...

ingest: build
	./var/gk ingest --data $(DATA) --db $(DB)

# 从零重建：解析入库约 6 秒（58890 个题块）
reingest: build
	./var/gk ingest --data $(DATA) --db $(DB) --reset
	./var/gk stats --db $(DB)

stats: build
	./var/gk stats --db $(DB) --dup 10

# ---- P2 公式还原 ----
# 需要本机有 paddleocr（见 docs/架构方案.md §9 P2）
media-index: build
	./var/gk media index --data $(DATA) --db $(DB)

# GPU 上约 8.3 张/秒，全量 32346 张 ≈ 65 分钟。可中断，重跑自动接续。
media-ocr: build
	./var/gk media ocr-formula --data $(DATA) --db $(DB) --shard 11000 --batch 24

media-backfill: build
	./var/gk media backfill --data $(DATA) --db $(DB)

media-report: build
	./var/gk media report --data $(DATA) --db $(DB)

media-sample: build
	./var/gk media sample --data $(DATA) --db $(DB) --n 30

# 题目图 → 表格文本。资料分析的材料表格靠这一步才能被模型读到，约 11 分钟。
media-ocr-text: build
	./var/gk media ocr-text --data $(DATA) --db $(DB) --shard 3000 --batch 8

# 从图片索引到回填的完整 P2 流程
media: media-index media-ocr media-ocr-text media-backfill media-report

# ---- P3 蒸馏与 Web ----

# 构建前端（产物直接输出到 cmd/gk/web_dist，由 go:embed 打进二进制）
web-install:
	cd web && npm ci

web: web-install
	cd web && npm run build
	@echo "前端已构建。重新编译二进制以嵌入: make build"

# 首次使用：建管理员账号，然后启动服务到 /admin 填 API 地址与密钥
admin-init: build
	./var/gk admin create --db $(DB) --user admin

serve: build
	./var/gk serve --db $(DB) --addr 127.0.0.1:8081

config: build
	./var/gk config list --db $(DB)

# 先看提示词对不对，再花钱跑
distill-smoke: build
	./var/gk distill prompt --db $(DB) --subject 资料分析

# P3 的关键一步：50 题分层打散抽样，成本约 $0.01
distill-mvp: build
	./var/gk distill run --db $(DB) --limit 50

distill-report: build
	./var/gk distill report --db $(DB)

fmt:
	gofmt -w ./cmd ./internal

vet:
	go vet ./...

clean:
	rm -rf var/

# 本地构建服务器部署包；不打包数据库、图片或密钥，不执行蒸馏。
release: web
	mkdir -p var/release/docs
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -o var/release/gk ./cmd/gk
	cp -R deploy taxonomy var/release/
	cp README.md var/release/README.md
	cp docs/deployment-2c4g.md docs/api-design.md docs/website-completion.md var/release/docs/
	tar -czf var/gk-linux-amd64.tar.gz -C var/release README.md gk deploy taxonomy docs

backup: build
	./var/gk backup --db $(DB) --out var/backups/gk-$(shell date +%Y%m%d-%H%M%S).sqlite
