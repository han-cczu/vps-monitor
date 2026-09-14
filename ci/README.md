# CI

实际生效的工作流在 `.github/workflows/`，这里只做索引：

| 文件 | 作用 |
|---|---|
| `.github/workflows/ci.yml` | PR 与 main 上的静态检查：Go vet/test、前端 tsc/eslint/build |
| `.github/workflows/release.yml` | 面板镜像与 Agent 发布 |
| `.github/workflows/sing-box.yml` | 手动钉定核心版本；两架构编译、真实统计验证、Release 资产与源码 |

`sing-box-stats/` 是独立 Go 模块，用真实 SS2022 传输验证双用户计数与 reset，不依赖公网服务。进入该目录用 `GOWORK=off go run . -core /absolute/path/sing-box -output stats.json` 运行；生成的配置和随机密钥自动清理，输出只含版本与计数器证据。

`corectl-smoke.py` 在没有既有 sing-box 的 Linux/systemd 测试环境验证 Agent 下载、配置事务、真实统计和启停。需要 root，启动前拒绝覆盖已有路径；finally 清理本次服务和文件。下载服务是带 Bearer 校验的本地 HTTP fixture。执行说明和结果见 `docs/verify/corectl.md`。
