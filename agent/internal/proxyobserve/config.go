// Package proxyobserve discovers and reads external cores. It has no service,
// firewall, configuration-writing, reset-counter or uninstall operations.
package proxyobserve

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"vpsmon/proto"
)

const maxConfigBytes = 8 << 20

var errRead = errors.New("source_read_failed")
var errChanged = errors.New("source_changed_during_read")
var privateLabel = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[a-z0-9_+/=-]{48,}`)

func label(s string, limit int) string {
	s = privateLabel.ReplaceAllString(strings.ToValidUTF8(s, ""), "[已隐藏]")
	s = strings.Map(func(r rune) rune {
		if r < ' ' || r == 127 {
			return -1
		}
		return r
	}, s)
	if len(s) > limit {
		s = strings.ToValidUTF8(s[:limit], "")
	}
	return s
}

// readStable bounds allocation and rejects symlink replacement, FIFO/devices and
// in-place rewrites. Error text never contains a configuration fragment or path.
func readStable(path string, limit int64) ([]byte, error) {
	before, err := os.Stat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > limit {
		return nil, errRead
	}
	f, err := openSource(path)
	if err != nil {
		return nil, errRead
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, errChanged
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errRead
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return nil, errChanged
	}
	return b, nil
}

// jsonc lexes strings, escapes and comments before the standard JSON parser. It
// accepts trailing commas, but never removes slashes from a quoted URL/string.
func jsonc(raw []byte) ([]byte, error) {
	out := append([]byte(nil), raw...)
	quoted, escape := false, false
	for i := 0; i < len(out); i++ {
		c := out[i]
		if quoted {
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
			continue
		}
		if c != '/' || i+1 >= len(out) {
			continue
		}
		if out[i+1] == '/' {
			out[i] = ' '
			i++
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
			continue
		}
		if out[i+1] == '*' {
			out[i] = ' '
			out[i+1] = ' '
			i += 2
			for i+1 < len(out) && !(out[i] == '*' && out[i+1] == '/') {
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			if i+1 >= len(out) {
				return nil, errors.New("config_invalid_jsonc")
			}
			out[i] = ' '
			out[i+1] = ' '
			i++
		}
	}
	quoted = false
	escape = false
	for i, c := range out {
		if quoted {
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(out) && bytes.ContainsRune([]byte(" \r\n\t"), rune(out[j])) {
				j++
			}
			if j < len(out) && (out[j] == '}' || out[j] == ']') {
				out[i] = ' '
			}
		}
	}
	if !json.Valid(out) {
		return nil, errors.New("config_invalid_jsonc")
	}
	return out, nil
}

type parsedConfig struct {
	Inbounds     []proto.ObservedInbound
	RawTags      map[string]string
	StatsAddress string
	Issues       []string
}
type object = map[string]json.RawMessage

func obj(raw json.RawMessage) object { var v object; _ = json.Unmarshal(raw, &v); return v }
func str(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return string(n)
	}
	return ""
}
func boolean(raw json.RawMessage) bool { var v bool; _ = json.Unmarshal(raw, &v); return v }
func array(raw json.RawMessage) []json.RawMessage {
	var a []json.RawMessage
	_ = json.Unmarshal(raw, &a)
	return a
}
func count(raw json.RawMessage) *int {
	var a []json.RawMessage
	if json.Unmarshal(raw, &a) != nil || a == nil {
		return nil
	}
	n := len(a)
	return &n
}

func parseConfig(core string, raw []byte) (parsedConfig, error) {
	result := parsedConfig{Inbounds: []proto.ObservedInbound{}, RawTags: map[string]string{}}
	clean, err := jsonc(raw)
	if err != nil {
		return result, err
	}
	root := obj(clean)
	if root == nil {
		return result, errors.New("config_invalid_jsonc")
	}
	api := obj(root["api"])
	apiTag := str(api["tag"])
	if core == "sing-box" {
		result.StatsAddress = str(obj(obj(root["experimental"])["v2ray_api"])["listen"])
	}
	if core == "xray" {
		for _, service := range array(api["services"]) {
			if str(service) == "StatsService" {
				result.StatsAddress = str(api["listen"])
				break
			}
		}
	}
	for idx, rawInbound := range array(root["inbounds"]) {
		i := obj(rawInbound)
		if i == nil {
			continue
		}
		tag := str(i["tag"])
		if core == "xray" && apiTag != "" && tag == apiTag {
			for _, service := range array(api["services"]) {
				if str(service) == "StatsService" && result.StatsAddress == "" {
					result.StatsAddress = joinAddress(str(i["listen"]), str(i["port"]))
				}
			}
			continue // management API is not a user-facing proxy inbound
		}
		in := proto.ObservedInbound{ID: fmt.Sprintf("config-%d", idx), Tag: label(tag, 128), Listen: label(str(i["listen"]), 128), InConfig: true}
		if tag != "" {
			in.ID = "tag-" + shortHash(tag)
		}
		if _, exists := result.RawTags[in.ID]; exists {
			in.ID += fmt.Sprintf("-duplicate-%d", idx)
			result.Issues = append(result.Issues, "duplicate_inbound_tag")
		}
		result.RawTags[in.ID] = tag
		if core == "sing-box" {
			in.Protocol = label(str(i["type"]), 48)
			in.Port = label(str(i["listen_port"]), 128)
			in.Transport = label(str(obj(i["transport"])["type"]), 48)
			tls := obj(i["tls"])
			in.TLS = boolean(tls["enabled"])
			in.Reality = boolean(obj(tls["reality"])["enabled"])
			in.Users = count(i["users"])
		} else {
			in.Protocol = label(str(i["protocol"]), 48)
			in.Port = label(str(i["port"]), 128)
			stream := obj(i["streamSettings"])
			in.Transport = label(str(stream["network"]), 48)
			security := str(stream["security"])
			in.TLS = security == "tls" || security == "reality"
			in.Reality = security == "reality"
			in.Users = count(obj(i["settings"])["clients"])
		}
		if in.Protocol == "" {
			in.Protocol = "unknown"
		}
		result.Inbounds = append(result.Inbounds, in)
	}
	return result, nil
}

func joinAddress(host, port string) string {
	if _, err := strconv.Atoi(port); err != nil {
		return ""
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + port
	}
	return host + ":" + port
}
