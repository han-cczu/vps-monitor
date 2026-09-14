# sing-box 版本约定

核对日期：2026-09-14。第 10 步钉定 **v1.14.0**，后续第 11–15 步按此版本实现。

| 项目 | 值 |
|---|---|
| 最新稳定 Release | [v1.14.0](https://github.com/SagerNet/sing-box/releases/tag/v1.14.0)，发布于 2026-08-31 |
| 源码提交 | `0b8995879f29a9b98ee027bc17b75e101445b238` |
| 上游 go.mod | `go 1.25.5`；工作流从该文件选择 Go |
| 本地实测编译器 | Go 1.26.2，CGO_ENABLED=0 |
| 产物 | Linux amd64、arm64；最大 64 MiB / 文件 |
| 标签 | `with_quic,with_utls,with_v2ray_api` |
| 版本注入 | `-X github.com/sagernet/sing-box/constant.Version=1.14.0` |

源码依据：[go.mod](https://github.com/SagerNet/sing-box/blob/v1.14.0/go.mod)、[该 tag 构建文档](https://github.com/SagerNet/sing-box/blob/v1.14.0/docs/installation/build-from-source.md)、[默认标签](https://github.com/SagerNet/sing-box/blob/v1.14.0/release/DEFAULT_BUILD_TAGS)。

该版本默认 Linux 标签为：

```text
with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_tailscale,with_ccm,with_ocm,with_cloudflared,with_naive_outbound,with_usbip,with_openvpn,with_openconnect,badlinkname,tfogo_checklinkname0
```

本项目按设计只保留 QUIC / uTLS，并额外启用默认不包含的 V2Ray API。该版本没有单独的 `with_reality_server` 标签。证书由面板后续步骤生成和下发，因此本步未启用 ACME；不会引入 TUN、OpenVPN、OpenConnect 等额外能力。

## 对后续实现有影响的变化

1. **统计 RPC 的实际服务名**：`v2ray.core.app.stats.command.StatsService`。虽然 proto 的 package 是 `experimental.v2rayapi`，上游 [stats.go](https://github.com/SagerNet/sing-box/blob/v1.14.0/experimental/v2rayapi/stats.go) 的 init 会覆写 ServiceDesc。第 11 步不能直接使用未调整路径的生成客户端，否则可能得到 `Unimplemented`。本步验证使用实际路径 `/v2ray.core.app.stats.command.StatsService/QueryStats`。
2. `QueryStatsRequest.pattern` 已弃用，筛选使用 `patterns` / `regexp`；本项目全量增量读取用空筛选 + `reset=true`。用户名称仍对应 `user>>>NAME>>>traffic>>>uplink/downlink`。
3. 1.14 引入独立的 sing-box API service / Dashboard；它与本项目使用的 `experimental.v2ray_api` 是两个接口，本项目不启用前者。
4. 内联 TLS ACME 配置迁移到 `certificate_providers`；后续渲染器使用面板提供的证书和密钥文件，不能复制旧版 ACME 模板。DNS、HTTP/2、QUIC 也有字段调整，配置需要用钉定核心执行 `check`。详见 [v1.14.0 迁移说明](https://github.com/SagerNet/sing-box/blob/v1.14.0/docs/migration.md)。
5. 设计文档中的旧版 Clash/V2Ray tracker 互相覆盖问题，不直接视为 1.14.0 的已证实缺陷：该版本 [box.go](https://github.com/SagerNet/sing-box/blob/v1.14.0/box.go) 使用 `AppendTracker`。本项目仍只开 V2Ray API，未实测同时打开两个接口的行为。

## 可复现构建

```sh
git clone --branch v1.14.0 --depth 1 https://github.com/SagerNet/sing-box.git
cd sing-box
test "$(git rev-parse HEAD)" = 0b8995879f29a9b98ee027bc17b75e101445b238
mkdir -p dist
for arch in amd64 arm64; do
  GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
    -tags 'with_quic,with_utls,with_v2ray_api' \
    -ldflags '-s -w -X github.com/sagernet/sing-box/constant.Version=1.14.0' \
    -o "dist/sing-box-v1.14.0-linux-$arch" ./cmd/sing-box
done
cd dist
sha256sum sing-box-* > SHA256SUMS
```

工作流 `.github/workflows/sing-box.yml` 在各自原生架构 runner 上执行 `version` 和双用户统计验证，两者均通过才发布本仓库 `sing-box-v1.14.0` Release，并附对应源码归档和许可证。重复执行不会覆盖同名 Release 的既有产物。

上传服务读取 Go buildinfo 和 ELF 验证程序路径、Linux 架构、静态链接及三个必要标签，并校验管理员提供的 SHA256。`-trimpath` 产物不保留 `-ldflags`，所以服务端不声称能通过 buildinfo 证明版本号；版本值由构建工作流执行 `version` 核对，上传者须填写匹配的标签。服务端在上传过程中不执行二进制。

本地构建实测值（更换 Go 工具链后校验和可能变化，以同次构建的 SUMS 为准）：

| 架构 | 字节数 | SHA256 |
|---|---:|---|
| amd64 | 40243362 | `d00d965478fcc006e6bc58d98a7abf18d8eea6a9d52310fda3828e614dc9671c` |
| arm64 | 37486754 | `b3d5c10c5e4cab67ba7f1d5e24c3be87849300ebcb934ebf87e7398389be1bc1` |

统计证据见 [双用户验证](verify/sing-box-stats.md)。仓库当前没有 remote，GitHub 工作流和 Release 尚未实际触发。
