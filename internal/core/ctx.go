package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"devnest/internal/platform"
	"devnest/internal/svchost"
)

// Ctx is what a recipe works with while installing or uninstalling one
// package. Everything that changes the machine goes through it, so the change
// is logged and recorded for a later uninstall.
type Ctx struct {
	context.Context
	M    *Manager
	P    platform.Platform
	Pkg  *Package
	Rel  Release
	Opts map[string]string
	// Dir is the install folder: <root>/apps/<id>/<version>.
	Dir string
	// DataDir holds data that must survive upgrades: <root>/data/<name>.
	DataDir string
	// DataExisted is true when DataDir already held files before this install.
	DataExisted bool
	Inst        *Installed

	task  *Task
	steps int
}

func (c *Ctx) Sim() bool { return c.P.Simulated() }

func (c *Ctx) Logf(format string, a ...any) { c.task.Logf(format, a...) }

// Step announces the next phase of the install and moves the progress bar.
func (c *Ctx) Step(name string) {
	c.steps++
	total := c.Pkg.Steps
	if total < c.steps {
		total = c.steps + 1
	}
	c.task.Set(0.5+0.48*float64(c.steps-1)/float64(total), name)
	c.Logf("—— %s", name)
}

func (c *Ctx) Opt(key string) string { return c.Opts[key] }

func (c *Ctx) OptInt(key string) int {
	n, _ := strconv.Atoi(c.Opts[key])
	return n
}

func (c *Ctx) OptBool(key string) bool { return c.Opts[key] == "true" }

// J joins path elements onto the install folder.
func (c *Ctx) J(parts ...string) string {
	return filepath.Join(append([]string{c.Dir}, parts...)...)
}

// Slash returns a path with forward slashes, which most config files want.
func Slash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// Fetch downloads this release (or takes it from the cache).
func (c *Ctx) Fetch() (string, error) { return c.FetchRelease(c.Rel) }

func (c *Ctx) FetchRelease(r Release) (string, error) {
	mb := func(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }
	return c.M.Fetcher().Get(c, r, func(done, total int64, src string) {
		p := -1.0
		msg := "下载 " + mb(done)
		if total > 0 {
			p = 0.5 * float64(done) / float64(total)
			msg = fmt.Sprintf("下载 %s / %s（%s）", mb(done), mb(total), src)
		}
		c.task.Set(p, msg)
	}, c.Logf)
}

// Unpack extracts an archive into the install folder, dropping a single
// top-level folder if the archive has one.
func (c *Ctx) Unpack(archive string) error { return c.UnpackTo(archive, c.Dir) }

func (c *Ctx) UnpackTo(archive, dest string) error {
	c.Logf("解压到 %s", dest)
	if c.Sim() {
		for _, rel := range c.Pkg.Probe {
			if err := c.Write(filepath.Join(dest, filepath.FromSlash(rel)), "sim\n"); err != nil {
				return err
			}
		}
		return os.MkdirAll(dest, 0o755)
	}
	low := strings.ToLower(archive)
	if strings.HasSuffix(low, ".tgz") || strings.HasSuffix(low, ".tar.gz") {
		return Untgz(c, archive, dest, true)
	}
	return Unzip(c, archive, dest, true, func(done, total int) {
		c.task.Set(-1, fmt.Sprintf("解压 %d / %d", done, total))
	})
}

func (c *Ctx) run(o platform.RunOpts, exe string, args []string) (string, error) {
	if o.Timeout == 0 {
		o.Timeout = 15 * time.Minute
	}
	c.Logf("> %s %s", filepath.Base(exe), strings.Join(args, " "))
	o.OnLine = func(l string) { c.Logf("  %s", l) }
	res, err := c.P.Run(c, exe, args, o)
	return res.Output, err
}

// Run executes a program and logs its output.
func (c *Ctx) Run(exe string, args ...string) (string, error) {
	return c.run(platform.RunOpts{}, exe, args)
}

func (c *Ctx) RunOpts(o platform.RunOpts, exe string, args ...string) (string, error) {
	return c.run(o, exe, args)
}

// RunShell executes a full command line (an uninstall string from the registry).
func (c *Ctx) RunShell(line string, okCodes ...int) error {
	c.Logf("> %s", line)
	_, err := c.P.RunShell(c, line, platform.RunOpts{
		Timeout: 20 * time.Minute, OKCodes: okCodes,
		OnLine: func(l string) { c.Logf("  %s", l) },
	})
	return err
}

// Installer runs a setup program and waits for it. In simulated mode the files
// it would have created are faked so the rest of the flow can continue.
func (c *Ctx) Installer(exe string, okCodes []int, args ...string) error {
	if _, err := c.run(platform.RunOpts{Timeout: 45 * time.Minute, OKCodes: okCodes}, exe, args); err != nil {
		return err
	}
	if c.Sim() {
		for _, rel := range c.Pkg.Probe {
			if err := c.Write(c.J(filepath.FromSlash(rel)), "sim\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

// OpenFirewall lets other computers reach a port through Windows Firewall.
// The rule is removed again when the package is uninstalled.
func (c *Ctx) OpenFirewall(port int) {
	name := fmt.Sprintf("DevNest %s %d", c.Pkg.ID, port)
	_, err := c.run(platform.RunOpts{Timeout: time.Minute}, "netsh.exe", []string{"advfirewall", "firewall", "add", "rule",
		"name=" + name, "dir=in", "action=allow", "protocol=TCP", fmt.Sprintf("localport=%d", port)})
	if err != nil {
		c.Logf("添加防火墙规则失败（局域网可能连不上，需要手动放行端口 %d）：%v", port, err)
		return
	}
	c.Inst.Firewall = addUnique(c.Inst.Firewall, name)
}

// Write creates a text file, making parent folders as needed.
func (c *Ctx) Write(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// Edit rewrites a text file through fn. A missing file is treated as empty.
func (c *Ctx) Edit(path string, fn func(old string) string) error {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return c.Write(path, fn(string(b)))
}

// Backup copies an existing file next to itself with a timestamp, so a config
// file we are about to replace is not lost.
func (c *Ctx) Backup(path string) {
	if _, err := os.Stat(path); err != nil {
		return
	}
	bak := path + ".bak-" + time.Now().Format("20060102-150405")
	if copyFile(path, bak) == nil {
		c.Logf("原文件已备份为 %s", bak)
	}
}

func (c *Ctx) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dirHasFiles(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}

func addUnique(list []string, v string) []string {
	for _, e := range list {
		if strings.EqualFold(e, v) {
			return list
		}
	}
	return append(list, v)
}

// setSystemEnv writes a machine-wide variable. A user-level variable of the
// same name would silently win for that user, so it is removed (its old value
// goes to the log).
func setSystemEnv(p platform.Platform, logf func(string, ...any), name, value string) error {
	if old, ok := p.GetEnv(platform.User, name); ok {
		logf("用户级环境变量 %s=%s 会盖住系统级设置，已删除", name, old)
		if err := p.DeleteEnv(platform.User, name); err != nil {
			logf("删除用户级 %s 失败：%v", name, err)
		}
	}
	if old, ok := p.GetEnv(platform.System, name); ok && old != value {
		logf("系统环境变量 %s：%s → %s", name, old, value)
	} else if !ok {
		logf("设置系统环境变量 %s=%s", name, value)
	}
	return p.SetEnv(platform.System, name, value)
}

// SetEnv sets a machine-wide environment variable and remembers it.
func (c *Ctx) SetEnv(name, value string) error {
	if err := setSystemEnv(c.P, c.Logf, name, value); err != nil {
		return err
	}
	c.Inst.Env = addUnique(c.Inst.Env, name)
	return nil
}

// AddPath puts a folder at the front of the machine-wide PATH and remembers it.
func (c *Ctx) AddPath(entry string) error {
	c.Logf("加入 PATH：%s", entry)
	if err := platform.AddPath(c.P, platform.System, entry, true); err != nil {
		return err
	}
	c.Inst.Paths = addUnique(c.Inst.Paths, entry)
	return nil
}

// NeedPort fails when something is already listening on the port.
func (c *Ctx) NeedPort(port int) error {
	if owner, used := c.P.PortOwner(port); used {
		who := owner.Name
		if who == "" {
			who = "未知程序"
		}
		return fmt.Errorf("端口 %d 已被 %s（PID %d）占用。请换一个端口，或先到「环境清理」清掉旧的安装", port, who, owner.PID)
	}
	return nil
}

// NeedNoService fails when a Windows service of that name already exists.
func (c *Ctx) NeedNoService(name string) error {
	if s, ok := c.P.Service(name); ok {
		return fmt.Errorf("已经存在名为 %s 的 Windows 服务（%s）。请先到「环境清理」清掉旧的安装，或换一个服务名", name, firstNonEmpty(s.BinaryPath, s.DisplayName))
	}
	return nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// WaitPort waits until something listens on the port.
func (c *Ctx) WaitPort(port int, d time.Duration) error {
	if c.Sim() {
		return nil
	}
	deadline := time.Now().Add(d)
	for {
		if _, ok := c.P.PortOwner(port); ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等了 %s，端口 %d 还是没有开始监听", d, port)
		}
		select {
		case <-c.Done():
			return c.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// waitServicePort waits for the port of a service that has just been started,
// and gives up at once when the service stops in the meantime.
func (c *Ctx) waitServicePort(name string, port int, d time.Duration) error {
	if c.Sim() {
		return nil
	}
	deadline := time.Now().Add(d)
	for {
		if _, ok := c.P.PortOwner(port); ok {
			return nil
		}
		if sv, ok := c.P.Service(name); ok && sv.State == "stopped" {
			return fmt.Errorf("服务 %s 启动后又停止了，端口 %d 没有开始监听", name, port)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等了 %s，端口 %d 还是没有开始监听", d, port)
		}
		select {
		case <-c.Done():
			return c.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

type simRegistrar interface {
	SimRegisterService(name, binary string, ports ...int)
}

// AddService records a Windows service created by the software's own tools
// (mysqld --install and the like), with the ports it listens on.
func (c *Ctx) AddService(name, exe string, ports ...PortRef) {
	c.Inst.Services = addUnique(c.Inst.Services, name)
	var nums []int
	for _, p := range ports {
		p.Service = name
		c.Inst.Ports = append(c.Inst.Ports, p)
		nums = append(nums, p.Port)
	}
	if sim, ok := c.P.(simRegistrar); ok {
		sim.SimRegisterService(name, exe, nums...)
	}
}

// WrapSpec describes a program that should run as a Windows service through
// our service host. It is for software that has no service support of its own
// (Redis, Nginx, Kafka, Nacos ...).
type WrapSpec struct {
	Name        string
	DisplayName string
	Exe         string
	Args        []string
	Dir         string
	Env         map[string]string
	StopExe     string
	StopArgs    []string
	StopTimeout int
	Ports       []PortRef
}

// WrapService registers the program as an auto-start Windows service.
func (c *Ctx) WrapService(w WrapSpec) error {
	if err := c.NeedNoService(w.Name); err != nil {
		return err
	}
	host, err := c.M.serviceHost()
	if err != nil {
		return err
	}
	logFile := filepath.Join(c.M.Root(), "logs", w.Name+".log")
	os.MkdirAll(filepath.Dir(logFile), 0o755)
	cfg := svchost.Config{
		Name: w.Name, Exe: w.Exe, Args: w.Args, Dir: firstNonEmpty(w.Dir, c.Dir), Env: w.Env,
		StopExe: w.StopExe, StopArgs: w.StopArgs, StopTimeoutSec: w.StopTimeout, LogFile: logFile,
	}
	cfgPath := svchost.ConfigPath(c.M.ConfigDir(), w.Name)
	if err := svchost.Save(cfgPath, cfg); err != nil {
		return err
	}
	c.Logf("注册 Windows 服务 %s（开机自启）", w.Name)
	err = c.P.CreateService(platform.ServiceSpec{
		Name: w.Name, DisplayName: firstNonEmpty(w.DisplayName, w.Name),
		Description: "由 DevNest 管理：" + c.Pkg.Name + " " + c.Rel.Version,
		Exe:         host, Args: []string{"--service", cfgPath}, StartType: "auto",
	})
	if err != nil {
		os.Remove(cfgPath)
		return err
	}
	c.Inst.Wrapped = addUnique(c.Inst.Wrapped, w.Name)
	c.Inst.Logs = addUnique(c.Inst.Logs, logFile)
	c.AddService(w.Name, w.Exe, w.Ports...)
	return nil
}

// StartService starts a service and, when a port is given, waits for it.
func (c *Ctx) StartService(name string, port int) error {
	c.Logf("启动服务 %s", name)
	if err := c.P.StartService(name); err != nil {
		c.showLogTails()
		return err
	}
	if port > 0 {
		if err := c.waitServicePort(name, port, 3*time.Minute); err != nil {
			c.showLogTails()
			return err
		}
		c.Logf("端口 %d 已开始监听", port)
	}
	return nil
}

// showLogTails copies the last lines of the software's own log files into the
// task log, so that a service which refuses to start says why.
func (c *Ctx) showLogTails() {
	for _, path := range c.Inst.Logs {
		lines := tailLines(path, 15)
		if len(lines) == 0 {
			continue
		}
		c.Logf("%s 的最后几行：", path)
		for _, l := range lines {
			c.Logf("  | %s", l)
		}
	}
}

// tailLines returns the last n non-empty lines of a text file.
func tailLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	const most = 8 << 10
	if fi, err := f.Stat(); err == nil && fi.Size() > most {
		f.Seek(-most, io.SeekEnd)
	}
	b, _ := io.ReadAll(f)
	var out []string
	for _, l := range strings.Split(strings.ToValidUTF8(string(b), "?"), "\n") {
		if l = strings.TrimRight(l, "\r \t"); l != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// Shortcut creates a Start menu shortcut and, if enabled in the settings, a
// desktop shortcut.
func (c *Ctx) Shortcut(name, target, args string) {
	dirs := []string{filepath.Join(c.P.Dir("startmenu"), "DevNest")}
	if c.M.Settings().DesktopShortcuts {
		dirs = append(dirs, c.P.Dir("desktop"))
	}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		lnk := filepath.Join(d, name+".lnk")
		if err := c.P.CreateShortcut(lnk, target, args, filepath.Dir(target), ""); err != nil {
			c.Logf("创建快捷方式失败（不影响使用）：%v", err)
			continue
		}
		c.Inst.Shortcuts = addUnique(c.Inst.Shortcuts, lnk)
	}
}

// Info adds a line to the "how to connect" card shown after installing.
func (c *Ctx) Info(label, value string) {
	c.Inst.Info = append(c.Inst.Info, KV{label, value})
}

// Also records an extra file or folder outside the install folder that
// uninstalling should remove.
func (c *Ctx) Also(path string) { c.Inst.Files = addUnique(c.Inst.Files, path) }

// LogFile registers a log file shown in the service panel.
func (c *Ctx) LogFile(path string) { c.Inst.Logs = addUnique(c.Inst.Logs, path) }

// Dep returns the installed package that satisfies a dependency.
func (c *Ctx) Dep(d Dep) (Installed, error) {
	if i, ok := c.M.Find(d); ok {
		return i, nil
	}
	return Installed{}, fmt.Errorf("缺少依赖：%s", d)
}

// Check runs a program just to confirm the install works; a failure is
// reported but only as a warning in simulated mode.
func (c *Ctx) Check(exe string, args ...string) error {
	if _, err := c.RunOpts(platform.RunOpts{Timeout: 2 * time.Minute}, exe, args...); err != nil {
		return fmt.Errorf("安装后自检没有通过：%w", err)
	}
	return nil
}

// serviceHost returns the path of the program used to host wrapped services.
// It is a copy of this program kept under ProgramData, so services keep
// working if the user moves or deletes the exe they downloaded.
func (m *Manager) serviceHost() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if m.P.Simulated() {
		return self, nil
	}
	dest := filepath.Join(m.ConfigDir(), "bin", "devnest-svc.exe")
	si, err := os.Stat(self)
	if err != nil {
		return "", err
	}
	if di, err := os.Stat(dest); err == nil && di.Size() == si.Size() {
		return dest, nil
	} else if err == nil {
		// An older copy may be in use by running services; keep it then.
		if copyFile(self, dest) != nil {
			return dest, nil
		}
		return dest, nil
	}
	if err := copyFile(self, dest); err != nil {
		return "", fmt.Errorf("准备服务宿主程序失败：%w", err)
	}
	return dest, nil
}

var errNotAdmin = errors.New("需要管理员权限。请右键程序，选「以管理员身份运行」")
