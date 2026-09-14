package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestUnknownMessageOnlyWarns(t *testing.T) {
	var b bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&b, nil)))
	defer slog.SetDefault(old)
	// Nil executors make accidental dispatch to any action fail immediately.
	handleMessage("exec", []byte(`{"type":"exec","command":"id"}`), nil, nil, nil, nil)
	if !strings.Contains(b.String(), `"level":"WARN"`) || !strings.Contains(b.String(), `"type":"exec"`) {
		t.Fatalf("missing warning: %s", b.String())
	}
}
