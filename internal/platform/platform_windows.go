//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

type winPlatform struct{}

// New returns the real Windows platform. Setting DEVNEST_SIM=1 switches to the
// simulated one, which is handy for trying the program without changing the
// machine.
func New() Platform {
	if os.Getenv("DEVNEST_SIM") == "1" {
		return NewSim(filepath.Join(os.TempDir(), "devnest-sim"))
	}
	return &winPlatform{}
}

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	iphlpapi                = windows.NewLazySystemDLL("iphlpapi.dll")
	procSendMessageTimeout  = user32.NewProc("SendMessageTimeoutW")
	procGetExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
)

const sysEnvKey = `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`

func (w *winPlatform) Simulated() bool { return false }

func (w *winPlatform) IsAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func (w *winPlatform) OSName() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return "Windows"
	}
	defer k.Close()
	name, _, _ := k.GetStringValue("ProductName")
	disp, _, _ := k.GetStringValue("DisplayVersion")
	build, _, _ := k.GetStringValue("CurrentBuild")
	// Windows 11 still reports "Windows 10" in ProductName.
	if n, _ := strconv.Atoi(build); n >= 22000 {
		name = strings.Replace(name, "Windows 10", "Windows 11", 1)
	}
	return strings.TrimSpace(fmt.Sprintf("%s %s (build %s)", name, disp, build))
}

// ---------- environment ----------

func envKey(scope Scope, access uint32) (registry.Key, error) {
	if scope == System {
		return registry.OpenKey(registry.LOCAL_MACHINE, sysEnvKey, access)
	}
	return registry.OpenKey(registry.CURRENT_USER, `Environment`, access)
}

func (w *winPlatform) GetEnv(scope Scope, name string) (string, bool) {
	k, err := envKey(scope, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		return "", false
	}
	return v, true
}

func (w *winPlatform) SetEnv(scope Scope, name, value string) error {
	isPath := strings.EqualFold(name, "path")
	if isPath && strings.TrimSpace(value) == "" {
		return errors.New("拒绝把 PATH 写成空值")
	}
	k, err := envKey(scope, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("打开环境变量注册表项失败：%w", err)
	}
	defer k.Close()
	if isPath || strings.Contains(value, "%") {
		err = k.SetExpandStringValue(name, value)
	} else {
		err = k.SetStringValue(name, value)
	}
	if err != nil {
		return fmt.Errorf("写入环境变量 %s 失败：%w", name, err)
	}
	if !isPath {
		os.Setenv(name, w.ExpandEnv(value))
	}
	return nil
}

func (w *winPlatform) DeleteEnv(scope Scope, name string) error {
	if strings.EqualFold(name, "path") {
		return errors.New("拒绝删除 PATH")
	}
	k, err := envKey(scope, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && err != registry.ErrNotExist {
		return err
	}
	os.Unsetenv(name)
	return nil
}

func (w *winPlatform) BroadcastEnv() {
	env, _ := windows.UTF16PtrFromString("Environment")
	var res uintptr
	// HWND_BROADCAST, WM_SETTINGCHANGE, SMTO_ABORTIFHUNG
	procSendMessageTimeout.Call(0xffff, 0x001A, 0, uintptr(unsafe.Pointer(env)), 0x0002, 3000, uintptr(unsafe.Pointer(&res)))
}

var envRef = regexp.MustCompile(`%([^%=]+)%`)

func (w *winPlatform) ExpandEnv(s string) string {
	for i := 0; i < 5 && strings.Contains(s, "%"); i++ {
		next := envRef.ReplaceAllStringFunc(s, func(m string) string {
			name := m[1 : len(m)-1]
			if strings.EqualFold(name, "path") {
				return m
			}
			if v, ok := w.GetEnv(System, name); ok && !strings.EqualFold(name, "username") {
				return v
			}
			if v, ok := w.GetEnv(User, name); ok {
				return v
			}
			if v, ok := os.LookupEnv(name); ok {
				return v
			}
			return m
		})
		if next == s {
			break
		}
		s = next
	}
	return s
}

// childEnv builds the environment for programs we start. The registry is read
// again every time because variables set during this session (JAVA_HOME, PATH)
// are not part of our own process environment.
func (w *winPlatform) childEnv(extra map[string]string) []string {
	type kv struct{ name, value string }
	m := map[string]kv{}
	for _, e := range os.Environ() {
		i := strings.IndexByte(e, '=')
		if i <= 0 {
			continue
		}
		m[strings.ToUpper(e[:i])] = kv{e[:i], e[i+1:]}
	}
	var paths []string
	for _, scope := range []Scope{System, User} {
		k, err := envKey(scope, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		names, _ := k.ReadValueNames(-1)
		for _, name := range names {
			v, _, err := k.GetStringValue(name)
			if err != nil {
				continue
			}
			switch {
			case strings.EqualFold(name, "path"):
				paths = append(paths, w.ExpandEnv(v))
			case scope == System && strings.EqualFold(name, "username"):
			default:
				m[strings.ToUpper(name)] = kv{name, w.ExpandEnv(v)}
			}
		}
		k.Close()
	}
	if len(paths) > 0 {
		m["PATH"] = kv{"Path", strings.Join(paths, ";")}
	}
	for name, v := range extra {
		if strings.EqualFold(name, "path+") {
			cur := m["PATH"]
			m["PATH"] = kv{"Path", v + ";" + cur.value}
			continue
		}
		m[strings.ToUpper(name)] = kv{name, v}
	}
	out := make([]string, 0, len(m))
	for _, e := range m {
		out = append(out, e.name+"="+e.value)
	}
	return out
}

// ---------- services ----------

func stateName(s svc.State) string {
	switch s {
	case svc.Running:
		return "running"
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "starting"
	case svc.StopPending:
		return "stopping"
	}
	return "unknown"
}

func startName(t uint32) string {
	switch t {
	case windows.SERVICE_AUTO_START:
		return "auto"
	case windows.SERVICE_DEMAND_START:
		return "manual"
	case windows.SERVICE_DISABLED:
		return "disabled"
	}
	return "other"
}

func (w *winPlatform) Services() ([]ServiceInfo, error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ENUMERATE_SERVICE|windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, err
	}
	defer windows.CloseServiceHandle(h)
	var needed, returned uint32
	var buf []byte
	for {
		var p *byte
		if len(buf) > 0 {
			p = &buf[0]
		}
		err = windows.EnumServicesStatusEx(h, windows.SC_ENUM_PROCESS_INFO, windows.SERVICE_WIN32,
			windows.SERVICE_STATE_ALL, p, uint32(len(buf)), &needed, &returned, nil, nil)
		if err == nil {
			break
		}
		if err != syscall.ERROR_MORE_DATA || needed <= uint32(len(buf)) {
			return nil, err
		}
		buf = make([]byte, needed)
	}
	if returned == 0 {
		return nil, nil
	}
	rows := unsafe.Slice((*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[0])), int(returned))
	out := make([]ServiceInfo, 0, len(rows))
	for _, r := range rows {
		info := ServiceInfo{
			Name:        windows.UTF16PtrToString(r.ServiceName),
			DisplayName: windows.UTF16PtrToString(r.DisplayName),
			State:       stateName(svc.State(r.ServiceStatusProcess.CurrentState)),
			PID:         int(r.ServiceStatusProcess.ProcessId),
			StartType:   "other",
		}
		if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+info.Name, registry.QUERY_VALUE); err == nil {
			info.BinaryPath, _, _ = k.GetStringValue("ImagePath")
			if st, _, err := k.GetIntegerValue("Start"); err == nil {
				info.StartType = startName(uint32(st))
			}
			k.Close()
		}
		out = append(out, info)
	}
	return out, nil
}

func (w *winPlatform) Service(name string) (ServiceInfo, bool) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return ServiceInfo{}, false
	}
	defer windows.CloseServiceHandle(scm)
	n, _ := windows.UTF16PtrFromString(name)
	h, err := windows.OpenService(scm, n, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return ServiceInfo{}, false
	}
	s := &mgr.Service{Name: name, Handle: h}
	defer s.Close()
	info := ServiceInfo{Name: name, State: "unknown", StartType: "other"}
	if st, err := s.Query(); err == nil {
		info.State = stateName(st.State)
		info.PID = int(st.ProcessId)
	}
	if c, err := s.Config(); err == nil {
		info.DisplayName = c.DisplayName
		info.BinaryPath = c.BinaryPathName
		info.StartType = startName(c.StartType)
	}
	return info, true
}

func withService(name string, fn func(s *mgr.Service) error) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务管理器失败（需要管理员权限）：%w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("打开服务 %s 失败：%w", name, err)
	}
	defer s.Close()
	return fn(s)
}

func waitState(s *mgr.Service, want svc.State, d time.Duration) error {
	deadline := time.Now().Add(d)
	for {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == want {
			return nil
		}
		if want == svc.Running && st.State == svc.Stopped {
			return fmt.Errorf("服务 %s 启动后又停止了（退出码 %d / %d）", s.Name, st.Win32ExitCode, st.ServiceSpecificExitCode)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待服务 %s 状态变化超时", s.Name)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func (w *winPlatform) CreateService(spec ServiceSpec) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务管理器失败（需要管理员权限）：%w", err)
	}
	defer m.Disconnect()
	cfg := mgr.Config{DisplayName: spec.DisplayName, Description: spec.Description, StartType: mgr.StartAutomatic}
	if spec.StartType == "manual" {
		cfg.StartType = mgr.StartManual
	}
	s, err := m.CreateService(spec.Name, spec.Exe, cfg, spec.Args...)
	if err != nil {
		return fmt.Errorf("创建服务 %s 失败：%w", spec.Name, err)
	}
	s.Close()
	return nil
}

func (w *winPlatform) StartService(name string) error {
	return withService(name, func(s *mgr.Service) error {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == svc.Running {
			return nil
		}
		if st.State == svc.StopPending {
			if err := waitState(s, svc.Stopped, 60*time.Second); err != nil {
				return err
			}
		}
		if st.State != svc.StartPending {
			if err := s.Start(); err != nil {
				return fmt.Errorf("启动服务 %s 失败：%w", name, err)
			}
		}
		return waitState(s, svc.Running, 120*time.Second)
	})
}

func stopOpened(s *mgr.Service) error {
	st, err := s.Query()
	if err != nil {
		return err
	}
	if st.State == svc.Stopped {
		return nil
	}
	if st.State != svc.StopPending {
		if _, err := s.Control(svc.Stop); err != nil {
			return fmt.Errorf("停止服务 %s 失败：%w", s.Name, err)
		}
	}
	return waitState(s, svc.Stopped, 120*time.Second)
}

func (w *winPlatform) StopService(name string) error {
	return withService(name, stopOpened)
}

func (w *winPlatform) DeleteService(name string) error {
	return withService(name, func(s *mgr.Service) error {
		if err := stopOpened(s); err != nil {
			// A service that will not stop is killed so its files can be removed.
			if st, qerr := s.Query(); qerr == nil && st.ProcessId != 0 {
				w.KillProcess(int(st.ProcessId))
			}
		}
		if err := s.Delete(); err != nil {
			return fmt.Errorf("删除服务 %s 失败：%w", name, err)
		}
		return nil
	})
}

func (w *winPlatform) SetServiceStart(name, startType string) error {
	return withService(name, func(s *mgr.Service) error {
		c, err := s.Config()
		if err != nil {
			return err
		}
		switch startType {
		case "auto":
			c.StartType = mgr.StartAutomatic
		case "manual":
			c.StartType = mgr.StartManual
		case "disabled":
			c.StartType = mgr.StartDisabled
		default:
			return fmt.Errorf("未知的启动类型 %q", startType)
		}
		return s.UpdateConfig(c)
	})
}

// ---------- registry ----------

func parseReg(path string) (registry.Key, string, error) {
	root, sub, _ := strings.Cut(path, `\`)
	switch strings.ToUpper(root) {
	case "HKLM", "HKEY_LOCAL_MACHINE":
		return registry.LOCAL_MACHINE, sub, nil
	case "HKCU", "HKEY_CURRENT_USER":
		return registry.CURRENT_USER, sub, nil
	case "HKCR", "HKEY_CLASSES_ROOT":
		return registry.CLASSES_ROOT, sub, nil
	case "HKU", "HKEY_USERS":
		return registry.USERS, sub, nil
	}
	return 0, "", fmt.Errorf("无法识别的注册表路径 %q", path)
}

func (w *winPlatform) UninstallEntries() ([]UninstallEntry, error) {
	roots := []struct {
		k            registry.Key
		prefix, path string
	}{
		{registry.LOCAL_MACHINE, "HKLM", `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.LOCAL_MACHINE, "HKLM", `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.CURRENT_USER, "HKCU", `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`},
	}
	var out []UninstallEntry
	for _, r := range roots {
		k, err := registry.OpenKey(r.k, r.path, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		names, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, name := range names {
			sk, err := registry.OpenKey(r.k, r.path+`\`+name, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			get := func(n string) string { v, _, _ := sk.GetStringValue(n); return v }
			e := UninstallEntry{
				KeyPath:              r.prefix + `\` + r.path + `\` + name,
				DisplayName:          get("DisplayName"),
				DisplayVersion:       get("DisplayVersion"),
				Publisher:            get("Publisher"),
				InstallLocation:      get("InstallLocation"),
				UninstallString:      get("UninstallString"),
				QuietUninstallString: get("QuietUninstallString"),
			}
			sk.Close()
			if e.DisplayName != "" {
				out = append(out, e)
			}
		}
	}
	return out, nil
}

func (w *winPlatform) RegKeyExists(path string) bool {
	root, sub, err := parseReg(path)
	if err != nil {
		return false
	}
	k, err := registry.OpenKey(root, sub, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

func (w *winPlatform) RegGetString(path, name string) (string, bool) {
	root, sub, err := parseReg(path)
	if err != nil {
		return "", false
	}
	k, err := registry.OpenKey(root, sub, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	return v, err == nil
}

func regDeleteTree(root registry.Key, sub string) error {
	k, err := registry.OpenKey(root, sub, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	names, _ := k.ReadSubKeyNames(-1)
	k.Close()
	for _, n := range names {
		if err := regDeleteTree(root, sub+`\`+n); err != nil {
			return err
		}
	}
	return registry.DeleteKey(root, sub)
}

func (w *winPlatform) RegDeleteTree(path string) error {
	root, sub, err := parseReg(path)
	if err != nil {
		return err
	}
	if strings.Count(strings.Trim(sub, `\`), `\`) < 1 {
		return fmt.Errorf("拒绝删除顶层注册表项 %s", path)
	}
	return regDeleteTree(root, strings.Trim(sub, `\`))
}

// ---------- processes ----------

func procPath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func (w *winPlatform) Processes() ([]ProcInfo, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	var out []ProcInfo
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		out = append(out, ProcInfo{
			PID:  int(pe.ProcessID),
			Name: windows.UTF16ToString(pe.ExeFile[:]),
			Path: procPath(pe.ProcessID),
		})
	}
	return out, nil
}

func (w *winPlatform) KillProcess(pid int) error {
	if pid <= 4 {
		return fmt.Errorf("拒绝结束系统进程 %d", pid)
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		return err
	}
	windows.WaitForSingleObject(h, 5000)
	return nil
}

// listeners returns port -> pid for listening TCP sockets of one address family.
func listeners(family uintptr, rowSize, portOff, pidOff int) map[int]int {
	const tcpTableOwnerPidListener = 3
	out := map[int]int{}
	var size uint32
	procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, family, tcpTableOwnerPidListener, 0)
	for try := 0; try < 4; try++ {
		buf := make([]byte, size+4096)
		size = uint32(len(buf))
		r, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, family, tcpTableOwnerPidListener, 0)
		if r == uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
			continue
		}
		if r != 0 {
			return out
		}
		n := int(uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24)
		for i := 0; i < n; i++ {
			off := 4 + i*rowSize
			if off+rowSize > len(buf) {
				break
			}
			port := int(buf[off+portOff])<<8 | int(buf[off+portOff+1])
			p := off + pidOff
			pid := int(uint32(buf[p]) | uint32(buf[p+1])<<8 | uint32(buf[p+2])<<16 | uint32(buf[p+3])<<24)
			if _, dup := out[port]; !dup {
				out[port] = pid
			}
		}
		return out
	}
	return out
}

func (w *winPlatform) PortOwner(port int) (ProcInfo, bool) {
	// MIB_TCPROW_OWNER_PID is 24 bytes, MIB_TCP6ROW_OWNER_PID is 56 bytes.
	pid, ok := listeners(windows.AF_INET, 24, 8, 20)[port]
	if !ok {
		pid, ok = listeners(windows.AF_INET6, 56, 20, 52)[port]
	}
	if !ok {
		return ProcInfo{}, false
	}
	info := ProcInfo{PID: pid, Path: procPath(uint32(pid))}
	if info.Path != "" {
		info.Name = filepath.Base(info.Path)
	} else if pid == 4 {
		info.Name = "System"
	}
	return info, true
}

// ---------- running programs ----------

func decodeConsole(b []byte) string {
	if len(b) == 0 || utf8.Valid(b) {
		return string(b)
	}
	const cpOEM = 1
	n, err := windows.MultiByteToWideChar(cpOEM, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil || n <= 0 {
		return strings.ToValidUTF8(string(b), "?")
	}
	u := make([]uint16, n)
	if _, err := windows.MultiByteToWideChar(cpOEM, 0, &b[0], int32(len(b)), &u[0], n); err != nil {
		return strings.ToValidUTF8(string(b), "?")
	}
	return windows.UTF16ToString(u)
}

func (w *winPlatform) comspec() string {
	return filepath.Join(w.Dir("systemroot"), "System32", "cmd.exe")
}

// command builds the process. Batch files go through cmd.exe with a command
// line we quote ourselves, because cmd.exe does not follow the quoting rules
// Go applies to normal programs.
func (w *winPlatform) command(ctx context.Context, exe string, args []string) *exec.Cmd {
	attr := &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	var cmd *exec.Cmd
	switch ext := strings.ToLower(filepath.Ext(exe)); ext {
	case ".bat", ".cmd":
		parts := []string{BatQuote(exe, true)}
		for _, a := range args {
			parts = append(parts, BatQuote(a, false))
		}
		cmd = exec.CommandContext(ctx, w.comspec())
		attr.CmdLine = `cmd.exe /d /s /c "` + strings.Join(parts, " ") + `"`
	default:
		cmd = exec.CommandContext(ctx, exe, args...)
	}
	cmd.SysProcAttr = attr
	return cmd
}

// NewCommand builds a hidden-window process with a fresh environment (read
// from the registry) plus extra variables. The service host uses it to start
// the program it looks after.
func NewCommand(ctx context.Context, exe string, args []string, env map[string]string) *exec.Cmd {
	w := &winPlatform{}
	cmd := w.command(ctx, exe, args)
	cmd.Env = w.childEnv(env)
	return cmd
}

func (w *winPlatform) finish(ctx context.Context, cmd *exec.Cmd, label string, opts RunOpts) (RunResult, error) {
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	cmd.Env = w.childEnv(opts.Env)
	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}
	lw := &lineWriter{onLine: opts.OnLine, decode: decodeConsole}
	cmd.Stdout, cmd.Stderr = lw, lw
	cmd.Cancel = func() error {
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		return kill.Run()
	}
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	lw.flush()
	res := RunResult{Output: lw.output()}
	// ErrWaitDelay: the program itself ended normally, but something it
	// started still holds the output pipe. Uninstallers that hand over to a
	// copy of themselves do this.
	if err == nil || errors.Is(err, exec.ErrWaitDelay) {
		return res, nil
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return res, fmt.Errorf("%s 运行超时", label)
		}
		return res, ctx.Err()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		if codeOK(res.ExitCode, opts.OKCodes) {
			return res, nil
		}
		return res, &ExitError{Exe: label, Code: res.ExitCode, Output: res.Output}
	}
	return res, fmt.Errorf("运行 %s 失败：%w", label, err)
}

func (w *winPlatform) Run(ctx context.Context, exe string, args []string, opts RunOpts) (RunResult, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	return w.finish(ctx, w.command(ctx, exe, args), filepath.Base(exe), opts)
}

func (w *winPlatform) RunShell(ctx context.Context, line string, opts RunOpts) (RunResult, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, w.comspec())
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
		CmdLine:       `cmd.exe /d /s /c "` + line + `"`,
	}
	return w.finish(ctx, cmd, "命令", opts)
}

func (w *winPlatform) Spawn(exe string, args []string, dir string, env map[string]string, logFile string) (int, error) {
	cmd := w.command(context.Background(), exe, args)
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
	cmd.Dir = dir
	cmd.Env = w.childEnv(env)
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		cmd.Stdout, cmd.Stderr = f, f
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	cmd.Process.Release()
	return pid, nil
}

// ---------- shell ----------

func (w *winPlatform) CreateShortcut(lnk, target, args, workDir, icon string) error {
	if err := os.MkdirAll(filepath.Dir(lnk), 0o755); err != nil {
		return err
	}
	script := `$s=(New-Object -ComObject WScript.Shell).CreateShortcut($env:DN_LNK);` +
		`$s.TargetPath=$env:DN_TARGET;` +
		`if($env:DN_ARGS){$s.Arguments=$env:DN_ARGS};` +
		`if($env:DN_DIR){$s.WorkingDirectory=$env:DN_DIR};` +
		`if($env:DN_ICON){$s.IconLocation=$env:DN_ICON};` +
		`$s.Save()`
	env := map[string]string{"DN_LNK": lnk, "DN_TARGET": target}
	for k, v := range map[string]string{"DN_ARGS": args, "DN_DIR": workDir, "DN_ICON": icon} {
		if v != "" {
			env[k] = v
		}
	}
	_, err := w.Run(context.Background(), "powershell.exe",
		[]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script},
		RunOpts{Env: env, Timeout: 30 * time.Second})
	return err
}

func (w *winPlatform) OpenPath(path string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}

func (w *winPlatform) Drives() []DriveInfo {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var out []DriveInfo
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		p, _ := windows.UTF16PtrFromString(root)
		if windows.GetDriveType(p) != windows.DRIVE_FIXED {
			continue
		}
		var free, total, totalFree uint64
		if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
			continue
		}
		out = append(out, DriveInfo{Root: root, FreeBytes: free, TotalBytes: total})
	}
	return out
}

func (w *winPlatform) Dir(kind string) string {
	var id *windows.KNOWNFOLDERID
	switch kind {
	case "programdata":
		id = windows.FOLDERID_ProgramData
	case "desktop":
		id = windows.FOLDERID_PublicDesktop
	case "userdesktop":
		id = windows.FOLDERID_Desktop
	case "startmenu":
		id = windows.FOLDERID_CommonPrograms
	case "home":
		id = windows.FOLDERID_Profile
	case "appdata":
		id = windows.FOLDERID_RoamingAppData
	case "localappdata":
		id = windows.FOLDERID_LocalAppData
	case "programfiles":
		id = windows.FOLDERID_ProgramFiles
	case "programfilesx86":
		id = windows.FOLDERID_ProgramFilesX86
	case "systemroot":
		id = windows.FOLDERID_Windows
	case "temp":
		return os.TempDir()
	default:
		return ""
	}
	p, err := windows.KnownFolderPath(id, 0)
	if err != nil {
		return ""
	}
	return p
}
