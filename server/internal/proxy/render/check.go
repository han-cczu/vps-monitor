package render

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"
)

var ErrNoLocalCore = errors.New("无可执行的本地 Linux 核心，预检由 Agent 完成")
var ErrCheck = errors.New("sing-box 配置预检失败；请在受控环境检查修订配置")

// Check never returns core stderr: it can contain certificate/private-key data.
func Check(ctx context.Context, binary string, config []byte) error {
	if _, err := os.Stat(binary); errors.Is(err, os.ErrNotExist) {
		return ErrNoLocalCore
	} else if err != nil {
		return err
	}
	if runtime.GOOS != "linux" {
		return ErrNoLocalCore
	}
	dir, err := os.MkdirTemp("", "vps-core-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	f, err := os.CreateTemp(dir, "config-*.json")
	if err != nil {
		return err
	}
	_, err = f.Write(config)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, binary, "check", "-c", f.Name())
	cmd.Dir = dir
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		if checkCtx.Err() != nil {
			return fmt.Errorf("%w：超时或取消", ErrCheck)
		}
		return ErrCheck
	}
	return nil
}
