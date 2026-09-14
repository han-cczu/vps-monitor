# 面板服务端镜像。构建上下文是仓库根目录：
#   docker build -f deploy/Dockerfile -t vps-monitor-server .
#
# 三段：前端构建 → Go 构建（server + 两架构 agent）→ distroless 运行。
# agent 的二进制和安装脚本一起打进镜像，服务端启动时同步到数据卷，
# 于是「升级镜像」自动等于「升级节点能下载到的 agent」。

# ----------------------------------------------------------------------
# 1. 前端
# ----------------------------------------------------------------------
FROM node:24-alpine AS web

WORKDIR /src/web

# 先只拷清单，依赖没变时这一层能命中缓存
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
RUN npm run build

# ----------------------------------------------------------------------
# 2. Go：server 与两个架构的 agent
# ----------------------------------------------------------------------
FROM golang:1.26-alpine AS go

WORKDIR /src

# 同样先拷模块清单，锁文件没变时不用重新下依赖
COPY go.work go.work.sum ./
COPY proto/go.mod proto/go.sum proto/
COPY agent/go.mod agent/go.sum agent/
COPY server/go.mod server/go.sum server/
RUN go mod download all

COPY proto/ proto/
COPY agent/ agent/
COPY server/ server/

# 前端产物放进 go:embed 的目录。server/web/dist 在仓库里只有一个 .gitkeep，
# 这一步把真正的构建产物盖上去。
COPY --from=web /src/web/dist/ server/web/dist/

ARG VERSION=dev
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/server ./server/cmd/server \
 && GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/agent/vps-agent-linux-amd64 ./agent/cmd/agent \
 && GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/agent/vps-agent-linux-arm64 ./agent/cmd/agent \
 && cp agent/install.sh agent/uninstall.sh /out/agent/ \
 && echo "${VERSION}" > /out/agent/VERSION

# ----------------------------------------------------------------------
# 3. 运行
# ----------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12

COPY --from=go /out/server /app/server
COPY --from=go /out/agent /app/agent-dist

# VM_AGENT_DIST 让服务端启动时把 /app/agent-dist 同步进数据卷（见 internal/agentdist）
ENV VM_LISTEN=:9000 \
    VM_DATA_DIR=/data \
    VM_AGENT_DIST=/app/agent-dist

VOLUME /data
EXPOSE 9000

# 以 root 运行：数据目录是 bind mount（./data:/data），宿主上那个目录由运维创建，
# 换成 nonroot 就得先 chown 65532，装机流程里多一步容易漏、漏了表现成启动即崩。
# 容器里没有 shell，进程只有这一个二进制，暴露面本来就很小；
# 真要收紧，把 compose 里的 bind mount 换成 named volume 再加 user: 65532:65532。
ENTRYPOINT ["/app/server"]
