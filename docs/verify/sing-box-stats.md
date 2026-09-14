# sing-box 每用户流量统计验证

日期：2026-09-14。**本地 Linux amd64 容器中的真实 SS2022 双用户流量与 reset 验证通过。** 没有使用海外 VPS，尚不能替代公网链路、ARM64 真机或完整四协议验收。

## 环境与配置

- sing-box v1.14.0，源码 `0b8995879f29a9b98ee027bc17b75e101445b238`。
- 自编译：Go 1.26.2，Linux amd64，CGO disabled；标签 `with_quic,with_utls,with_v2ray_api`。
- Docker Desktop Linux 引擎，Alpine 容器；本轮使用 `--network none`，所有端点只在容器回环地址通信。
- 一个 Shadowsocks 2022 入站 `ss-in`，方法 `2022-blake3-aes-128-gcm`，两个随机密码用户 `u1`、`u2`；direct 出站；只启用 `experimental.v2ray_api`。
- 本地 HTTP 服务精确提供 100,000,000 字节响应体；两个 sing-box 客户端分别使用 u1、u2 各下载一次。不是模拟统计值。
- gRPC 方法：`/v2ray.core.app.stats.command.StatsService/QueryStats`，空筛选，`reset=true`。

## 结果

| 计数器 | reset 前字节数 | 第二次 reset 读取 |
|---|---:|---:|
| `user>>>u1>>>traffic>>>downlink` | 100000142 | 0 |
| `user>>>u1>>>traffic>>>uplink` | 115 | 0 |
| `user>>>u2>>>traffic>>>downlink` | 100000142 | 0 |
| `user>>>u2>>>traffic>>>uplink` | 115 | 0 |
| `inbound>>>ss-in>>>traffic>>>downlink` | 200000284 | 0 |
| `inbound>>>ss-in>>>traffic>>>uplink` | 230 | 0 |

每用户下载比响应体多 142 字节（HTTP 响应头），相对差额 0.000142%；上传方向 115 字节是 HTTP 请求。单独完成 u1 后只出现 u1 和入站计数，没有把流量记到 u2。两用户计数之和严格等于入站计数，重置后的六个计数均为零。

原始机器可读输出：[sing-box-stats-amd64.json](sing-box-stats-amd64.json)。临时密码没有写入此证据文件。

## 可复现验证程序

`ci/sing-box-stats` 是独立 Go 模块，不引入 Agent 或服务端依赖。程序自动生成一次性 SS2022 密钥、执行 `check`、启动服务端和两个客户端、下载、断言用户隔离/字节范围/入站总和/reset，并清理临时进程和配置。

在 Linux 上：

```sh
cd ci/sing-box-stats
GOWORK=off go run . -core /absolute/path/sing-box-v1.14.0-linux-amd64 -output stats.json
```

本轮编译为静态验证程序后，在 Docker 中运行：

```sh
docker run --rm --network none -v "$PWD/dist/step10-qa:/qa" alpine:latest \
  /qa/verify-stats -core /qa/sing-box-v1.14.0-linux-amd64 -output /qa/stats-amd64.json
```

`--network none` 下 sing-box 会打印 `network: missing default interface`；容器回环链路仍完成了下载与上述断言。ARM64 文件已交叉编译，但本机没有对应执行器，尝试执行得到 `exec format error`，没有据此宣称 ARM64 运行验证通过。工作流在 `ubuntu-24.04-arm` 原生 runner 上执行同一验证程序。

## 后续实现必须遵守

上游 proto 的 package 与实际注册服务名不同。不要仅按 proto package 拼 RPC 路径。第 11 步若拷贝生成代码，需调整客户端方法名或显式调用上述实际路径。计数器值是字节，reset 是取出后原子归零；断电后未取到的增量不能恢复，后续采集持久化不能误当作累计计数器处理。
