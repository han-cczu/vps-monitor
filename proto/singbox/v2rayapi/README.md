# sing-box stats RPC

Schema source: `SagerNet/sing-box` tag `v1.14.0`, commit
`0b8995879f29a9b98ee027bc17b75e101445b238`,
`experimental/v2rayapi/stats.proto`. Upstream license is included in `LICENSE.upstream`.

The message fields are unchanged. The protobuf package is changed to
`v2ray.core.app.stats.command`: upstream `stats.go` overrides the generated
`StatsService_ServiceDesc.ServiceName` to this name at runtime. Using the original
`experimental.v2rayapi` client method produces `Unimplemented`. `go_package` is
changed to this repository's import path. No sing-box runtime code is linked into
the agent. The root `vpsmon/proto` package still imports only the standard library;
this generated subpackage imports gRPC/protobuf.

Regenerate from this directory (protoc 29.3, Go plugin 1.36.11, gRPC plugin 1.5.1):

```sh
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
protoc --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative stats.proto
```
