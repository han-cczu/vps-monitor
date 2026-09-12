// Package web 内嵌前端构建产物（web/dist 由 make build-web 拷贝到这里）。
//
// dist 目录整个不进版本库，只留一个 .gitkeep 让 go:embed 有东西可匹配；
// 没跑过前端构建时 Handler 回退到 placeholder.html，不会编译失败。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// placeholder 是没跑过前端构建时下发的提示页。
//
//go:embed placeholder.html
var placeholder []byte

// FS 返回 dist 目录的文件系统视图。
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		// dist 目录随二进制一起编译进来，取子目录不可能失败。
		panic(err)
	}
	return sub
}
