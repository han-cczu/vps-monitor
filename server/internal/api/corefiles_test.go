package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"vpsmon/server/internal/corefiles"
	"vpsmon/server/internal/testutil"
)

func coreTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return newTestEnv(t, func(d *Deps) {
		s, e := corefiles.New(filepath.Join(t.TempDir(), "corefiles"), d.DB)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { s.Close() })
		d.CoreFiles = s
	})
}
func uploadCore(t *testing.T, e *testEnv, token, v, a string, data []byte, expected int) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	sum := sha256.Sum256(data)
	for k, v := range map[string]string{"version": v, "arch": a, "sha256": hex.EncodeToString(sum[:])} {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	w, err := mw.CreateFormFile("file", "ignored-client-filename")
	if err != nil {
		t.Fatal(err)
	}
	w.Write(data)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/api/corefiles", &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != expected {
		t.Fatalf("upload status=%d want=%d body=%s", resp.StatusCode, expected, raw)
	}
}
func TestCoreFilesAuthUploadActivationAndDownload(t *testing.T) {
	e := coreTestEnv(t)
	jwt := e.adminToken(t)
	id, agentToken, _ := e.createServer(t, jwt, newServerBody())
	for _, tc := range []struct{ method, path string }{{"GET", "/api/corefiles"}, {"POST", "/api/corefiles"}, {"POST", "/api/corefiles/fetch"}, {"PUT", "/api/corefiles/current"}, {"DELETE", "/api/corefiles/v1.14.0"}} {
		resp, _ := e.do(t, tc.method, tc.path, "", nil)
		if resp.StatusCode != 401 {
			t.Fatalf("missing admin auth: %s %d", tc.path, resp.StatusCode)
		}
		resp, _ = e.do(t, tc.method, tc.path, agentToken, nil)
		if resp.StatusCode != 401 {
			t.Fatalf("agent accessed admin route: %d", resp.StatusCode)
		}
	}
	data := testutil.CoreBinary(t, "v1.14.0", "amd64", "with_quic,with_utls,with_v2ray_api")
	arm := testutil.CoreBinary(t, "v1.14.0", "arm64", "with_quic,with_utls,with_v2ray_api")
	uploadCore(t, e, jwt, "v1.14.0", "amd64", data, 201)
	resp, _ := e.do(t, "PUT", "/api/corefiles/current", jwt, map[string]string{"version": "v1.14.0"})
	if resp.StatusCode != 409 {
		t.Fatal("incomplete current accepted")
	}
	uploadCore(t, e, jwt, "v1.14.0", "arm64", arm, 201)
	resp, _ = e.do(t, "PUT", "/api/corefiles/current", jwt, map[string]string{"version": "v1.14.0"})
	if resp.StatusCode != 200 {
		t.Fatal("current failed")
	}
	resp, _ = e.do(t, "DELETE", "/api/corefiles/v1.14.0", jwt, nil)
	if resp.StatusCode != 409 {
		t.Fatal("deleted current")
	}
	resp, body := e.do(t, "GET", "/api/corefiles", jwt, nil)
	if resp.StatusCode != 200 || body["pinned_version"] != "v1.14.0" {
		t.Fatal(body)
	}
	for _, token := range []string{"", jwt, "wrong"} {
		resp, _ := e.do(t, "GET", "/api/agent/corefiles/v1.14.0/amd64", token, nil)
		if resp.StatusCode != 401 {
			t.Fatal("bad agent auth accepted")
		}
	}
	download := func(token, rangeHeader, etag string) (int, http.Header, []byte) {
		t.Helper()
		req, _ := http.NewRequest("GET", e.srv.URL+"/api/agent/corefiles/v1.14.0/amd64", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := e.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, raw
	}
	status, headers, raw := download(agentToken, "", "")
	hash := sha256.Sum256(data)
	if status != 200 || !bytes.Equal(raw, data) || headers.Get("X-Checksum-Sha256") != hex.EncodeToString(hash[:]) {
		t.Fatal("download checksum mismatch")
	}
	status, _, raw = download(agentToken, "bytes=0-3", "")
	if status != 206 || !bytes.Equal(raw, data[:4]) {
		t.Fatal("range request failed")
	}
	status, _, _ = download(agentToken, "", headers.Get("ETag"))
	if status != 304 {
		t.Fatalf("conditional request=%d", status)
	}
	resp, _ = e.do(t, "POST", "/api/servers/"+jsonNumber(id)+"/token", jwt, nil)
	if resp.StatusCode != 200 {
		t.Fatal("reset token failed")
	}
	status, _, _ = download(agentToken, "", "")
	if status != 401 {
		t.Fatal("revoked token still downloads")
	}
	var auditCount int
	if err := e.db.QueryRow("SELECT count(*) FROM audit_log WHERE action LIKE 'corefile.%'").Scan(&auditCount); err != nil || auditCount != 3 {
		t.Fatalf("audit=%d %v", auditCount, err)
	}
}
func jsonNumber(n int64) string { data, _ := json.Marshal(n); return string(data) }
func TestCoreFileValidationRoutes(t *testing.T) {
	e := coreTestEnv(t)
	jwt := e.adminToken(t)
	uploadCore(t, e, jwt, "v1.14.0", "amd64", []byte("not binary"), 400)
	for _, version := range []string{"../outside", "current-local", "v1.14.0/../v1.14.1"} {
		resp, _ := e.do(t, "PUT", "/api/corefiles/current", jwt, map[string]string{"version": version})
		if resp.StatusCode != 400 {
			t.Fatalf("bad version: %s %d", version, resp.StatusCode)
		}
	}
	resp, _ := e.do(t, "POST", "/api/corefiles/fetch", jwt, map[string]string{"version": "v1.14.0", "arch": "amd64", "url": "http://127.0.0.1/secret", "sha256": strings.Repeat("0", 64)})
	if resp.StatusCode != 400 {
		t.Fatal("internal fetch allowed")
	}
	resp, _ = e.do(t, "DELETE", "/api/corefiles/v1.14.0", jwt, nil)
	if resp.StatusCode != 404 {
		t.Fatal("missing version deletion")
	}
}
