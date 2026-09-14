// Package corefiles stores immutable, checksum-verified Linux sing-box builds.
package corefiles

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const MaxFileSize int64 = 64 << 20
const CurrentKey = "core.current_version"
const PinnedVersion = "v1.14.0"

var (
	ErrInvalid     = errors.New("核心文件参数无效")
	ErrNotFound    = errors.New("核心版本或架构不存在")
	ErrConflict    = errors.New("核心版本状态冲突")
	ErrTooLarge    = errors.New("核心文件不能超过 64 MiB")
	versionPattern = regexp.MustCompile(`^v[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}$`)
)

type Settings interface {
	GetSetting(context.Context, string, any) (bool, error)
	SetSetting(context.Context, string, any) error
}
type Artifact struct {
	Arch       string `json:"arch"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	UploadedAt int64  `json:"uploaded_at"`
}
type Version struct {
	Version    string     `json:"version"`
	Arches     []string   `json:"arches"`
	Files      []Artifact `json:"files"`
	UploadedAt int64      `json:"uploaded_at"`
	Current    bool       `json:"current"`
}
type manifest struct {
	Files []Artifact `json:"files"`
}

type Store struct {
	root *os.Root
	db   Settings
	arch string
	mu   sync.RWMutex
	// validateBinary only reads the executable; uploads never execute code.
	validateBinary func(*os.File, string, string) error
}

func New(dir string, db Settings) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Store{root: root, db: db, arch: runtime.GOARCH, validateBinary: validateBinary}, nil
}
func (s *Store) Close() error    { return s.root.Close() }
func ValidVersion(v string) bool { return versionPattern.MatchString(v) }
func ValidArch(a string) bool    { return a == "amd64" || a == "arm64" }
func validate(v, a string) error {
	if !ValidVersion(v) || !ValidArch(a) {
		return fmt.Errorf("%w：版本格式为 v1.14.0，架构为 amd64/arm64", ErrInvalid)
	}
	return nil
}
func filename(v, a string) string { return v + "/sing-box-linux-" + a }
func (s *Store) current(ctx context.Context) (string, error) {
	var v string
	_, err := s.db.GetSetting(ctx, CurrentKey, &v)
	return v, err
}
func (s *Store) readManifest(v string) (manifest, error) {
	var m manifest
	data, err := s.root.ReadFile(v + "/manifest.json")
	if errors.Is(err, fs.ErrNotExist) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if !ValidArch(f.Arch) || !validHash(f.SHA256) || f.Size <= 0 || f.Size > MaxFileSize || seen[f.Arch] {
			return m, errors.New("核心文件清单损坏")
		}
		seen[f.Arch] = true
	}
	return m, nil
}
func validHash(sum string) bool {
	b, e := hex.DecodeString(sum)
	return e == nil && len(b) == sha256.Size
}

func (s *Store) List(ctx context.Context) ([]Version, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	current, err := s.current(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(s.root.FS(), ".")
	if err != nil {
		return nil, err
	}
	versions := []Version{}
	for _, entry := range entries {
		v := entry.Name()
		if !entry.IsDir() || !ValidVersion(v) {
			continue
		}
		m, err := s.readManifest(v)
		if errors.Is(err, ErrNotFound) {
			continue
		} // interrupted upload has no published manifest
		if err != nil {
			return nil, err
		}
		item := Version{Version: v, Arches: []string{}, Files: m.Files, Current: v == current}
		for _, f := range m.Files {
			item.Arches = append(item.Arches, f.Arch)
			if f.UploadedAt > item.UploadedAt {
				item.UploadedAt = f.UploadedAt
			}
		}
		versions = append(versions, item)
	}
	sort.Slice(versions, func(i, j int) bool {
		if versions[i].UploadedAt == versions[j].UploadedAt {
			return versions[i].Version > versions[j].Version
		}
		return versions[i].UploadedAt > versions[j].UploadedAt
	})
	return versions, nil
}

// Put publishes only after checksum and build metadata validation. Existing artifacts are immutable.
func (s *Store) Put(ctx context.Context, v, a string, reader io.Reader, checksum string) (Artifact, error) {
	var artifact Artifact
	if err := validate(v, a); err != nil {
		return artifact, err
	}
	checksum = strings.ToLower(checksum)
	if !validHash(checksum) {
		return artifact, fmt.Errorf("%w：SHA256 必须为 64 位十六进制", ErrInvalid)
	}
	// The upload is bounded and streamed to disk; no 64 MiB allocation per request.
	tmp, err := s.temp(".upload-")
	if err != nil {
		return artifact, err
	}
	tmpName := filepath.Base(tmp.Name())
	defer s.root.Remove(tmpName)
	defer tmp.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(&contextReader{ctx, reader}, MaxFileSize+1))
	if err != nil {
		return artifact, err
	}
	if n > MaxFileSize {
		return artifact, ErrTooLarge
	}
	if n == 0 || hex.EncodeToString(hash.Sum(nil)) != checksum {
		return artifact, fmt.Errorf("%w：文件 SHA256 不匹配或文件为空", ErrInvalid)
	}
	if err := s.validateBinary(tmp, v, a); err != nil {
		return artifact, fmt.Errorf("%w：%v", ErrInvalid, err)
	}
	if err := tmp.Chmod(0755); err != nil {
		return artifact, err
	}
	if err := tmp.Sync(); err != nil {
		return artifact, err
	}
	if err := tmp.Close(); err != nil {
		return artifact, err
	}
	if err := ctx.Err(); err != nil {
		return artifact, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.readManifest(v)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return artifact, err
	}
	for _, file := range m.Files {
		if file.Arch == a {
			if file.SHA256 == checksum {
				return file, nil
			}
			return artifact, fmt.Errorf("%w：同版本同架构不能覆盖，请使用新版本", ErrConflict)
		}
	}
	if err := s.root.MkdirAll(v, 0700); err != nil {
		return artifact, err
	}
	if err := s.root.Rename(tmpName, filename(v, a)); err != nil {
		return artifact, err
	}
	artifact = Artifact{Arch: a, SHA256: checksum, Size: n, UploadedAt: time.Now().Unix()}
	m.Files = append(m.Files, artifact)
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Arch < m.Files[j].Arch })
	// Manifest is the publication boundary. A failed publish leaves a replaceable orphan binary.
	if err := s.writeManifest(v, m); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
func (s *Store) temp(prefix string) (*os.File, error) {
	for range 10 {
		name := fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
		f, e := s.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(e, fs.ErrExist) {
			continue
		}
		return f, e
	}
	return nil, errors.New("无法创建核心临时文件")
}
func (s *Store) atomicWrite(name string, data []byte, mode fs.FileMode) error {
	tmp, e := s.temp(".metadata-")
	if e != nil {
		return e
	}
	n := filepath.Base(tmp.Name())
	defer s.root.Remove(n)
	defer tmp.Close()
	if _, e = tmp.Write(data); e != nil {
		return e
	}
	if e = tmp.Chmod(mode); e != nil {
		return e
	}
	if e = tmp.Sync(); e != nil {
		return e
	}
	if e = tmp.Close(); e != nil {
		return e
	}
	return s.root.Rename(n, name)
}
func (s *Store) writeManifest(v string, m manifest) error {
	var sums strings.Builder
	for _, f := range m.Files {
		fmt.Fprintf(&sums, "%s  sing-box-linux-%s\n", f.SHA256, f.Arch)
	}
	if e := s.atomicWrite(v+"/SHA256SUMS", []byte(sums.String()), 0600); e != nil {
		return e
	}
	data, e := json.Marshal(m)
	if e != nil {
		return e
	}
	return s.atomicWrite(v+"/manifest.json", data, 0600)
}
func (s *Store) open(v, a string) (*os.File, Artifact, error) {
	if e := validate(v, a); e != nil {
		return nil, Artifact{}, e
	}
	m, e := s.readManifest(v)
	if e != nil {
		return nil, Artifact{}, e
	}
	for _, f := range m.Files {
		if f.Arch == a {
			file, e := s.root.Open(filename(v, a))
			if errors.Is(e, fs.ErrNotExist) {
				e = ErrNotFound
			}
			if e != nil {
				return nil, f, e
			}
			info, e := file.Stat()
			if e != nil || !info.Mode().IsRegular() || info.Size() != f.Size {
				file.Close()
				return nil, f, errors.New("核心文件大小或类型与清单不一致")
			}
			return file, f, nil
		}
	}
	return nil, Artifact{}, ErrNotFound
}
func (s *Store) Open(v, a string) (*os.File, Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.open(v, a)
}

func (s *Store) prepareLocal(v string) (string, error) {
	if !ValidArch(s.arch) {
		return "", fmt.Errorf("%w：面板仅支持 amd64/arm64 核心", ErrInvalid)
	}
	for _, a := range []string{"amd64", "arm64"} {
		file, meta, e := s.open(v, a)
		if e != nil {
			return "", fmt.Errorf("%w：两个架构必须齐全：%v", ErrConflict, e)
		}
		h := sha256.New()
		_, e = io.Copy(h, file)
		file.Close()
		if e != nil {
			return "", e
		}
		if hex.EncodeToString(h.Sum(nil)) != meta.SHA256 {
			return "", fmt.Errorf("%w：%s 校验失败", ErrConflict, a)
		}
	}
	file, _, e := s.open(v, s.arch)
	if e != nil {
		return "", e
	}
	defer file.Close()
	tmp, e := s.temp(".current-")
	if e != nil {
		return "", e
	}
	n := filepath.Base(tmp.Name())
	ok := false
	defer func() {
		tmp.Close()
		if !ok {
			s.root.Remove(n)
		}
	}()
	if _, e = io.Copy(tmp, file); e != nil {
		return "", e
	}
	if e = tmp.Chmod(0755); e != nil {
		return "", e
	}
	if e = tmp.Sync(); e != nil {
		return "", e
	}
	if e = tmp.Close(); e != nil {
		return "", e
	}
	ok = true
	return n, nil
}

func (s *Store) SetCurrent(ctx context.Context, v string) error {
	if !ValidVersion(v) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, e := s.current(ctx)
	if e != nil {
		return e
	}
	tmp, e := s.prepareLocal(v)
	if e != nil {
		return e
	}
	defer s.root.Remove(tmp)
	// The DB is authoritative. Startup repairs current-local after interruption.
	if e = s.db.SetSetting(ctx, CurrentKey, v); e != nil {
		return e
	}
	if e = s.root.Rename(tmp, "current-local"); e != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return errors.Join(e, s.db.SetSetting(rollbackCtx, CurrentKey, old))
	}
	return nil
}

// Reconcile repairs the derived executable after DB restore or interrupted activation.
func (s *Store) Reconcile(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, e := s.current(ctx)
	if e != nil {
		return e
	}
	if v == "" {
		e = s.root.Remove("current-local")
		if errors.Is(e, fs.ErrNotExist) {
			return nil
		}
		return e
	}
	if !ValidVersion(v) {
		return ErrInvalid
	}
	tmp, e := s.prepareLocal(v)
	if e != nil {
		return e
	}
	defer s.root.Remove(tmp)
	return s.root.Rename(tmp, "current-local")
}
func (s *Store) Delete(ctx context.Context, v string) error {
	if !ValidVersion(v) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, e := s.current(ctx)
	if e != nil {
		return e
	}
	if current == v {
		return fmt.Errorf("%w：当前版本不能删除", ErrConflict)
	}
	if _, e := s.readManifest(v); e != nil {
		return e
	}
	// Rename unpublishes the whole version before removing its contents.
	trash := fmt.Sprintf(".deleted-%d", time.Now().UnixNano())
	if e := s.root.Rename(v, trash); e != nil {
		return e
	}
	return s.root.RemoveAll(trash)
}

func validateBinary(file *os.File, v, a string) error {
	info, e := buildinfo.Read(file)
	if e != nil {
		return errors.New("文件不是可识别的 Go 二进制")
	}
	if info.Path != "github.com/sagernet/sing-box/cmd/sing-box" {
		return errors.New("文件不是 sing-box")
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	if settings["GOOS"] != "linux" || settings["GOARCH"] != a || settings["CGO_ENABLED"] != "0" {
		return errors.New("需要对应架构、CGO_ENABLED=0 的 Linux 二进制")
	}
	tags := "," + strings.ReplaceAll(settings["-tags"], " ", ",") + ","
	for _, tag := range []string{"with_quic", "with_utls", "with_v2ray_api"} {
		if !strings.Contains(tags, ","+tag+",") {
			return fmt.Errorf("缺少构建标签 %s", tag)
		}
	}
	// -trimpath builds do not retain -ldflags; the release workflow verifies the
	// injected version by running the binary. The server never executes uploads.
	ef, e := elf.NewFile(file)
	if e != nil {
		return errors.New("需要 Linux ELF 文件")
	}
	if ef.Class != elf.ELFCLASS64 || (a == "amd64" && ef.Machine != elf.EM_X86_64) || (a == "arm64" && ef.Machine != elf.EM_AARCH64) {
		return errors.New("ELF 架构不匹配")
	}
	for _, p := range ef.Progs {
		if p.Type == elf.PT_INTERP {
			return errors.New("核心必须静态链接")
		}
	}
	return nil
}
