package render

import (
	"strings"
	"testing"
)

func TestDetailedCheckDiagnosticRedactsSecrets(t *testing.T) {
	raw := []byte(`{"inbounds":[{"users":[{"uuid":"uuid-secret","password":"secret-pass"}],"tls":{"private_key":"secret-key","certificate":["cert-line"]}}]}`)
	text := redactDiagnostic("decode config: unknown field broken; uuid-secret secret-pass secret-key cert-line", raw)
	for _, secret := range []string{"uuid-secret", "secret-pass", "secret-key", "cert-line"} {
		if strings.Contains(text, secret) {
			t.Fatal("diagnostic leaked secret")
		}
	}
	if !strings.Contains(text, "unknown field broken") {
		t.Fatal("useful diagnostic removed")
	}
}
