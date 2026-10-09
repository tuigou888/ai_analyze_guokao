# --- Stage 1: Frontend ---
FROM node:24-alpine AS frontend-build
WORKDIR /build
COPY web/ ./web/
RUN cd web && npm ci && npm run build

# --- Stage 2: Go binary ---
FROM golang:1.26-alpine AS go-build
ARG TARGETARCH
WORKDIR /build
COPY go.mod go.sum ./
ENV GOPROXY=https://goproxy.cn,https://goproxy.io,direct
RUN go mod download
COPY internal/ ./internal/
COPY cmd/ ./cmd/
COPY --from=frontend-build /build/cmd/gk/web_dist ./cmd/gk/web_dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -buildvcs=false -trimpath -o /out/gk ./cmd/gk

# --- Stage 3: Runtime ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates curl && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime 2>/dev/null || true
WORKDIR /opt/gk
COPY --from=go-build /out/gk /opt/gk/gk
COPY taxonomy/v1/taxonomy.yaml /opt/gk/taxonomy/v1/
RUN chmod +x /opt/gk/gk && \
    mkdir -p /opt/gk/var/db /opt/gk/var/data /opt/gk/var/logs /opt/gk/var/backups /opt/gk/var/runs

EXPOSE 8080
ENV GOMAXPROCS=2
ENV GOMEMLIMIT=768MiB

ENTRYPOINT ["/opt/gk/gk"]
CMD ["serve"]
