// Package web 内嵌前端构建产物（web/dist 由 make build-web 拷贝到这里）。
//
// 仓库里只提交一个占位 index.html，保证 go build 在没跑过前端构建时也能成功。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// FS 返回 dist 目录的文件系统视图。
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		// dist 目录随二进制一起编译进来，取子目录不可能失败。
		panic(err)
	}
	return sub
}
