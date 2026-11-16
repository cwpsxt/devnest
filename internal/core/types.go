// Package core is the engine: the software catalogue, downloads, installs,
// uninstalls, version switching, the service panel, cleanup of old
// installations and mirror configuration.
package core

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"devnest/internal/platform"
)

// Release is one downloadable version of a package.
type Release struct {
	Version string   `json:"version"`
	Label   string   `json:"label,omitempty"`
	URLs    []string `json:"urls"`
	File    string   `json:"file"`
	Size    int64    `json:"size"`
	SHA256  string   `json:"sha256,omitempty"`
	SHA512  string   `json:"sha512,omitempty"`
	// SumURL points at a text file whose first hex word is the SHA-256 (64
	// characters) or SHA-512 (128 characters) of the download.
	SumURL string `json:"sumUrl,omitempty"`
	// Extra carries recipe-specific values.
	Extra map[string]string `json:"-"`
}

type Choice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Option is one setting the user can change before installing.
type Option struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"` // text | password | port | bool | select
	Default string   `json:"default"`
	Help    string   `json:"help,omitempty"`
	Choices []Choice `json:"choices,omitempty"`
	MinLen  int      `json:"minLen,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
	// PortName labels the port in the service panel ("MySQL", "HTTP").
	PortName string `json:"portName,omitempty"`
}

// Dep says a package needs another one. Version is a prefix ("21" matches
// 21.0.13); empty means any version.
type Dep struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
	// MinMajor accepts any installed version whose major number is at least
	// this value.
	MinMajor int `json:"minMajor,omitempty"`
}

// Detect describes how to recognise installations of a package that were not
// made by this program, for the cleanup page.
type Detect struct {
	ServiceName []string // regex on the service name
	ServiceBin  []string // regex on the service binary path
	Uninstall   []string // regex on the "Apps & features" display name
	EnvVars     []string // variable names
	PathRx      []string // regex on PATH entries (after expansion)
	Dirs        []string // folders; %VAR% and * allowed; a leading "?:" means every fixed drive
	DataDirs    []string // like Dirs, but holding user data: backed up instead of deleted
	UserDirs    []string // per-user config/cache folders: listed but not ticked by default
	RegKeys     []string
	// RegHomes lists the install folders a key in RegKeys vouches for. While
	// one of them is still on the disk and is not being removed, the key is
	// a live registration and not a leftover, and it is not ticked.
	RegHomes func(p platform.Platform, key string) []string
	// Prefixes are settings naming a folder that goes on PATH as it is:
	// npm's global folder, moved with npm_config_prefix or with `prefix` in
	// .npmrc. The setting is removed together with the PATH entries naming
	// that folder, so new global commands go where the new installation
	// puts them; the folder itself is listed but not ticked.
	Prefixes []Setting
	Procs    []string // process image names, lower case
}

// Setting is an environment variable when File is empty, otherwise a
// key=value line in File (%VAR% allowed).
type Setting struct {
	File string
	Key  string
	// Note is shown with the folder the setting names.
	Note string
}

type KV struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Package is one piece of software in the catalogue.
type Package struct {
	ID       string
	Name     string
	Category string // lang | db | mw | ide | tool | dep
	Desc     string
	Homepage string
	Releases []Release
	Options  []Option
	Requires []Dep
	Notes    []string

	// Multi packages can have several versions installed; one is active.
	Multi bool
	// HomeVar is the environment variable that points at the active version.
	HomeVar string
	// PathSubs are folders under the home folder that go on PATH ("" means
	// the home folder itself).
	PathSubs []string
	// Probe lists files that must exist after a successful install.
	Probe []string
	// DataName names the data folder under <root>/data; empty means none.
	DataName func(rel Release) string
	// Steps is how many c.Step calls Install makes (for the progress bar).
	Steps int

	Install    func(c *Ctx) error
	Uninstall  func(c *Ctx) error
	OnActivate func(c *Ctx) error
	Detect     Detect
}

// Installed records what an install did, so uninstalling can undo it.
type Installed struct {
	ID        string            `json:"id"`
	Version   string            `json:"version"`
	Dir       string            `json:"dir"`
	DataDir   string            `json:"dataDir,omitempty"`
	Options   map[string]string `json:"options,omitempty"`
	Services  []string          `json:"services,omitempty"`
	Wrapped   []string          `json:"wrapped,omitempty"`
	Ports     []PortRef         `json:"ports,omitempty"`
	Env       []string          `json:"env,omitempty"`
	Paths     []string          `json:"paths,omitempty"`
	Shortcuts []string          `json:"shortcuts,omitempty"`
	Files     []string          `json:"files,omitempty"`
	Firewall  []string          `json:"firewall,omitempty"`
	Info      []KV              `json:"info,omitempty"`
	Logs      []string          `json:"logs,omitempty"`
	Active    bool              `json:"active"`
	At        time.Time         `json:"at"`
}

type PortRef struct {
	Name    string `json:"name"`
	Port    int    `json:"port"`
	Service string `json:"service,omitempty"`
}

func (i *Installed) Key() string { return i.ID + "@" + i.Version }

var catalog = map[string]*Package{}
var catalogOrder []string

// Register adds a package to the catalogue. Recipes call it from init.
func Register(p *Package) {
	if _, dup := catalog[p.ID]; dup {
		panic("duplicate package " + p.ID)
	}
	catalog[p.ID] = p
	catalogOrder = append(catalogOrder, p.ID)
}

func Lookup(id string) *Package { return catalog[id] }

func Packages() []*Package {
	out := make([]*Package, 0, len(catalogOrder))
	for _, id := range catalogOrder {
		out = append(out, catalog[id])
	}
	return out
}

func (p *Package) Release(version string) (Release, bool) {
	for _, r := range p.Releases {
		if r.Version == version {
			return r, true
		}
	}
	if version == "" && len(p.Releases) > 0 {
		return p.Releases[0], true
	}
	return Release{}, false
}

func (p *Package) Option(key string) *Option {
	for i := range p.Options {
		if p.Options[i].Key == key {
			return &p.Options[i]
		}
	}
	return nil
}

// Major returns the leading number of a version ("21.0.13" -> 21, "v24.1" -> 24).
func Major(version string) int {
	v := strings.TrimLeft(version, "vV")
	end := 0
	for end < len(v) && v[end] >= '0' && v[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(v[:end])
	return n
}

// MinorKey returns "major.minor" ("8.0.46" -> "8.0").
func MinorKey(version string) string {
	parts := strings.SplitN(strings.TrimLeft(version, "vV"), ".", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return parts[0]
}

func (d Dep) Matches(version string) bool {
	if d.MinMajor > 0 {
		return Major(version) >= d.MinMajor
	}
	if d.Version == "" {
		return true
	}
	return version == d.Version || strings.HasPrefix(version, d.Version+".") || strings.HasPrefix(version, d.Version+"+")
}

func (d Dep) String() string {
	name := d.ID
	if p := Lookup(d.ID); p != nil {
		name = p.Name
	}
	switch {
	case d.MinMajor > 0:
		return fmt.Sprintf("%s %d 或更高", name, d.MinMajor)
	case d.Version != "":
		return name + " " + d.Version
	}
	return name
}

var passwordRx = regexp.MustCompile(`^[A-Za-z0-9_@#.+\-]+$`)

// CheckOptions fills in defaults and validates what the user typed.
func (p *Package) CheckOptions(in map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, o := range p.Options {
		v, ok := in[o.Key]
		if !ok {
			v = o.Default
		}
		v = strings.TrimSpace(v)
		switch o.Type {
		case "port":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("%s：端口必须是 1–65535 之间的数字", o.Label)
			}
		case "password":
			if len(v) < o.MinLen {
				return nil, fmt.Errorf("%s：至少 %d 位", o.Label, o.MinLen)
			}
			if len(v) > 32 {
				return nil, fmt.Errorf("%s：最多 32 位", o.Label)
			}
			if v != "" && !passwordRx.MatchString(v) {
				return nil, fmt.Errorf("%s：只能用字母、数字和 _ @ # . + - 这几个符号", o.Label)
			}
		case "bool":
			if v != "true" {
				v = "false"
			}
		case "select":
			found := false
			for _, c := range o.Choices {
				found = found || c.Value == v
			}
			if !found {
				return nil, fmt.Errorf("%s：无效的选项 %q", o.Label, v)
			}
		default:
			if o.Pattern != "" {
				if ok, _ := regexp.MatchString(o.Pattern, v); !ok {
					return nil, fmt.Errorf("%s：格式不对。%s", o.Label, o.Help)
				}
			}
		}
		out[o.Key] = v
	}
	// A service opened to the network must not keep a well-known password.
	if out["lan"] == "true" {
		for _, o := range p.Options {
			if o.Type != "password" {
				continue
			}
			if v := out[o.Key]; v == o.Default || len(v) < 8 {
				return nil, fmt.Errorf("%s：允许局域网访问时，密码不能用默认值，并且至少 8 位", o.Label)
			}
		}
	}
	return out, nil
}

// CompareVersions orders dotted versions numerically, newest first when used
// with sort.Slice(less = CompareVersions(a,b) > 0).
func CompareVersions(a, b string) int {
	split := func(s string) []int {
		var out []int
		for _, f := range regexp.MustCompile(`\d+`).FindAllString(s, -1) {
			n, _ := strconv.Atoi(f)
			out = append(out, n)
		}
		return out
	}
	x, y := split(a), split(b)
	for i := 0; i < len(x) || i < len(y); i++ {
		var p, q int
		if i < len(x) {
			p = x[i]
		}
		if i < len(y) {
			q = y[i]
		}
		if p != q {
			if p > q {
				return 1
			}
			return -1
		}
	}
	return 0
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
