package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"devnest/internal/platform"
)

// The types below are what the UI receives.

type ReleaseView struct {
	Version   string `json:"version"`
	Label     string `json:"label"`
	Size      int64  `json:"size"`
	Cached    bool   `json:"cached"`
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
	Source    string `json:"source"`
}

type PackageView struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Category  string        `json:"category"`
	Desc      string        `json:"desc"`
	Homepage  string        `json:"homepage"`
	Notes     []string      `json:"notes"`
	Releases  []ReleaseView `json:"releases"`
	Options   []Option      `json:"options"`
	Requires  []string      `json:"requires"`
	Multi     bool          `json:"multi"`
	HomeVar   string        `json:"homeVar"`
	HasData   bool          `json:"hasData"`
	Installed []Installed   `json:"installed"`
}

func (m *Manager) Catalog() []PackageView {
	f := m.Fetcher()
	out := []PackageView{}
	for _, p := range Packages() {
		v := PackageView{ID: p.ID, Name: p.Name, Category: p.Category, Desc: p.Desc, Homepage: p.Homepage,
			Notes: p.Notes, Options: p.Options, Multi: p.Multi, HomeVar: p.HomeVar, HasData: p.DataName != nil,
			Installed: m.VersionsOf(p.ID), Requires: []string{}, Releases: []ReleaseView{}}
		if v.Notes == nil {
			v.Notes = []string{}
		}
		if v.Options == nil {
			v.Options = []Option{}
		}
		if v.Installed == nil {
			v.Installed = []Installed{}
		}
		for _, d := range p.Requires {
			v.Requires = append(v.Requires, d.String())
		}
		for _, r := range p.Releases {
			rv := ReleaseView{Version: r.Version, Label: firstNonEmpty(r.Label, r.Version), Size: r.Size, Cached: f.Cached(r)}
			if len(r.URLs) > 0 {
				rv.Source = hostOf(r.URLs[0])
			}
			for _, i := range v.Installed {
				if i.Version == r.Version {
					rv.Installed, rv.Active = true, i.Active
				}
			}
			v.Releases = append(v.Releases, rv)
		}
		out = append(out, v)
	}
	return out
}

type Overview struct {
	Version   string               `json:"version"`
	OS        string               `json:"os"`
	Admin     bool                 `json:"admin"`
	Simulated bool                 `json:"simulated"`
	Root      string               `json:"root"`
	RootError string               `json:"rootError"`
	Drives    []platform.DriveInfo `json:"drives"`
	Installed int                  `json:"installed"`
	Services  int                  `json:"services"`
	Running   int                  `json:"running"`
	CacheSize int64                `json:"cacheSize"`
	LogDir    string               `json:"logDir"`
}

func (m *Manager) Overview() Overview {
	o := Overview{Version: AppVersion, OS: m.P.OSName(), Admin: m.P.IsAdmin(), Simulated: m.P.Simulated(),
		Root: m.Root(), Drives: m.P.Drives(), LogDir: m.LogDir()}
	if err := m.CheckRoot(o.Root); err != nil {
		o.RootError = err.Error()
	}
	if o.Drives == nil {
		o.Drives = []platform.DriveInfo{}
	}
	for _, i := range m.Installed() {
		if p := Lookup(i.ID); p == nil || p.Category != "dep" {
			o.Installed++
		}
	}
	for _, s := range m.Services() {
		o.Services++
		if s.State == "running" {
			o.Running++
		}
	}
	o.CacheSize = dirSize(m.CacheDir())
	return o
}

// ClearCache deletes downloaded installers.
func (m *Manager) ClearCache() error {
	if m.Busy() {
		return fmt.Errorf("有任务正在进行，稍后再清理缓存")
	}
	entries, err := os.ReadDir(m.CacheDir())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		os.RemoveAll(filepath.Join(m.CacheDir(), e.Name()))
	}
	m.changed("cache")
	return nil
}

// Preset is a set of packages installed together with one click.
type Preset struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Desc  string   `json:"desc"`
	Items []string `json:"items"` // "id" or "id@versionPrefix"
}

var presets = []Preset{
	{"java", "Java 后端", "JDK 21、Maven、MySQL、Redis、Git、IntelliJ IDEA", []string{"jdk@21", "maven", "mysql", "redis", "git", "idea"}},
	{"web", "前端", "Node.js（含 pnpm、yarn）、Git、VS Code", []string{"node", "git", "vscode"}},
	{"python", "Python", "Python、Git、PyCharm", []string{"python", "git", "pycharm"}},
	{"go", "Go", "Go、Git、VS Code", []string{"go", "git", "vscode"}},
	{"db", "数据库", "MySQL、PostgreSQL、Redis、MongoDB、DBeaver", []string{"mysql", "postgresql", "redis", "mongodb", "dbeaver"}},
	{"micro", "微服务中间件", "JDK 21、Nacos、RabbitMQ、Kafka、Elasticsearch、Nginx", []string{"jdk@21", "nacos", "rabbitmq", "kafka", "elasticsearch", "nginx"}},
	{"graph", "图数据库", "JDK 21、Neo4j", []string{"jdk@21", "neo4j"}},
}

// Presets returns the bundles whose packages all exist in the catalogue.
func (m *Manager) Presets() []Preset {
	out := []Preset{}
	for _, p := range presets {
		ok := true
		for _, it := range p.Items {
			id, _, _ := strings.Cut(it, "@")
			ok = ok && Lookup(id) != nil
		}
		if ok {
			out = append(out, p)
		}
	}
	return out
}

// BatchResult reports what a multi-package install queued and what it left out.
type BatchResult struct {
	Tasks   []int    `json:"tasks"`
	Skipped []string `json:"skipped"`
	Errors  []string `json:"errors"`
}

// InstallMany queues several installs. Packages that are already installed
// are skipped; a package that cannot be queued is reported without stopping
// the others.
func (m *Manager) InstallMany(reqs []InstallReq) BatchResult {
	res := BatchResult{Tasks: []int{}, Skipped: []string{}, Errors: []string{}}
	for _, r := range reqs {
		pkg := Lookup(r.ID)
		if pkg == nil {
			res.Errors = append(res.Errors, "没有这个软件："+r.ID)
			continue
		}
		if have := m.VersionsOf(r.ID); len(have) > 0 {
			same := false
			for _, h := range have {
				same = same || h.Version == r.Version || r.Version == ""
			}
			if same || !pkg.Multi {
				res.Skipped = append(res.Skipped, pkg.Name+" "+have[0].Version+"（已安装）")
				continue
			}
		}
		if _, busy := m.pendingInstall(Dep{ID: r.ID, Version: r.Version}); busy {
			res.Skipped = append(res.Skipped, pkg.Name+"（已在队列中）")
			continue
		}
		ids, err := m.Install(r)
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		res.Tasks = append(res.Tasks, ids...)
	}
	return res
}
