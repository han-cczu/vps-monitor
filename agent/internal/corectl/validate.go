package corectl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"vpsmon/proto"
)

const MaxConfigSize = 1 << 20

var versionRE = regexp.MustCompile(`^v[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}$`)
var portRE = regexp.MustCompile(`^[1-9][0-9]{0,4}/(tcp|udp)$`)

func CompactConfig(raw []byte) ([]byte, string, error) {
	if len(raw) == 0 || len(raw) >= MaxConfigSize {
		return nil, "", fmt.Errorf("config must be a JSON object smaller than 1 MiB")
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return nil, "", fmt.Errorf("invalid config JSON")
	}
	if b.Bytes()[0] != '{' {
		return nil, "", fmt.Errorf("config must be a JSON object")
	}
	sum := sha256.Sum256(b.Bytes())
	return b.Bytes(), hex.EncodeToString(sum[:]), nil
}

func validSHA(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && s == strings.ToLower(s)
}

func Validate(a proto.CoreApply) error {
	if a.Type != proto.TypeCoreApply || a.Core != "sing-box" || a.Revision <= 0 || !versionRE.MatchString(a.Version) {
		return fmt.Errorf("invalid core.apply type, core, revision or version")
	}
	if len(a.ReqID) > 128 || len(a.Ports) > 1024 {
		return fmt.Errorf("too many ports or req_id too long")
	}
	seen := make(map[string]bool)
	for _, p := range a.Ports {
		if !portRE.MatchString(p) {
			return fmt.Errorf("invalid port (expected 1..65535/tcp or udp)")
		}
		n, _ := strconv.Atoi(strings.SplitN(p, "/", 2)[0])
		if n > 65535 || seen[p] {
			return fmt.Errorf("port out of range or duplicated")
		}
		seen[p] = true
	}
	_, sum, err := CompactConfig(a.Config)
	if err != nil {
		return err
	}
	if !validSHA(a.ConfigSHA256) || sum != a.ConfigSHA256 {
		return fmt.Errorf("config SHA256 mismatch")
	}
	return nil
}

func validateAction(a proto.CoreAction) error {
	if a.Type != proto.TypeCoreAction || len(a.ReqID) > 128 {
		return fmt.Errorf("invalid core.action")
	}
	switch a.Action {
	case "install":
		if !versionRE.MatchString(a.Version) || !validSHA(a.SHA256) {
			return fmt.Errorf("install requires version vX.Y.Z and lowercase SHA256")
		}
		if a.File != "" && a.File != "sing-box-linux-"+runtime.GOARCH {
			return fmt.Errorf("install file must match this node's architecture")
		}
	case "start", "stop", "restart":
		if a.File != "" || a.SHA256 != "" || a.Version != "" {
			return fmt.Errorf("unexpected install fields on service action")
		}
	default:
		return fmt.Errorf("unsupported core action")
	}
	return nil
}

func ValidateStatsAddress(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("stats address must be a loopback IP:port")
	}
	ip := net.ParseIP(host)
	n, err := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("stats address must be a loopback IP:port")
	}
	return nil
}

func decode(raw []byte, dst any) error {
	if len(raw) > MaxConfigSize+65536 {
		return fmt.Errorf("core message too large")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("invalid core message schema")
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}
