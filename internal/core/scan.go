package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"devnest/internal/platform"
)

// Finding is one trace of an existing installation found by a scan.
type Finding struct {
	ID      string `json:"id"`
	PkgID   string `json:"pkgId"`
	PkgName string `json:"pkgName"`
	// Kind: process | service | uninstall | dir | data | userdir | conf | env | path | reg
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Detail  string `json:"detail"`
	Checked bool   `json:"checked"`
	Size    int64  `json:"size"` // bytes for folders; -1 when too big to measure quickly
	Note    string `json:"note,omitempty"`
	// Manual marks something this program cannot remove by itself; it is
	// listed so the user knows about it, and Clean leaves it alone.
	Manual bool `json:"manual"`

	target string
	scope  platform.Scope
	cmd    string
	subs   []string
	key    string // the setting in the file a "conf" finding points at
}

var kindOrder = map[string]int{"process": 0, "service": 1, "uninstall": 2, "data": 3, "dir": 4, "userdir": 5, "conf": 6, "env": 7, "path": 8, "reg": 9}

var envScopes = []struct {
	s    platform.Scope
	name string
}{{platform.System, "系统"}, {platform.User, "用户"}}

// nativePath turns the backslashes of a Windows path into slashes when the
// program runs elsewhere (simulated mode).
func nativePath(path string) string {
	if filepath.Separator == '/' {
		return strings.ReplaceAll(path, `\`, "/")
	}
	return path
}

// matchAny tests s against the patterns. Patterns are written for Windows
// paths, so forward slashes (simulated mode) are turned into backslashes.
func matchAny(patterns []string, s string) bool {
	s = strings.ReplaceAll(s, "/", `\`)
	for _, p := range patterns {
		if ok, _ := regexp.MatchString(p, s); ok {
			return true
		}
	}
	return false
}

// expandPattern turns a folder pattern from a Detect rule into real paths.
func (m *Manager) expandPattern(pat string) []string {
	var roots []string
	if strings.HasPrefix(pat, "?:") {
		for _, d := range m.P.Drives() {
			roots = append(roots, strings.TrimRight(d.Root, `\/`)+pat[2:])
		}
	} else {
		roots = []string{m.P.ExpandEnv(pat)}
	}
	var out []string
	for _, r := range roots {
		if strings.Contains(r, "%") {
			continue // an unknown variable; nothing to look at
		}
		r = nativePath(r)
		if !strings.ContainsAny(r, "*?[") {
			if _, err := os.Stat(r); err == nil {
				out = append(out, r)
			}
			continue
		}
		if hits, err := filepath.Glob(r); err == nil {
			out = append(out, hits...)
		}
	}
	return out
}

// quickSize measures a folder but gives up after a short while.
func quickSize(path string) int64 {
	deadline := time.Now().Add(250 * time.Millisecond)
	var total int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if time.Now().After(deadline) {
			return errors.New("slow")
		}
		if err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return -1
	}
	return total
}

// protected reports whether deleting path would take a system folder, a
// user profile or our own folders with it.
func (m *Manager) protected(path string) bool {
	if strings.Trim(filepath.VolumeName(path)+string(filepath.Separator), `\/`) == strings.Trim(path, `\/`) {
		return true // a drive root
	}
	home := m.P.Dir("home")
	keep := []string{m.P.Dir("systemroot"), m.P.Dir("programfiles"), m.P.Dir("programfilesx86"), m.P.Dir("programdata"),
		home, m.P.Dir("appdata"), m.P.Dir("localappdata"), m.Root(), m.ConfigDir()}
	if home != "" {
		keep = append(keep, filepath.Dir(home))
	}
	for _, k := range keep {
		if k != "" && isUnder(k, path) {
			return true
		}
	}
	return isUnder(path, m.P.Dir("systemroot"))
}

var msiRx = regexp.MustCompile(`(?i)msiexec(?:\.exe)?"?\s+.*/[ix]\s*(\{[0-9a-f\-]+\})`)
var exeRx = regexp.MustCompile(`(?i)^\s*("[^"]+"|\S+\.exe)(.*)$`)

// quietUninstall works out a command that removes a program without showing
// its wizard. ok is false when no silent form is known.
func quietUninstall(e platform.UninstallEntry) (cmd string, ok bool) {
	if q := strings.TrimSpace(e.QuietUninstallString); q != "" {
		return q, true
	}
	u := strings.TrimSpace(e.UninstallString)
	if u == "" {
		return "", false
	}
	if m := msiRx.FindStringSubmatch(u); m != nil {
		return "msiexec.exe /x " + m[1] + " /qn /norestart", true
	}
	m := exeRx.FindStringSubmatch(u)
	if m == nil {
		return "", false
	}
	exe, rest := m[1], strings.TrimSpace(m[2])
	if !strings.HasPrefix(exe, `"`) {
		exe = `"` + exe + `"`
	}
	low := strings.ToLower(exe)
	switch {
	case strings.Contains(low, "unins0"): // Inno Setup
		return exe + " /VERYSILENT /SUPPRESSMSGBOXES /NORESTART", true
	case strings.Contains(low, "docker desktop installer"):
		return exe + " uninstall --quiet", true
	case strings.Contains(strings.ToLower(rest), "/uninstall"): // WiX bundles (Python, VC++)
		return exe + " " + rest + " /quiet /norestart", true
	case strings.Contains(low, "uninstall-postgresql"): // InstallBuilder (the EDB installer)
		return exe + " --mode unattended", true
	case strings.Contains(low, "uninst"): // NSIS
		return strings.TrimSpace(exe+" "+rest) + " /S", true
	}
	return "", false
}

// exeOf pulls the program path out of a service command line.
func exeOf(binary string) string {
	b := strings.TrimSpace(binary)
	if strings.HasPrefix(b, `"`) {
		if i := strings.Index(b[1:], `"`); i >= 0 {
			return b[1 : i+1]
		}
	}
	if i := strings.Index(strings.ToLower(b), ".exe"); i >= 0 {
		return b[:i+4]
	}
	return b
}

// homeOf guesses the install folder from a program path: the parent of a
// "bin" folder, otherwise the folder holding the program.
func homeOf(exe string) string {
	dir := filepath.Dir(exe)
	switch strings.ToLower(filepath.Base(dir)) {
	case "bin", "sbin":
		return filepath.Dir(dir)
	}
	return dir
}

type scanner struct {
	m        *Manager
	out      []Finding
	seen     map[string]bool
	services []platform.ServiceInfo
	entries  []platform.UninstallEntry
	procs    []platform.ProcInfo
	owned    map[string]bool
}

func (s *scanner) add(f Finding) {
	key := f.Kind + "|" + strings.ToLower(f.target) + "|" + fmt.Sprint(f.scope) + "|" + strings.ToLower(f.key)
	if f.Kind == "dir" || f.Kind == "data" || f.Kind == "userdir" {
		key = "dir|" + platform.NormPath(f.target)
	}
	if s.seen[key] {
		return
	}
	s.seen[key] = true
	f.ID = strconv.Itoa(len(s.out) + 1)
	s.out = append(s.out, f)
}

func (s *scanner) addDir(pkg *Package, kind, path, title string, checked bool) {
	m := s.m
	if path == "" || m.underRoot(path) || m.protected(path) {
		return
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return
	}
	f := Finding{PkgID: pkg.ID, PkgName: pkg.Name, Kind: kind, Title: title, Detail: path, Checked: checked, Size: quickSize(path), target: path}
	switch kind {
	case "data":
		f.Note = "数据目录：不会直接删除，而是移到备份文件夹"
	case "dir":
		for _, sub := range pkg.Detect.DataDirs {
			if !strings.ContainsAny(sub, `:%\/`) && dirHasFiles(filepath.Join(path, sub)) {
				f.subs = append(f.subs, sub)
			}
		}
		if len(f.subs) > 0 {
			f.Note = "里面的数据子目录（" + strings.Join(f.subs, "、") + "）会先移到备份文件夹"
		}
	case "userdir":
		f.Note = "个人配置或缓存，默认不勾选"
	}
	s.add(f)
}

func (s *scanner) scanPkg(pkg *Package) {
	m, p, d := s.m, s.m.P, pkg.Detect

	for _, pr := range s.procs {
		if pr.PID == os.Getpid() || m.underRoot(pr.Path) {
			continue
		}
		for _, name := range d.Procs {
			if strings.EqualFold(pr.Name, name) {
				s.add(Finding{PkgID: pkg.ID, PkgName: pkg.Name, Kind: "process", Checked: true,
					Title: fmt.Sprintf("正在运行的进程 %s（PID %d）", pr.Name, pr.PID), Detail: pr.Path, target: strconv.Itoa(pr.PID)})
			}
		}
	}

	for _, sv := range s.services {
		if s.owned[strings.ToLower(sv.Name)] {
			continue
		}
		exe := exeOf(sv.BinaryPath)
		if m.underRoot(exe) || isUnder(exe, m.ConfigDir()) {
			continue
		}
		if !matchAny(d.ServiceName, sv.Name) && !matchAny(d.ServiceBin, sv.BinaryPath) {
			continue
		}
		state := map[string]string{"running": "运行中", "stopped": "已停止"}[sv.State]
		if state == "" {
			state = sv.State
		}
		s.add(Finding{PkgID: pkg.ID, PkgName: pkg.Name, Kind: "service", Checked: true,
			Title: fmt.Sprintf("Windows 服务 %s（%s）", sv.Name, state), Detail: sv.BinaryPath, target: sv.Name})
		s.addDir(pkg, "dir", homeOf(exe), "服务程序所在目录", matchAny(d.PathRx, exe))
	}

	for _, e := range s.entries {
		if !matchAny(d.Uninstall, e.DisplayName) || m.underRoot(e.InstallLocation) {
			continue
		}
		f := Finding{PkgID: pkg.ID, PkgName: pkg.Name, Kind: "uninstall", Checked: true,
			Title: strings.TrimSpace("已安装程序：" + e.DisplayName + " " + e.DisplayVersion), target: e.KeyPath}
		if cmd, ok := quietUninstall(e); ok {
			f.cmd, f.Detail = cmd, cmd
		} else {
			f.Checked, f.Manual = false, true
			f.Detail = e.UninstallString
			f.Note = "这个程序没有已知的静默卸载方式，请在 Windows「应用和功能」里手动卸载"
		}
		s.add(f)
		if e.InstallLocation != "" {
			s.addDir(pkg, "dir", strings.Trim(e.InstallLocation, `"`), "安装目录（卸载后残留时删除）", true)
		}
	}

	for _, pat := range d.DataDirs {
		if !strings.ContainsAny(pat, `:%\/`) {
			continue // a sub-folder name, handled together with its parent
		}
		for _, path := range m.expandPattern(pat) {
			s.addDir(pkg, "data", path, "数据目录", true)
		}
	}
	for _, pat := range d.Dirs {
		for _, path := range m.expandPattern(pat) {
			s.addDir(pkg, "dir", path, "程序目录", true)
		}
	}
	for _, pat := range d.UserDirs {
		for _, path := range m.expandPattern(pat) {
			s.addDir(pkg, "userdir", path, "用户配置 / 缓存", false)
		}
	}

	folders := s.scanPrefixes(pkg)
	for _, sc := range envScopes {
		for _, name := range d.EnvVars {
			v, ok := p.GetEnv(sc.s, name)
			if !ok || m.underRoot(p.ExpandEnv(v)) {
				continue
			}
			s.add(Finding{PkgID: pkg.ID, PkgName: pkg.Name, Kind: "env", Checked: true,
				Title: sc.name + "环境变量 " + name, Detail: v, target: name, scope: sc.s})
			if fi, err := os.Stat(v); err == nil && fi.IsDir() {
				// M2 and the like point at the bin folder; what there is to
				// remove is the installation around it.
				home := v
				if strings.EqualFold(filepath.Base(home), "bin") {
					home = filepath.Dir(home)
				}
				s.addDir(pkg, "dir", home, name+" 指向的目录", matchAny(d.PathRx, v))
			}
		}
		for _, entry := range platform.PathEntries(p, sc.s) {
			full := p.ExpandEnv(entry)
			if m.underRoot(full) {
				continue
			}
			if !matchAny(d.PathRx, entry) && !matchAny(d.PathRx, full) && !folders[platform.NormPath(full)] {
				continue
			}
			s.add(Finding{PkgID: pkg.ID, PkgName: pkg.Name, Kind: "path", Checked: true,
				Title: sc.name + " PATH 里的条目", Detail: entry, target: entry, scope: sc.s})
		}
	}

	for _, key := range d.RegKeys {
		if p.RegKeyExists(key) {
			s.add(Finding{PkgID: pkg.ID, PkgName: pkg.Name, Kind: "reg", Checked: true, Title: "注册表项", Detail: key, target: key})
		}
	}
}

// scanPrefixes lists the settings in Detect.Prefixes that are set, with the
// folders they name, and returns those folders keyed by NormPath. A setting
// that names a folder of ours is left alone.
func (s *scanner) scanPrefixes(pkg *Package) map[string]bool {
	m, p := s.m, s.m.P
	folders := map[string]bool{}
	for _, st := range pkg.Detect.Prefixes {
		take := func(f Finding, value, title string) {
			dir := p.ExpandEnv(strings.TrimSpace(value))
			if dir == "" || m.underRoot(dir) {
				return
			}
			f.PkgID, f.PkgName, f.Checked = pkg.ID, pkg.Name, true
			s.add(f)
			folders[platform.NormPath(dir)] = true
			n := len(s.out)
			s.addDir(pkg, "dir", nativePath(dir), title, false)
			if len(s.out) > n {
				s.out[n].Note = strings.TrimSpace(s.out[n].Note + " " + st.Note)
			}
		}
		if st.File == "" {
			for _, sc := range envScopes {
				if v, ok := p.GetEnv(sc.s, st.Key); ok {
					take(Finding{Kind: "env", Title: sc.name + "环境变量 " + st.Key, Detail: v, target: st.Key, scope: sc.s},
						v, st.Key+" 指向的目录")
				}
			}
			continue
		}
		file := nativePath(p.ExpandEnv(st.File))
		if v := GetKeyLine(readText(file), st.Key); v != "" {
			name := filepath.Base(file)
			take(Finding{Kind: "conf", Title: name + " 里的 " + st.Key + " 设置", Detail: st.Key + "=" + v, target: file, key: st.Key},
				v, name+" 里 "+st.Key+" 指向的目录")
		}
	}
	return folders
}

// spareServiceFolders unticks folders that hold the program of a Windows
// service nothing on the list is going to remove. Such a folder is not a
// leftover: something else still runs from it (Docker Engine in
// C:\Program Files\Docker, when the rules only know Docker Desktop).
// A package with an uninstaller on the list is left alone, because the
// uninstaller usually takes its service with it; the cleaner checks again
// before it deletes.
func (s *scanner) spareServiceFolders() {
	listed, uninstalls := map[string]bool{}, map[string]bool{}
	for _, f := range s.out {
		switch {
		case f.Kind == "service":
			listed[strings.ToLower(f.target)] = true
		case f.Kind == "uninstall" && f.Checked:
			uninstalls[f.PkgID] = true
		}
	}
	for i := range s.out {
		f := &s.out[i]
		if !f.Checked || f.Kind != "dir" || uninstalls[f.PkgID] {
			continue
		}
		for _, sv := range s.services {
			if listed[strings.ToLower(sv.Name)] || !isUnder(exeOf(sv.BinaryPath), f.target) {
				continue
			}
			f.Checked = false
			f.Note = strings.TrimSpace(f.Note + " Windows 服务 " + sv.Name + " 的程序在这个目录里，而它不在清理清单中，所以默认不勾选")
			break
		}
	}
}

// spareLiveRegistrations unticks registry keys that describe an installation
// which is still on the disk and not on the list. Deleting such a key does
// not remove the software; it only hides it from the programs that look it
// up there, and leaves the machine in a state installers do not expect.
func (s *scanner) spareLiveRegistrations() {
	removing := func(path string) bool {
		for _, f := range s.out {
			if f.Checked && (f.Kind == "dir" || f.Kind == "data") && isUnder(path, f.target) {
				return true
			}
		}
		return false
	}
	for i := range s.out {
		f := &s.out[i]
		pkg := Lookup(f.PkgID)
		if f.Kind != "reg" || !f.Checked || pkg == nil || pkg.Detect.RegHomes == nil {
			continue
		}
		for _, home := range pkg.Detect.RegHomes(s.m.P, f.target) {
			if fi, err := os.Stat(home); err != nil || !fi.IsDir() || removing(home) {
				continue
			}
			f.Checked = false
			f.Note = strings.TrimSpace(f.Note + " 这里登记的安装（" + home + "）还在，而它不在清理清单中，所以默认不勾选")
			break
		}
	}
}

// serviceUnder names a Windows service whose program lies in the folder.
func serviceUnder(p platform.Platform, dir string) string {
	services, _ := p.Services()
	for _, sv := range services {
		if isUnder(exeOf(sv.BinaryPath), dir) {
			return sv.Name
		}
	}
	return ""
}

// waitKeyGone waits for an "installed programs" row to disappear.
// Uninstallers built with NSIS or InstallBuilder hand over to a copy of
// themselves and return at once, so their exit does not mean they are done.
func waitKeyGone(ctx context.Context, p platform.Platform, key string, most time.Duration) error {
	deadline := time.Now().Add(most)
	for p.RegKeyExists(key) {
		if time.Now().After(deadline) {
			return errors.New("卸载程序已经运行，但「已安装程序」里的条目还在；可能需要重启，或在 Windows「应用和功能」里手动卸载")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil
}

// Scan looks for existing installations of the given packages (all packages
// with detection rules when ids is empty). Nothing is changed.
func (m *Manager) Scan(ids []string) ([]Finding, error) {
	s := &scanner{m: m, seen: map[string]bool{}, owned: map[string]bool{}}
	var err error
	if s.services, err = m.P.Services(); err != nil {
		return nil, fmt.Errorf("读取服务列表失败：%w", err)
	}
	s.entries, _ = m.P.UninstallEntries()
	s.procs, _ = m.P.Processes()
	for _, inst := range m.Installed() {
		for _, name := range inst.Services {
			s.owned[strings.ToLower(name)] = true
		}
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, pkg := range Packages() {
		if len(want) > 0 && !want[pkg.ID] {
			continue
		}
		s.scanPkg(pkg)
	}
	s.spareServiceFolders()
	s.spareLiveRegistrations()
	pkgOrder := map[string]int{}
	for i, pkg := range Packages() {
		pkgOrder[pkg.ID] = i
	}
	sort.SliceStable(s.out, func(i, j int) bool {
		a, b := s.out[i], s.out[j]
		if a.PkgID != b.PkgID {
			return pkgOrder[a.PkgID] < pkgOrder[b.PkgID]
		}
		return kindOrder[a.Kind] < kindOrder[b.Kind]
	})
	m.mu.Lock()
	m.lastScan = map[string]Finding{}
	for _, f := range s.out {
		m.lastScan[f.ID] = f
	}
	m.mu.Unlock()
	if s.out == nil {
		s.out = []Finding{}
	}
	return s.out, nil
}

// backupEnv writes the current PATH values and the variables and settings
// about to be removed to a text file, so a wrong cleanup can be undone by hand.
func (m *Manager) backupEnv(items []Finding) (string, error) {
	var b strings.Builder
	b.WriteString("DevNest 环境变量备份 " + time.Now().Format("2006-01-02 15:04:05") + "\r\n\r\n")
	for _, sc := range envScopes {
		b.WriteString("[" + sc.name + " PATH]\r\n" + strings.Join(platform.PathEntries(m.P, sc.s), ";") + "\r\n\r\n")
	}
	for _, f := range items {
		switch f.Kind {
		case "env":
			b.WriteString(f.Title + " = " + f.Detail + "\r\n")
		case "conf":
			b.WriteString(f.target + " 里的 " + f.Detail + "\r\n")
		}
	}
	path := filepath.Join(m.ConfigDir(), "env-backup", "env-"+time.Now().Format("20060102-150405")+".txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(b.String()), 0o644)
}

// Clean queues the removal of the selected findings of the last scan.
func (m *Manager) Clean(ids []string) (int, error) {
	if !m.P.IsAdmin() {
		return 0, errNotAdmin
	}
	m.mu.Lock()
	var items []Finding
	for _, id := range ids {
		if f, ok := m.lastScan[id]; ok && !f.Manual {
			items = append(items, f)
		}
	}
	m.mu.Unlock()
	if len(items) == 0 {
		return 0, errors.New("没有选中要清理的项目，请先扫描并勾选")
	}
	sort.SliceStable(items, func(i, j int) bool { return kindOrder[items[i].Kind] < kindOrder[items[j].Kind] })
	t := m.Enqueue("clean", fmt.Sprintf("清理 %d 项旧安装痕迹", len(items)), "", nil, func(t *Task, ctx context.Context) error {
		return m.doClean(t, ctx, items)
	})
	return t.ID, nil
}

func (m *Manager) doClean(t *Task, ctx context.Context, items []Finding) error {
	p := m.P
	if bak, err := m.backupEnv(items); err == nil {
		t.Logf("环境变量已备份到 %s", bak)
	} else {
		return fmt.Errorf("备份环境变量失败，已取消清理：%w", err)
	}
	backupDir := filepath.Join(m.BackupRoot(), time.Now().Format("20060102-150405"))
	failed := 0
	backedUp := false
	for i, f := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		t.Set(float64(i)/float64(len(items)), f.Title)
		var err error
		switch f.Kind {
		case "process":
			pid, _ := strconv.Atoi(f.target)
			t.Logf("结束进程 %s", f.Title)
			err = p.KillProcess(pid)
		case "service":
			t.Logf("停止并删除服务 %s", f.target)
			if _, ok := p.Service(f.target); ok {
				err = p.DeleteService(f.target)
			}
		case "uninstall":
			if !p.RegKeyExists(f.target) {
				t.Logf("%s 已经不在了，跳过", f.Title)
				break
			}
			if strings.TrimSpace(f.cmd) == "" {
				err = errors.New("没有已知的静默卸载方式，请在 Windows「应用和功能」里手动卸载")
				break
			}
			t.Logf("静默卸载：%s", f.cmd)
			// 3010 = reboot needed, 1605 = already gone.
			var res platform.RunResult
			res, err = p.RunShell(ctx, f.cmd, platform.RunOpts{Timeout: 20 * time.Minute, OKCodes: []int{3010, 1605, 1614},
				OnLine: func(l string) { t.Logf("  %s", l) }})
			if err == nil && (res.ExitCode == 1605 || res.ExitCode == 1614) {
				// Windows Installer no longer knows the product; only the row is left.
				p.RegDeleteTree(f.target)
			}
			if err == nil {
				err = waitKeyGone(ctx, p, f.target, 3*time.Minute)
			}
		case "data":
			dest := filepath.Join(backupDir, f.PkgID, filepath.Base(f.target))
			t.Logf("数据目录 %s 移到备份 %s", f.target, dest)
			if err = moveDir(f.target, dest); err == nil {
				backedUp = true
			}
		case "dir", "userdir":
			if _, serr := os.Stat(f.target); serr != nil {
				break
			}
			if m.protected(f.target) || m.underRoot(f.target) {
				err = errors.New("受保护的目录，拒绝删除")
				break
			}
			// Services on the list are gone by now; one that is still here
			// is something else running from this folder.
			if name := serviceUnder(p, f.target); name != "" {
				err = fmt.Errorf("Windows 服务 %s 还在用这个目录，没有删除", name)
				break
			}
			killUnder(p, f.target, t.Logf)
			for _, sub := range f.subs {
				src := filepath.Join(f.target, sub)
				if !dirHasFiles(src) {
					continue
				}
				dest := filepath.Join(backupDir, f.PkgID, filepath.Base(f.target)+"-"+sub)
				t.Logf("数据子目录 %s 移到备份 %s", src, dest)
				if err = moveDir(src, dest); err != nil {
					break
				}
				backedUp = true
			}
			if err == nil {
				t.Logf("删除目录 %s", f.target)
				err = removeAll(f.target)
			}
		case "conf":
			content := readText(f.target)
			if GetKeyLine(content, f.key) == "" {
				break
			}
			t.Logf("从 %s 删除 %s", f.target, f.Detail)
			err = writeConfig(f.target, SetKeyLine(content, f.key, "=", ""))
		case "env":
			t.Logf("删除%s", f.Title)
			err = p.DeleteEnv(f.scope, f.target)
		case "path":
			t.Logf("从 PATH 移除 %s", f.target)
			err = platform.RemovePathEntry(p, f.scope, f.target)
		case "reg":
			t.Logf("删除注册表项 %s", f.target)
			err = p.RegDeleteTree(f.target)
		}
		if err != nil {
			failed++
			t.Logf("没有完成：%s：%v", f.Title, err)
		}
	}
	p.BroadcastEnv()
	m.changed("clean")
	if backedUp {
		t.Logf("数据备份在 %s，确认不需要后可以手动删除", backupDir)
	}
	if failed > 0 {
		return fmt.Errorf("%d 项没有清理成功，详见日志；其余 %d 项已完成", failed, len(items)-failed)
	}
	return nil
}
