package render

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var ErrNoLocalCore = errors.New("无可执行的本地 Linux 核心，预检由 Agent 完成")
var ErrCheck = errors.New("sing-box 配置预检失败；请在受控环境检查修订配置")

// Check never returns core stderr: it can contain certificate/private-key data.
func Check(ctx context.Context, binary string, config []byte) error {
	return check(ctx, binary, config, false)
}

// CheckDetailed is only for authenticated administrator preflight. Secrets are
// removed before stderr leaves this package; ordinary reconcile keeps Check.
func CheckDetailed(ctx context.Context, binary string, config []byte) error {
	return check(ctx, binary, config, true)
}

type diagnosticBuffer struct {
	bytes.Buffer
	truncated bool
}

func (b *diagnosticBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := 16384 - b.Len(); remaining > 0 {
		if len(p) > remaining {
			b.truncated = true
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	} else {
		b.truncated = true
	}
	return n, nil
}
func redactDiagnostic(message string, config []byte) string {
	var value any
	_ = json.Unmarshal(config, &value)
	var walk func(any, bool)
	walk = func(v any, secret bool) {
		switch x := v.(type) {
		case map[string]any:
			for key, item := range x {
				lower := strings.ToLower(key)
				walk(item, secret || strings.Contains(lower, "password") || strings.Contains(lower, "key") || strings.Contains(lower, "certificate") || strings.Contains(lower, "uuid") || strings.Contains(lower, "token"))
			}
		case []any:
			for _, item := range x {
				walk(item, secret)
			}
		case string:
			if secret && x != "" {
				message = strings.ReplaceAll(message, x, "[redacted]")
				escaped, _ := json.Marshal(x)
				message = strings.ReplaceAll(message, strings.Trim(string(escaped), "\""), "[redacted]")
				for _, line := range strings.Split(x, "\n") {
					if line != "" {
						message = strings.ReplaceAll(message, line, "[redacted]")
					}
				}
			}
		}
	}
	walk(value, false)
	// Strip terminal controls so a core's diagnostics cannot inject terminal commands.
	return strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, message)
}
func check(ctx context.Context, binary string, config []byte, detailed bool) error {
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
	var diagnostic diagnosticBuffer
	if detailed {
		cmd.Stderr = &diagnostic
	}
	if err = cmd.Run(); err != nil {
		if checkCtx.Err() != nil {
			return fmt.Errorf("%w：超时或取消", ErrCheck)
		}
		if detailed {
			message := diagnostic.String()
			if diagnostic.truncated {
				if last := strings.LastIndex(message, "\n"); last >= 0 {
					message = message[:last]
				} else {
					message = ""
				}
				message += "\n[diagnostic truncated]"
			}
			return fmt.Errorf("%w：%s", ErrCheck, redactDiagnostic(message, config))
		}
		return ErrCheck
	}
	return nil
}
