package proto

import "encoding/json"

// CoreApply carries the entire compact-JSON configuration, hashed with SHA256.
type CoreApply struct {
	Type         string          `json:"type"`
	Revision     int64           `json:"revision"`
	Core         string          `json:"core"`
	Version      string          `json:"version"`
	ConfigSHA256 string          `json:"config_sha256"`
	Ports        []string        `json:"ports"`
	Config       json.RawMessage `json:"config"`
	ReqID        string          `json:"req_id,omitempty"`
}

type CoreAction struct {
	Type    string `json:"type"`
	Action  string `json:"action"`
	Version string `json:"version,omitempty"`
	File    string `json:"file,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	ReqID   string `json:"req_id,omitempty"`
}

type CoreLogsReq struct {
	Type  string `json:"type"`
	Kind  string `json:"kind"`
	Lines int    `json:"lines"`
	ReqID string `json:"req_id,omitempty"`
}

type CoreState struct {
	Type             string   `json:"type"`
	Core             string   `json:"core"`
	InstalledVersion string   `json:"installed_version"`
	Running          bool     `json:"running"`
	AppliedRevision  int64    `json:"applied_revision"`
	ConfigSHA256     string   `json:"config_sha256"`
	Listening        []string `json:"listening"`
	Firewall         string   `json:"firewall"`
	Error            *string  `json:"error"`
	ReqID            string   `json:"req_id,omitempty"`
}

type Counter struct {
	Name string `json:"name"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}

type CoreStats struct {
	Type     string    `json:"type"`
	TS       int64     `json:"ts"`
	Inbounds []Counter `json:"inbounds"`
	Users    []Counter `json:"users"`
}

type CoreLogs struct {
	Type  string `json:"type"`
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	ReqID string `json:"req_id,omitempty"`
}
