package agentdist

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"vpsmon/proto"
)

// InstalledVersion reports custom/dev builds as well, without making them eligible for updates.
func InstalledVersion(dir string) (string, error) {
	return readVersion(dir)
}

func CurrentVersion(dir string) (string, error) {
	v, err := readVersion(dir)
	if err != nil {
		return "", err
	}
	if !proto.ValidAgentVersion(v) {
		return "", fmt.Errorf("no stable agent release installed")
	}
	return v, nil
}
func UpdateArtifact(dir, arch, current string) (proto.AgentUpdate, error) {
	version, err := CurrentVersion(dir)
	if err != nil {
		return proto.AgentUpdate{}, err
	}
	a := proto.AgentUpdate{Type: proto.TypeAgentUpdate, Version: version, File: "vps-agent-linux-" + arch, SHA256: fmt.Sprintf("%064d", 0)}
	if err = a.Validate(arch, current); err != nil {
		return a, err
	}
	path := filepath.Join(dir, a.File)
	info, err := os.Lstat(path)
	if err != nil {
		return a, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > proto.MaxAgentBinarySize {
		return a, fmt.Errorf("invalid agent release file")
	}
	f, err := os.Open(path)
	if err != nil {
		return a, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, proto.MaxAgentBinarySize+1))
	if err != nil {
		return a, err
	}
	if n != info.Size() {
		return a, fmt.Errorf("agent release file changed")
	}
	a.SHA256 = hex.EncodeToString(h.Sum(nil))
	return a, nil
}
