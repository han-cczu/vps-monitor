# 运维手册

> 本文件随代码走：每个步骤新增的部署、备份、排障动作都记到这里。

## 1. 本地开发

前置：Go 1.26、Node 24、npm 11。

```sh
# 一次性：装前端依赖
cd web && npm i

# 两个终端
make dev-server   # Go 服务端，监听 :9000
make dev-web      # Vite dev server，监听 :8080，/api 与 /api/ws 代理到 :9000
```

Windows 上如果没装 make，直接跑等价命令：

```sh
go run ./server/cmd/server     # 等价于 make dev-server
cd web && npm run dev          # 等价于 make dev-web
```

npm 全局缓存目录若不可写（典型报错 `EPERM ... node_cache`），用仓库内缓存绕开：

```sh
npm i --cache ../.npm
```

打开 http://localhost:8080 。健康检查：`curl http://localhost:9000/api/health`。

### 环境变量

| 变量 | 默认 | 说明 | 引入步骤 |
|---|---|---|---|
| `VM_LISTEN` | `:9000` | 服务端监听地址 | 01 |
| `VM_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` | 01 |

## 2. 构建

```sh
make build-web      # 前端构建产物拷到 server/web/dist
make build-server   # 内嵌前端，产出 dist/server/vps-server
make build-agent    # 产出 dist/agent/vps-agent-linux-{amd64,arm64}
```

## 3. 部署

（步骤 07 填充。）

## 4. 备份与恢复

（步骤 07 填充。）

## 5. 排障

（随步骤累积。）
