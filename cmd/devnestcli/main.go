// Command devnestcli drives the same engine as the desktop program from a
// terminal. It exists for the automated tests that install real software on
// a real Windows machine, and for people who would rather script it.
//
//	devnestcli list                          what can be installed, what is
//	devnestcli install mysql@8.0.46 port=3307 password=env:MYSQL_PW
//	devnestcli uninstall mysql [--data keep|backup|delete]
//	devnestcli use jdk@17.0.21               switch the default version
//	devnestcli scan [--json] [mysql jdk]     look for older installations
//	devnestcli clean --yes [mysql jdk]       remove what scan ticks by default
//	devnestcli services [--json]
//	devnestcli info mysql                    what was recorded about an install (JSON)
//	devnestcli service start|stop|restart <name>
//	devnestcli mirrors [all]                 show mirrors, or set all of them
//	devnestcli root [D:\DevNest]             show or change the install folder
//	devnestcli version
//
// A version may be shortened to its leading part: jdk@21 means the 21.x.y
// the catalogue carries.
//
// An option value written as env:NAME is read from that environment
// variable, which keeps passwords out of the command line.
//
// The exit status is 1 when anything failed.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"devnest/internal/core"
	"devnest/internal/platform"
	_ "devnest/internal/recipes"
	"devnest/internal/svchost"
)

func main() {
	// Windows services registered by the engine are hosted by a copy of the
	// program that installed them, so this program has to play that part too.
	if len(os.Args) >= 3 && os.Args[1] == "--service" {
		if err := svchost.Run(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	m := core.NewManager(platform.New())
	m.Emit = func(name string, data any) {
		if name != "log" {
			return
		}
		if ev, ok := data.(map[string]any); ok {
			fmt.Println(ev["line"])
		}
	}
	if err := run(m, os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "失败："+err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `用法：
  devnestcli list
  devnestcli install <软件>[@版本] [选项=值 ...]     值写成 env:变量名 表示从环境变量读（用来传密码）
  devnestcli uninstall <软件>[@版本] [--data keep|backup|delete]
  devnestcli use <软件>@<版本>
  devnestcli scan [--json] [软件 ...]
  devnestcli clean --yes [软件 ...]
  devnestcli services [--json]
  devnestcli info <软件>[@版本]
  devnestcli service start|stop|restart <服务名>
  devnestcli mirrors [all]
  devnestcli root [安装目录]
  devnestcli version

版本可以只写前几段：jdk@21 表示目录里的 21.x.y`)
}

func run(m *core.Manager, cmd string, args []string) error {
	switch cmd {
	case "list":
		return list(m)
	case "install":
		return install(m, args)
	case "uninstall":
		return uninstall(m, args)
	case "use":
		if len(args) != 1 || !strings.Contains(args[0], "@") {
			return errors.New("用法：devnestcli use <软件>@<版本>")
		}
		id, version, err := installedRef(m, args[0])
		if err != nil {
			return err
		}
		task, err := m.Activate(id, version)
		if err != nil {
			return err
		}
		return wait(m, task)
	case "scan":
		if len(args) > 0 && args[0] == "--json" {
			found, err := find(m, args[1:])
			if err != nil {
				return err
			}
			return printJSON(found)
		}
		_, err := scan(m, args)
		return err
	case "clean":
		return clean(m, args)
	case "services":
		return services(m, len(args) == 1 && args[0] == "--json")
	case "info":
		if len(args) != 1 {
			return errors.New("用法：devnestcli info <软件>[@版本]")
		}
		id, version, err := installedRef(m, args[0])
		if err != nil {
			return err
		}
		for _, inst := range m.VersionsOf(id) {
			if inst.Version == version {
				return printJSON(inst)
			}
		}
		return nil
	case "service":
		if len(args) != 2 {
			return errors.New("用法：devnestcli service start|stop|restart <服务名>")
		}
		task, err := m.ServiceAction(args[1], args[0])
		if err != nil {
			return err
		}
		return wait(m, task)
	case "mirrors":
		return mirrors(m, args)
	case "root":
		return root(m, args)
	case "version":
		fmt.Println(core.AppVersion)
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	}
	usage()
	return fmt.Errorf("不认识的命令 %q", cmd)
}

// splitRef splits "mysql@8.0.46" into its two halves; the version may be absent.
func splitRef(ref string) (id, version string) {
	id, version, _ = strings.Cut(ref, "@")
	return id, version
}

// sameSeries reports whether version is want itself or begins with it
// ("21" covers "21.0.13").
func sameSeries(want, version string) bool {
	return core.Dep{Version: want}.Matches(version)
}

// catalogRef resolves "jdk@21" against the catalogue. An absent version stays
// empty, which the engine reads as "the recommended one".
func catalogRef(ref string) (id, version string, err error) {
	id, version = splitRef(ref)
	pkg := core.Lookup(id)
	if pkg == nil {
		return "", "", fmt.Errorf("没有这个软件：%s（用 devnestcli list 查看全部）", id)
	}
	if version == "" {
		return id, "", nil
	}
	var have []string
	for _, r := range pkg.Releases {
		if sameSeries(version, r.Version) {
			return id, r.Version, nil
		}
		have = append(have, r.Version)
	}
	return "", "", fmt.Errorf("%s 没有 %s 这个版本，可选：%s", pkg.Name, version, strings.Join(have, "、"))
}

// installedRef resolves "jdk@21" against what is installed. The version may
// be left out when only one is installed.
func installedRef(m *core.Manager, ref string) (id, version string, err error) {
	id, version = splitRef(ref)
	installed := m.VersionsOf(id)
	if len(installed) == 0 {
		return "", "", fmt.Errorf("%s 没有安装", id)
	}
	var have []string
	for _, inst := range installed {
		if version != "" && sameSeries(version, inst.Version) {
			return id, inst.Version, nil
		}
		have = append(have, inst.Version)
	}
	switch {
	case version != "":
		return "", "", fmt.Errorf("%s 没有装 %s，已装的是：%s", id, version, strings.Join(have, "、"))
	case len(installed) > 1:
		return "", "", fmt.Errorf("%s 装了 %d 个版本（%s），请写明是哪一个，例如 %s@%s", id, len(installed), strings.Join(have, "、"), id, have[0])
	}
	return id, have[0], nil
}

// wait blocks until the queue is empty and reports the first of the given
// tasks that did not succeed. Tasks queued on their behalf (dependencies)
// are covered because a failed dependency fails the task that needed it.
func wait(m *core.Manager, ids ...int) error {
	m.WaitIdle()
	wanted := map[int]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	for _, t := range m.Tasks() {
		if wanted[t.ID] && t.State != "done" {
			return fmt.Errorf("%s：%s", t.Title, t.Error)
		}
	}
	return nil
}

func list(m *core.Manager) error {
	for _, p := range m.Catalog() {
		var versions []string
		for _, r := range p.Releases {
			v := r.Version
			switch {
			case r.Active:
				v += "（已安装，当前使用）"
			case r.Installed:
				v += "（已安装）"
			}
			versions = append(versions, v)
		}
		fmt.Printf("%-14s %-18s %s\n", p.ID, p.Name, strings.Join(versions, "  "))
	}
	return nil
}

func install(m *core.Manager, args []string) error {
	if len(args) == 0 {
		return errors.New("用法：devnestcli install <软件>[@版本] [选项=值 ...]")
	}
	id, version, err := catalogRef(args[0])
	if err != nil {
		return err
	}
	opts := map[string]string{}
	for _, a := range args[1:] {
		k, v, ok := strings.Cut(a, "=")
		if !ok || k == "" {
			return fmt.Errorf("选项要写成 名字=值，收到的是 %q", a)
		}
		if name, fromEnv := strings.CutPrefix(v, "env:"); fromEnv {
			if v, ok = os.LookupEnv(name); !ok {
				return fmt.Errorf("环境变量 %s 没有设置", name)
			}
		}
		opts[k] = v
	}
	tasks, err := m.Install(core.InstallReq{ID: id, Version: version, Options: opts})
	if err != nil {
		return err
	}
	return wait(m, tasks...)
}

func uninstall(m *core.Manager, args []string) error {
	data := "keep"
	var ref string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--data" && i+1 < len(args):
			i++
			data = args[i]
		case strings.HasPrefix(args[i], "--data="):
			data = strings.TrimPrefix(args[i], "--data=")
		case ref == "":
			ref = args[i]
		default:
			return fmt.Errorf("多余的参数 %q", args[i])
		}
	}
	if ref == "" {
		return errors.New("用法：devnestcli uninstall <软件>[@版本] [--data keep|backup|delete]")
	}
	if data != "keep" && data != "backup" && data != "delete" {
		return fmt.Errorf("--data 只能是 keep、backup 或 delete，收到的是 %q", data)
	}
	id, version, err := installedRef(m, ref)
	if err != nil {
		return err
	}
	task, err := m.Uninstall(id, version, data)
	if err != nil {
		return err
	}
	return wait(m, task)
}

// find runs a scan for the named packages (all of them when none is named).
func find(m *core.Manager, ids []string) ([]core.Finding, error) {
	for _, id := range ids {
		if core.Lookup(id) == nil {
			return nil, fmt.Errorf("没有这个软件：%s", id)
		}
	}
	found, err := m.Scan(ids)
	if found == nil {
		found = []core.Finding{}
	}
	return found, err
}

func scan(m *core.Manager, ids []string) ([]core.Finding, error) {
	found, err := find(m, ids)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		fmt.Println("没有发现旧的安装痕迹")
		return nil, nil
	}
	for _, f := range found {
		mark := "[ ]"
		switch {
		case f.Manual:
			mark = "[手动]"
		case f.Checked:
			mark = "[x]"
		}
		fmt.Printf("%s %-10s %-9s %s\n      %s\n", mark, f.PkgID, f.Kind, f.Title, f.Detail)
		if f.Note != "" {
			fmt.Printf("      注意：%s\n", f.Note)
		}
	}
	return found, nil
}

func clean(m *core.Manager, args []string) error {
	yes := false
	var ids []string
	for _, a := range args {
		if a == "--yes" || a == "-y" {
			yes = true
		} else {
			ids = append(ids, a)
		}
	}
	found, err := scan(m, ids)
	if err != nil {
		return err
	}
	var pick []string
	for _, f := range found {
		if f.Checked && !f.Manual {
			pick = append(pick, f.ID)
		}
	}
	if len(pick) == 0 {
		return nil
	}
	if !yes {
		return fmt.Errorf("上面打 [x] 的 %d 项会被清理。确认无误后加上 --yes 再执行", len(pick))
	}
	task, err := m.Clean(pick)
	if err != nil {
		return err
	}
	return wait(m, task)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func services(m *core.Manager, asJSON bool) error {
	rows := m.Services()
	if asJSON {
		if rows == nil {
			rows = []core.ServiceRow{}
		}
		return printJSON(rows)
	}
	for _, s := range rows {
		var ports []string
		for _, p := range s.Ports {
			state := "没有监听"
			if p.Listening {
				state = "监听中"
			}
			ports = append(ports, fmt.Sprintf("%d %s", p.Port, state))
		}
		fmt.Printf("%-16s %-10s %-8s %-9s PID %-6d %s\n", s.Name, s.PkgID, s.Version, s.State, s.PID, strings.Join(ports, "，"))
	}
	return nil
}

func mirrors(m *core.Manager, args []string) error {
	if len(args) == 1 && args[0] == "all" {
		if problems := m.SetAllMirrors(); len(problems) > 0 {
			return errors.New(strings.Join(problems, "；"))
		}
	} else if len(args) != 0 {
		return errors.New("用法：devnestcli mirrors [all]")
	}
	for _, it := range m.Mirrors() {
		cur := it.Current
		if cur == "" {
			cur = "官方源"
		}
		fmt.Printf("%-10s %-24s %s\n", it.ID, it.Name, cur)
	}
	return nil
}

func root(m *core.Manager, args []string) error {
	s := m.Settings()
	if len(args) == 1 {
		s.Root = args[0]
		if err := m.SaveSettings(s); err != nil {
			return err
		}
	} else if len(args) != 0 {
		return errors.New("用法：devnestcli root [安装目录]")
	}
	fmt.Println(m.Settings().Root)
	return nil
}
