package api

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"vpsmon/proto"

	"github.com/go-chi/chi/v5"
)

// agentDirName 是 {VM_DATA_DIR} 下存放 agent 安装脚本与二进制的目录。
// 步骤 07 的镜像会把 CI 产物放进去；本地开发时 make build-agent 后手工拷。
const agentDirName = "agent"

// agentFiles 是允许对外下载的文件名白名单，值是响应的 Content-Type。
//
// 这两个接口是公开的（agent 装机时还没有任何凭据），所以只认固定的几个名字：
// 不做前缀匹配、不接受路径分隔符，从根上堵掉目录穿越。
var agentFiles = map[string]string{
	"vps-agent-linux-amd64.sha256": "text/plain; charset=utf-8",
	"vps-agent-linux-arm64.sha256": "text/plain; charset=utf-8",
	"install.sh":                   "text/x-shellscript; charset=utf-8",
	"uninstall.sh":                 "text/x-shellscript; charset=utf-8",
	"vps-agent-linux-amd64":        "application/octet-stream",
	"vps-agent-linux-arm64":        "application/octet-stream",
}

// installScript 处理 GET /install.sh —— 一键安装命令里 curl 的那个地址。
func (d *Deps) installScript(w http.ResponseWriter, r *http.Request) {
	d.serveAgentFile(w, r, "install.sh")
}

// agentFile 处理 GET /agent/{file}：安装脚本与 agent 二进制。
func (d *Deps) agentFile(w http.ResponseWriter, r *http.Request) {
	d.serveAgentFile(w, r, chi.URLParam(r, "file"))
}

// serveAgentFile 从 {DataDir}/agent/ 下发一个白名单内的文件。
//
// 这里的 404 是纯文本：调用方通常是 curl | bash，给它一段 HTML 或 JSON 没有意义。
func (d *Deps) serveAgentFile(w http.ResponseWriter, r *http.Request, name string) {
	contentType, ok := agentFiles[name]
	if !ok {
		notFoundText(w)
		return
	}

	checksum := strings.HasSuffix(name, ".sha256")
	binaryName := strings.TrimSuffix(name, ".sha256")
	path := filepath.Join(d.DataDir, agentDirName, binaryName)
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
		notFoundText(w)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		// 文件还没放进来是常态（步骤 04 产出 agent 之前根本没有），不该是 500
		if errors.Is(err, fs.ErrNotExist) {
			slog.Debug("agent file missing", "path", path)
			notFoundText(w)
			return
		}
		slog.Error("open agent file failed", "path", path, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		slog.Error("stat agent file failed", "path", path, "err", err, "is_dir", err == nil && stat.IsDir())
		notFoundText(w)
		return
	}

	if checksum {
		if stat.Size() <= 0 || stat.Size() > proto.MaxAgentBinarySize {
			notFoundText(w)
			return
		}
		hash := sha256.New()
		n, err := io.Copy(hash, io.LimitReader(f, proto.MaxAgentBinarySize+1))
		if err != nil || n != stat.Size() {
			http.Error(w, "checksum unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = fmt.Fprintf(w, "%x  %s\n", hash.Sum(nil), binaryName)
		return
	}
	w.Header().Set("Content-Type", contentType)
	// 内容会随版本变（文件名固定），交给 If-Modified-Since / Range 判断，不做长缓存
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, name, stat.ModTime(), f)
}

func notFoundText(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("not found\n"))
}
