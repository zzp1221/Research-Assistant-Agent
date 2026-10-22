# Keep the builder version aligned with docker-sandbox/go.mod (go 1.26.1).
FROM golang:1.26.1-bookworm AS builder

ENV GOTOOLCHAIN=local \
    GOPROXY=https://goproxy.cn,direct
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/sandbox .

# 直接使用官方 Docker CLI 运行时镜像，避免 apt 源不稳定
FROM docker:27-cli
WORKDIR /app
COPY --from=builder /app/bin/sandbox .

# 暴露端口
EXPOSE 8082

# 运行服务
CMD ["./sandbox"]
