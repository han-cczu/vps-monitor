package corectl

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func ParsePorts(r io.Reader, protocol string) ([]string, error) {
	s := bufio.NewScanner(r)
	found := map[string]bool{}
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) < 4 || f[0] == "sl" {
			continue
		}
		if protocol == "tcp" && f[3] != "0A" {
			continue
		}
		_, hexport, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		p, err := strconv.ParseUint(hexport, 16, 16)
		if err != nil || p == 0 {
			continue
		}
		found[fmt.Sprintf("%d/%s", p, protocol)] = true
	}
	out := make([]string, 0, len(found))
	for p := range found {
		out = append(out, p)
	}
	slices.Sort(out)
	return out, s.Err()
}
func Listening(dir string) ([]string, error) {
	found := map[string]bool{}
	for _, name := range []string{"tcp", "tcp6", "udp", "udp6"} {
		f, err := os.Open(filepath.Join(dir, name))
		if os.IsNotExist(err) && strings.HasSuffix(name, "6") {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read /proc/net/%s: %w", name, err)
		}
		ports, err := ParsePorts(f, strings.TrimSuffix(name, "6"))
		f.Close()
		if err != nil {
			return nil, err
		}
		for _, p := range ports {
			found[p] = true
		}
	}
	out := make([]string, 0, len(found))
	for p := range found {
		out = append(out, p)
	}
	slices.Sort(out)
	return out, nil
}
func Missing(expected, actual []string) []string {
	missing := []string{}
	for _, p := range expected {
		if !slices.Contains(actual, p) {
			missing = append(missing, p)
		}
	}
	return missing
}
