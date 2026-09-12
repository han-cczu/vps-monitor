# VPS Monitor · 前端

基于 [Minimal UI starter](https://mui.com/store/items/minimal-dashboard/) v7.7.0（React 19 + MUI 9 + Vite），
从完整版拷入了 `chart`、`custom-data-grid`、`custom-dialog`、`snackbar`、`empty-content`、`custom-breadcrumbs` 六个组件目录。

## 开发

```sh
npm i
npm run dev     # http://localhost:8080，/api 与 /api/ws 代理到 Go 服务端 :9000
```

Go 服务端在仓库根目录用 `make dev-server`（或 `go run ./server/cmd/server`）启动。

## 检查与构建

```sh
npm run tsc:check   # 类型
npm run lint        # ESLint
npm run build       # 产出 dist/，由 make build-web 拷到 server/web/dist 供 go:embed
```

## 约定

- 包管理用 **npm**（starter 原本声明 yarn，已移除 `packageManager` 字段与 `yarn.lock`）
- `VITE_SERVER_URL` 留空表示同源；生产环境前端由 `vps-server` 内嵌下发
- 主题默认深色，见 `src/theme/theme-config.ts` 的 `defaultMode`
