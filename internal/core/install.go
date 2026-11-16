package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"devnest/internal/platform"
	"devnest/internal/svchost"
)

// ActiveOption is added to packages that support several versions.
var ActiveOption = Option{Key: "active", Label: "设为默认版本", Type: "bool", Default: "true",
	Help: "勾选后，命令行里直接用到的就是这个版本；以后可以在「版本切换」里改"}

type InstallReq struct {
	ID      string            `json:"id"`
	Version string            `json:"version"`
	Options map[string]string `json:"options"`
}

// pendingInstall finds a queued or running install task for a dependency.
func (m *Manager) pendingInstall(d Dep) (int, bool) {
	m.taskMu.Lock()
	defer m.taskMu.Unlock()
	for _, t := range m.tasks {
		if t.Kind != "install" || (t.State != "queued" && t.State != "running") {
			continue
		}
		id, ver, _ := strings.Cut(t.PkgID, "@")
		if id == d.ID && d.Matches(ver) {
			return t.ID, true
		}
	}
	return 0, false
}

// Install queues the install of a package and of any missing dependencies.
// It returns the queued task ids, dependencies first.
func (m *Manager) Install(req InstallReq) ([]int, error) {
	if !m.P.IsAdmin() {
		return nil, errNotAdmin
	}
	if err := m.CheckRoot(m.Root()); err != nil {
		return nil, err
	}
	return m.install(req, 0)
}

func (m *Manager) install(req InstallReq, depth int) ([]int, error) {
	pkg := Lookup(req.ID)
	if pkg == nil {
		return nil, fmt.Errorf("没有这个软件：%s", req.ID)
	}
	if depth > 8 {
		return nil, fmt.Errorf("%s 的依赖关系出现了循环", pkg.Name)
	}
	rel, ok := pkg.Release(req.Version)
	if !ok {
		return nil, fmt.Errorf("%s 没有 %s 这个版本", pkg.Name, req.Version)
	}
	opts, err := pkg.CheckOptions(req.Options)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", pkg.Name, err)
	}
	m.mu.Lock()
	installed := m.versionsOf(pkg.ID)
	m.mu.Unlock()
	for _, i := range installed {
		if i.Version == rel.Version {
			return nil, fmt.Errorf("%s %s 已经装过了", pkg.Name, rel.Version)
		}
		if !pkg.Multi {
			return nil, fmt.Errorf("已经装了 %s %s，要换版本请先卸载它", pkg.Name, i.Version)
		}
	}
	if _, busy := m.pendingInstall(Dep{ID: pkg.ID, Version: rel.Version}); busy {
		return nil, fmt.Errorf("%s %s 已经在安装队列里了", pkg.Name, rel.Version)
	}

	var ids, after []int
	for _, d := range pkg.Requires {
		if _, ok := m.Find(d); ok {
			continue
		}
		if id, ok := m.pendingInstall(d); ok {
			after = append(after, id)
			continue
		}
		dp := Lookup(d.ID)
		if dp == nil {
			return nil, fmt.Errorf("%s 依赖的 %s 不在软件库里", pkg.Name, d.ID)
		}
		ver := ""
		for _, r := range dp.Releases {
			if d.Matches(r.Version) {
				ver = r.Version
				break
			}
		}
		if ver == "" {
			return nil, fmt.Errorf("%s 需要 %s，但软件库里没有合适的版本", pkg.Name, d)
		}
		// A dependency never takes over as the default version of something the
		// user already has.
		depOpts := map[string]string{}
		if dp.Multi && len(m.VersionsOf(dp.ID)) > 0 {
			depOpts["active"] = "false"
		}
		sub, err := m.install(InstallReq{ID: d.ID, Version: ver, Options: depOpts}, depth+1)
		if err != nil {
			return nil, err
		}
		ids = append(ids, sub...)
		after = append(after, sub...)
	}

	t := m.Enqueue("install", "安装 "+pkg.Name+" "+rel.Version, pkg.ID+"@"+rel.Version, after,
		func(t *Task, ctx context.Context) error { return m.doInstall(t, ctx, pkg, rel, opts) })
	return append(ids, t.ID), nil
}

func (m *Manager) freeSpace(path string) (uint64, bool) {
	for _, d := range m.P.Drives() {
		if isUnder(path, d.Root) {
			return d.FreeBytes, true
		}
	}
	return 0, false
}

func (m *Manager) doInstall(t *Task, ctx context.Context, pkg *Package, rel Release, opts map[string]string) error {
	root := m.Root()
	for _, d := range []string{m.AppsDir(), m.DataRoot(), m.CacheDir(), filepath.Join(root, "logs")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("创建目录 %s 失败：%w", d, err)
		}
	}
	need := uint64(rel.Size)*4 + 200<<20
	if free, ok := m.freeSpace(root); ok && !m.P.Simulated() && free < need {
		return fmt.Errorf("磁盘空间不够：需要约 %d MB，%s 所在磁盘只剩 %d MB", need>>20, root, free>>20)
	}
	for _, o := range pkg.Options {
		if o.Type == "password" {
			t.Secret(opts[o.Key])
		}
	}

	inst := &Installed{ID: pkg.ID, Version: rel.Version, Dir: filepath.Join(m.AppsDir(), pkg.ID, rel.Version),
		Options: map[string]string{}, At: time.Now()}
	for _, o := range pkg.Options {
		if o.Type != "password" {
			inst.Options[o.Key] = opts[o.Key]
		}
	}
	c := &Ctx{Context: ctx, M: m, P: m.P, Pkg: pkg, Rel: rel, Opts: opts, Dir: inst.Dir, Inst: inst, task: t}
	if pkg.DataName != nil {
		c.DataDir = filepath.Join(m.DataRoot(), pkg.DataName(rel))
		c.DataExisted = dirHasFiles(c.DataDir)
		inst.DataDir = c.DataDir
	}
	for _, d := range pkg.Requires {
		if _, ok := m.Find(d); !ok {
			return fmt.Errorf("缺少依赖：%s", d)
		}
	}
	if dirHasFiles(inst.Dir) {
		t.Logf("清理上次没装完留下的目录 %s", inst.Dir)
		if err := removeAll(inst.Dir); err != nil {
			return fmt.Errorf("删不掉上次留下的目录 %s（可能有程序正在用它）：%w", inst.Dir, err)
		}
	}
	if err := os.MkdirAll(inst.Dir, 0o755); err != nil {
		return err
	}

	err := pkg.Install(c)
	if err == nil {
		for _, rel := range pkg.Probe {
			if !c.Exists(c.J(filepath.FromSlash(rel))) {
				err = fmt.Errorf("安装后没有找到 %s，安装包结构和预期不一样", rel)
				break
			}
		}
	}
	if err != nil {
		t.Logf("安装失败，撤销已经做过的改动")
		mode := "delete"
		if c.DataExisted {
			mode = "keep"
		}
		// The rollback must not be cut short by the cancellation that caused it.
		c.Context = context.Background()
		m.remove(c, mode, true)
		m.P.BroadcastEnv()
		return err
	}

	m.mu.Lock()
	others := m.versionsOf(pkg.ID)
	m.state[inst.Key()] = inst
	m.mu.Unlock()
	if pkg.HomeVar != "" && (len(others) == 0 || opts["active"] != "false") {
		if err := m.activate(c, inst); err != nil {
			return err
		}
	}
	m.mu.Lock()
	err = m.saveState()
	m.mu.Unlock()
	m.P.BroadcastEnv()
	m.changed("installed")
	if len(inst.Paths) > 0 || len(inst.Env) > 0 || pkg.HomeVar != "" {
		t.Logf("环境变量已更新：新开的命令行窗口才会生效")
	}
	return err
}

// homeEntries returns the PATH entries that follow a package's home variable.
func homeEntries(pkg *Package) []string {
	var out []string
	for _, sub := range pkg.PathSubs {
		e := "%" + pkg.HomeVar + "%"
		if sub != "" {
			e += `\` + sub
		}
		out = append(out, e)
	}
	return out
}

// activate points the package's home variable (JAVA_HOME ...) at one
// installed version. PATH refers to the variable, so only the variable
// changes when switching.
func (m *Manager) activate(c *Ctx, inst *Installed) error {
	pkg := c.Pkg
	if err := setSystemEnv(m.P, c.Logf, pkg.HomeVar, inst.Dir); err != nil {
		return err
	}
	entries := homeEntries(pkg)
	for i := len(entries) - 1; i >= 0; i-- {
		if err := platform.AddPath(m.P, platform.System, entries[i], true); err != nil {
			return err
		}
	}
	m.mu.Lock()
	for _, i := range m.versionsOf(pkg.ID) {
		i.Active = i.Version == inst.Version
	}
	inst.Active = true
	m.mu.Unlock()
	if pkg.OnActivate != nil {
		if err := pkg.OnActivate(c); err != nil {
			return err
		}
	}
	c.Logf("%s 的默认版本现在是 %s", pkg.Name, inst.Version)
	return nil
}

func (m *Manager) ctxFor(t *Task, ctx context.Context, inst *Installed) *Ctx {
	pkg := Lookup(inst.ID)
	if pkg == nil {
		pkg = &Package{ID: inst.ID, Name: inst.ID}
	}
	rel, ok := pkg.Release(inst.Version)
	if !ok || rel.Version != inst.Version {
		rel = Release{Version: inst.Version}
	}
	opts := map[string]string{}
	for k, v := range inst.Options {
		opts[k] = v
	}
	return &Ctx{Context: ctx, M: m, P: m.P, Pkg: pkg, Rel: rel, Opts: opts, Dir: inst.Dir, DataDir: inst.DataDir, Inst: inst, task: t}
}

// Activate queues a switch of the default version.
func (m *Manager) Activate(id, version string) (int, error) {
	pkg := Lookup(id)
	if pkg == nil || pkg.HomeVar == "" {
		return 0, fmt.Errorf("%s 不支持切换版本", id)
	}
	m.mu.Lock()
	inst := m.state[id+"@"+version]
	m.mu.Unlock()
	if inst == nil {
		return 0, fmt.Errorf("%s %s 没有安装", pkg.Name, version)
	}
	t := m.Enqueue("switch", "切换 "+pkg.Name+" 到 "+version, id+"@"+version, nil, func(t *Task, ctx context.Context) error {
		if err := m.activate(m.ctxFor(t, ctx, inst), inst); err != nil {
			return err
		}
		m.mu.Lock()
		err := m.saveState()
		m.mu.Unlock()
		m.P.BroadcastEnv()
		m.changed("installed")
		t.Logf("新开的命令行窗口才会生效")
		return err
	})
	return t.ID, nil
}

// dependents lists installed packages that would lose a dependency if this
// version went away.
func (m *Manager) dependents(inst *Installed) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, other := range m.state {
		pkg := Lookup(other.ID)
		if pkg == nil || other.Key() == inst.Key() {
			continue
		}
		for _, d := range pkg.Requires {
			if d.ID != inst.ID || !d.Matches(inst.Version) {
				continue
			}
			stillMet := false
			for _, v := range m.versionsOf(inst.ID) {
				if v.Version != inst.Version && d.Matches(v.Version) {
					stillMet = true
				}
			}
			if !stillMet {
				out = append(out, pkg.Name+" "+other.Version)
			}
		}
	}
	return out
}

// Uninstall queues the removal of an installed version. dataMode says what
// happens to its data folder: keep, backup (move to <root>/backup) or delete.
func (m *Manager) Uninstall(id, version, dataMode string) (int, error) {
	if !m.P.IsAdmin() {
		return 0, errNotAdmin
	}
	m.mu.Lock()
	inst := m.state[id+"@"+version]
	m.mu.Unlock()
	if inst == nil {
		return 0, errors.New("这个版本没有安装")
	}
	if deps := m.dependents(inst); len(deps) > 0 {
		return 0, fmt.Errorf("还有软件依赖它：%s。请先卸载这些软件", strings.Join(deps, "、"))
	}
	switch dataMode {
	case "keep", "backup", "delete":
	default:
		dataMode = "keep"
	}
	name := id
	if pkg := Lookup(id); pkg != nil {
		name = pkg.Name
	}
	t := m.Enqueue("uninstall", "卸载 "+name+" "+version, inst.Key(), nil, func(t *Task, ctx context.Context) error {
		c := m.ctxFor(t, ctx, inst)
		err := m.remove(c, dataMode, false)
		m.P.BroadcastEnv()
		m.changed("installed")
		return err
	})
	return t.ID, nil
}

// removeAll deletes a folder, retrying while Windows still holds file locks
// of a process that just exited.
func removeAll(path string) error {
	var err error
	for i := 0; i < 8; i++ {
		if err = os.RemoveAll(path); err == nil {
			return nil
		}
		time.Sleep(time.Duration(300*(i+1)) * time.Millisecond)
	}
	return err
}

// killUnder ends processes whose program file is inside dir.
func killUnder(p platform.Platform, dir string, logf func(string, ...any)) {
	procs, _ := p.Processes()
	self := os.Getpid()
	for _, pr := range procs {
		if pr.PID != self && pr.Path != "" && isUnder(pr.Path, dir) {
			logf("结束进程 %s（PID %d）", pr.Name, pr.PID)
			if err := p.KillProcess(pr.PID); err != nil {
				logf("结束进程失败：%v", err)
			}
		}
	}
}

// remove undoes an install. With rollback set it is cleaning up after a
// failed install, where problems are only logged.
func (m *Manager) remove(c *Ctx, dataMode string, rollback bool) error {
	inst, pkg, p := c.Inst, c.Pkg, m.P
	var firstErr error
	fail := func(err error) {
		if err != nil {
			c.Logf("注意：%v", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	c.task.Set(0.1, "停止服务")
	for _, name := range inst.Services {
		if _, ok := p.Service(name); !ok {
			continue
		}
		c.Logf("停止并删除服务 %s", name)
		fail(p.DeleteService(name))
	}
	for _, name := range inst.Wrapped {
		os.Remove(svchost.ConfigPath(m.ConfigDir(), name))
	}
	killUnder(p, inst.Dir, c.Logf)

	if pkg.Uninstall != nil {
		c.task.Set(0.3, "运行卸载程序")
		if err := pkg.Uninstall(c); err != nil {
			fail(fmt.Errorf("卸载程序报错：%w", err))
		}
	}

	c.task.Set(0.5, "清理环境变量")
	m.mu.Lock()
	var others []*Installed
	for _, v := range m.versionsOf(inst.ID) {
		if v.Version != inst.Version {
			others = append(others, v)
		}
	}
	m.mu.Unlock()
	keepEnv := map[string]bool{}
	keepPath := map[string]bool{}
	for _, o := range others {
		for _, e := range o.Env {
			keepEnv[strings.ToUpper(e)] = true
		}
		for _, e := range o.Paths {
			keepPath[platform.NormPath(e)] = true
		}
	}
	for _, name := range inst.Env {
		if !keepEnv[strings.ToUpper(name)] {
			c.Logf("删除系统环境变量 %s", name)
			fail(p.DeleteEnv(platform.System, name))
		}
	}
	for _, e := range inst.Paths {
		if !keepPath[platform.NormPath(e)] {
			c.Logf("从 PATH 移除 %s", e)
			fail(platform.RemovePathEntry(p, platform.System, e))
		}
	}
	if pkg.HomeVar != "" {
		cur, _ := p.GetEnv(platform.System, pkg.HomeVar)
		pointsHere := platform.NormPath(cur) == platform.NormPath(inst.Dir)
		switch {
		case len(others) > 0 && (pointsHere || inst.Active) && !rollback:
			next := others[0]
			fail(m.activate(m.ctxFor(c.task, c.Context, next), next))
		case len(others) > 0 && pointsHere:
			next := others[0]
			for _, o := range others {
				if o.Active {
					next = o
				}
			}
			fail(setSystemEnv(p, c.Logf, pkg.HomeVar, next.Dir))
		case len(others) == 0 && (pointsHere || cur == ""):
			if cur != "" {
				c.Logf("删除系统环境变量 %s", pkg.HomeVar)
				fail(p.DeleteEnv(platform.System, pkg.HomeVar))
			}
			for _, e := range homeEntries(pkg) {
				c.Logf("从 PATH 移除 %s", e)
				fail(platform.RemovePathEntry(p, platform.System, e))
			}
		}
	}

	for _, rule := range inst.Firewall {
		c.Logf("删除防火墙规则 %s", rule)
		p.Run(c, "netsh.exe", []string{"advfirewall", "firewall", "delete", "rule", "name=" + rule}, platform.RunOpts{Timeout: time.Minute})
	}

	c.task.Set(0.7, "删除文件")
	for _, lnk := range inst.Shortcuts {
		os.Remove(lnk)
	}
	for _, f := range inst.Files {
		if m.underRoot(f) || len(others) == 0 {
			os.RemoveAll(f)
		}
	}
	if inst.Dir != "" && m.underRoot(inst.Dir) {
		c.Logf("删除 %s", inst.Dir)
		if err := removeAll(inst.Dir); err != nil {
			fail(fmt.Errorf("删除 %s 失败（可能有程序正在使用）：%w", inst.Dir, err))
		}
		os.Remove(filepath.Dir(inst.Dir)) // drops <root>/apps/<id> when empty
	}

	if inst.DataDir != "" && len(others) == 0 && dirHasFiles(inst.DataDir) && m.underRoot(inst.DataDir) {
		switch dataMode {
		case "backup":
			dest := filepath.Join(m.BackupRoot(), time.Now().Format("20060102-150405"), filepath.Base(inst.DataDir))
			c.Logf("数据目录移到备份：%s", dest)
			fail(moveDir(inst.DataDir, dest))
		case "delete":
			c.Logf("删除数据目录 %s", inst.DataDir)
			fail(removeAll(inst.DataDir))
		default:
			c.Logf("数据目录保留在 %s", inst.DataDir)
		}
	}

	m.mu.Lock()
	delete(m.state, inst.Key())
	err := m.saveState()
	m.mu.Unlock()
	fail(err)
	if rollback {
		return nil
	}
	return firstErr
}

// ImportInstaller copies an installer the user downloaded by hand into the cache.
func (m *Manager) ImportInstaller(id, version, path string) error {
	pkg := Lookup(id)
	if pkg == nil {
		return fmt.Errorf("没有这个软件：%s", id)
	}
	rel, ok := pkg.Release(version)
	if !ok {
		return fmt.Errorf("%s 没有 %s 这个版本", pkg.Name, version)
	}
	return m.Fetcher().Import(context.Background(), rel, path)
}
