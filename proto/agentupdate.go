package proto

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const TypeAgentUpdate = "agent.update"
const MaxAgentBinarySize int64 = 64 << 20

// AgentUpdate is only accepted over the authenticated panel connection.
type AgentUpdate struct {
	Type    string `json:"type"`
	Version string `json:"version"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
}

func versionParts(v string) ([3]uint64, bool) {
	var out [3]uint64
	if len(v) > 48 {
		return out, false
	}
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, s := range parts {
		if s == "" || (len(s) > 1 && s[0] == '0') {
			return out, false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return out, false
			}
		}
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
func ValidAgentVersion(v string) bool { _, ok := versionParts(v); return ok }

// AgentVersionNewer deliberately rejects dev, unknown, and prerelease versions.
func AgentVersionNewer(target, current string) bool {
	t, ok := versionParts(target)
	if !ok {
		return false
	}
	c, ok := versionParts(current)
	if !ok {
		return false
	}
	for i := 0; i < 3; i++ {
		if t[i] != c[i] {
			return t[i] > c[i]
		}
	}
	return false
}
func (a AgentUpdate) Validate(arch, current string) error {
	if a.Type != TypeAgentUpdate {
		return fmt.Errorf("unsupported update message")
	}
	if arch != "amd64" && arch != "arm64" {
		return fmt.Errorf("unsupported architecture")
	}
	if a.File != "vps-agent-linux-"+arch {
		return fmt.Errorf("agent filename does not match architecture")
	}
	if !AgentVersionNewer(a.Version, current) {
		return fmt.Errorf("target must be a newer stable version; dev/unknown versions require manual installation")
	}
	if len(a.SHA256) != 64 || strings.ToLower(a.SHA256) != a.SHA256 {
		return fmt.Errorf("invalid SHA256")
	}
	if _, err := hex.DecodeString(a.SHA256); err != nil {
		return fmt.Errorf("invalid SHA256")
	}
	return nil
}
