package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Sim is a pretend Windows machine kept in memory and in one sandbox folder.
// It lets the whole program run on macOS/Linux (and in tests) without
// touching the real system. It starts out "dirty": an old MySQL, an old JDK
// and a few PATH leftovers, so the cleanup screens have something to show.
type Sim struct {
	mu   sync.Mutex
	root string
	st   simState
	// Delay is how long a pretend command takes.
	Delay time.Duration
	// OnRun, when set, sees every pretend command (used by tests).
	OnRun func(exe string, args []string, opts RunOpts)
}

type simService struct {
	ServiceInfo
	Ports []int `json:"ports"`
}

type simState struct {
	SysEnv    map[string]string      `json:"sysEnv"`
	UserEnv   map[string]string      `json:"userEnv"`
	Services  map[string]*simService `json:"services"`
	Uninstall []UninstallEntry       `json:"uninstall"`
	RegKeys   map[string]bool        `json:"regKeys"`
	RegValues map[string]string      `json:"regValues"`
	Procs     []ProcInfo             `json:"procs"`
	NextPID   int                    `json:"nextPid"`
}

func NewSim(root string) *Sim {
	s := &Sim{root: root, Delay: 120 * time.Millisecond}
	os.MkdirAll(root, 0o755)
	if b, err := os.ReadFile(s.stateFile()); err == nil && json.Unmarshal(b, &s.st) == nil && s.st.SysEnv != nil {
		return s
	}
	s.seed()
	return s
}

func (s *Sim) stateFile() string { return filepath.Join(s.root, "sim-state.json") }

func (s *Sim) save() {
	if b, err := json.MarshalIndent(&s.st, "", "  "); err == nil {
		os.WriteFile(s.stateFile(), b, 0o644)
	}
}

func (s *Sim) c(parts ...string) string {
	return filepath.Join(append([]string{s.root, "C"}, parts...)...)
}

func (s *Sim) seed() {
	pf := s.c("Program Files")
	mysql := filepath.Join(pf, "MySQL", "MySQL Server 5.7")
	mysqlData := s.c("ProgramData", "MySQL", "MySQL Server 5.7")
	jdk := filepath.Join(pf, "Java", "jdk1.8.0_202")
	node := filepath.Join(pf, "nodejs")
	for path, content := range map[string]string{
		filepath.Join(mysql, "bin", "mysqld.exe"):          "sim",
		filepath.Join(mysqlData, "my.ini"):                 "[mysqld]\nport=3306\n",
		filepath.Join(mysqlData, "Data", "ibdata1"):        "sim data",
		filepath.Join(mysqlData, "Data", "shop", "db.opt"): "sim data",
		filepath.Join(jdk, "bin", "java.exe"):              "sim",
		filepath.Join(node, "node.exe"):                    "sim",
		s.c("Windows", "System32", "cmd.exe"):              "sim",
		s.c("Users", "dev", "Desktop", ".keep"):            "",
		s.c("Users", "Public", "Desktop", ".keep"):         "",
		filepath.Join(s.root, "D", ".keep"):                "",
	} {
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(content), 0o644)
	}
	sep := string(filepath.Separator)
	s.st = simState{
		SysEnv: map[string]string{
			"Path": strings.Join([]string{
				"%SystemRoot%" + sep + "system32", "%SystemRoot%",
				filepath.Join(mysql, "bin"), "%JAVA_HOME%" + sep + "bin", node,
			}, ";"),
			"JAVA_HOME":    jdk,
			"SystemRoot":   s.c("Windows"),
			"ProgramFiles": pf,
			"ProgramData":  s.c("ProgramData"),
			"TEMP":         s.c("Windows", "TEMP"),
		},
		UserEnv: map[string]string{
			"Path":        "%USERPROFILE%" + sep + "AppData" + sep + "Roaming" + sep + "npm",
			"USERPROFILE": s.c("Users", "dev"),
		},
		Services: map[string]*simService{
			"MySQL57": {
				ServiceInfo: ServiceInfo{
					Name: "MySQL57", DisplayName: "MySQL57", State: "running", StartType: "auto", PID: 4321,
					BinaryPath: `"` + filepath.Join(mysql, "bin", "mysqld.exe") + `" --defaults-file="` + filepath.Join(mysqlData, "my.ini") + `" MySQL57`,
				},
				Ports: []int{3306},
			},
			"Spooler": {ServiceInfo: ServiceInfo{
				Name: "Spooler", DisplayName: "Print Spooler", State: "running", StartType: "auto", PID: 2100,
				BinaryPath: s.c("Windows", "System32", "spoolsv.exe"),
			}},
		},
		Uninstall: []UninstallEntry{
			{KeyPath: `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{3D4C8E4E-57}`, DisplayName: "MySQL Server 5.7", DisplayVersion: "5.7.44", Publisher: "Oracle Corporation", InstallLocation: mysql, UninstallString: "MsiExec.exe /I{3D4C8E4E-57}"},
			{KeyPath: `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{64A3A4F4-B792-11D6-A78A-00B0D0180202}`, DisplayName: "Java SE Development Kit 8 Update 202 (64-bit)", DisplayVersion: "8.0.2020.8", Publisher: "Oracle Corporation", InstallLocation: jdk, UninstallString: "MsiExec.exe /X{64A3A4F4-B792-11D6-A78A-00B0D0180202}"},
			{KeyPath: `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{1F0A1B5C-16E0-4D2A-9B7C-0A1B2C3D4E5F}`, DisplayName: "Node.js", DisplayVersion: "16.20.2", Publisher: "Node.js Foundation", InstallLocation: node, UninstallString: "MsiExec.exe /X{1F0A1B5C-16E0-4D2A-9B7C-0A1B2C3D4E5F}"},
			{KeyPath: `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{7Zip}`, DisplayName: "7-Zip 23.01 (x64)", DisplayVersion: "23.01", Publisher: "Igor Pavlov"},
		},
		RegKeys:   map[string]bool{`HKLM\SOFTWARE\MySQL AB`: true, `HKLM\SOFTWARE\JavaSoft`: true},
		RegValues: map[string]string{},
		Procs: []ProcInfo{
			{PID: 4321, Name: "mysqld.exe", Path: filepath.Join(mysql, "bin", "mysqld.exe")},
			{PID: 880, Name: "explorer.exe", Path: s.c("Windows", "explorer.exe")},
		},
		NextPID: 6000,
	}
	s.save()
}

// Reset throws away the pretend machine and seeds a fresh one.
func (s *Sim) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	os.RemoveAll(s.root)
	os.MkdirAll(s.root, 0o755)
	s.seed()
}

func (s *Sim) Simulated() bool { return true }
func (s *Sim) IsAdmin() bool   { return true }
func (s *Sim) OSName() string  { return "模拟 Windows 11 专业版 24H2" }

func (s *Sim) env(scope Scope) map[string]string {
	if scope == System {
		return s.st.SysEnv
	}
	return s.st.UserEnv
}

func envLookup(m map[string]string, name string) (string, string, bool) {
	for k, v := range m {
		if strings.EqualFold(k, name) {
			return k, v, true
		}
	}
	return "", "", false
}

func (s *Sim) GetEnv(scope Scope, name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, v, ok := envLookup(s.env(scope), name)
	return v, ok
}

func (s *Sim) SetEnv(scope Scope, name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.EqualFold(name, "path") && strings.TrimSpace(value) == "" {
		return fmt.Errorf("拒绝把 PATH 写成空值")
	}
	m := s.env(scope)
	if k, _, ok := envLookup(m, name); ok {
		name = k
	}
	m[name] = value
	s.save()
	return nil
}

func (s *Sim) DeleteEnv(scope Scope, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.env(scope)
	if k, _, ok := envLookup(m, name); ok {
		delete(m, k)
		s.save()
	}
	return nil
}

func (s *Sim) BroadcastEnv() {}

var simEnvRef = regexp.MustCompile(`%([^%=]+)%`)

func (s *Sim) ExpandEnv(v string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := 0; i < 5 && strings.Contains(v, "%"); i++ {
		next := simEnvRef.ReplaceAllStringFunc(v, func(m string) string {
			name := m[1 : len(m)-1]
			if _, val, ok := envLookup(s.st.SysEnv, name); ok {
				return val
			}
			if _, val, ok := envLookup(s.st.UserEnv, name); ok {
				return val
			}
			switch strings.ToLower(name) {
			case "programfiles(x86)":
				return s.c("Program Files (x86)")
			case "appdata":
				return s.c("Users", "dev", "AppData", "Roaming")
			case "localappdata":
				return s.c("Users", "dev", "AppData", "Local")
			case "systemdrive":
				return s.c()
			}
			return m
		})
		if next == v {
			break
		}
		v = next
	}
	return v
}

func (s *Sim) Services() ([]ServiceInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ServiceInfo, 0, len(s.st.Services))
	for _, v := range s.st.Services {
		out = append(out, v.ServiceInfo)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Sim) find(name string) *simService {
	for k, v := range s.st.Services {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return nil
}

func (s *Sim) Service(name string) (ServiceInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.find(name); v != nil {
		return v.ServiceInfo, true
	}
	return ServiceInfo{}, false
}

func (s *Sim) CreateService(spec ServiceSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.find(spec.Name) != nil {
		return fmt.Errorf("服务 %s 已存在", spec.Name)
	}
	st := spec.StartType
	if st == "" {
		st = "auto"
	}
	bin := `"` + spec.Exe + `"`
	if len(spec.Args) > 0 {
		bin += " " + strings.Join(spec.Args, " ")
	}
	s.st.Services[spec.Name] = &simService{ServiceInfo: ServiceInfo{
		Name: spec.Name, DisplayName: spec.DisplayName, BinaryPath: bin, State: "stopped", StartType: st,
	}}
	s.save()
	return nil
}

// SimRegisterService records a service that a pretend installer command
// (for example `mysqld --install`) would have created.
func (s *Sim) SimRegisterService(name, binary string, ports ...int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.find(name); v != nil {
		v.Ports = ports
	} else {
		s.st.Services[name] = &simService{
			ServiceInfo: ServiceInfo{Name: name, DisplayName: name, BinaryPath: binary, State: "stopped", StartType: "auto"},
			Ports:       ports,
		}
	}
	s.save()
}

func (s *Sim) DeleteService(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.find(name)
	if v == nil {
		return fmt.Errorf("服务 %s 不存在", name)
	}
	s.dropProc(v.PID)
	delete(s.st.Services, v.Name)
	s.save()
	return nil
}

func (s *Sim) dropProc(pid int) {
	if pid == 0 {
		return
	}
	keep := s.st.Procs[:0]
	for _, p := range s.st.Procs {
		if p.PID != pid {
			keep = append(keep, p)
		}
	}
	s.st.Procs = keep
}

func firstExe(binary string) string {
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

func (s *Sim) StartService(name string) error {
	time.Sleep(s.Delay)
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.find(name)
	if v == nil {
		return fmt.Errorf("服务 %s 不存在", name)
	}
	if v.State == "running" {
		return nil
	}
	s.st.NextPID += 4
	v.State, v.PID = "running", s.st.NextPID
	exe := firstExe(v.BinaryPath)
	s.st.Procs = append(s.st.Procs, ProcInfo{PID: v.PID, Name: filepath.Base(exe), Path: exe})
	s.save()
	return nil
}

func (s *Sim) StopService(name string) error {
	time.Sleep(s.Delay)
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.find(name)
	if v == nil {
		return fmt.Errorf("服务 %s 不存在", name)
	}
	s.dropProc(v.PID)
	v.State, v.PID = "stopped", 0
	s.save()
	return nil
}

func (s *Sim) SetServiceStart(name, startType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.find(name)
	if v == nil {
		return fmt.Errorf("服务 %s 不存在", name)
	}
	v.StartType = startType
	s.save()
	return nil
}

func (s *Sim) UninstallEntries() ([]UninstallEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]UninstallEntry(nil), s.st.Uninstall...), nil
}

func (s *Sim) RegKeyExists(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.RegKeys[path] {
		return true
	}
	for _, e := range s.st.Uninstall {
		if strings.EqualFold(e.KeyPath, path) {
			return true
		}
	}
	return false
}

func (s *Sim) RegDeleteTree(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.st.RegKeys, path)
	keep := s.st.Uninstall[:0]
	for _, e := range s.st.Uninstall {
		if !strings.EqualFold(e.KeyPath, path) {
			keep = append(keep, e)
		}
	}
	s.st.Uninstall = keep
	s.save()
	return nil
}

func (s *Sim) RegGetString(path, name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.st.RegValues[path+`\\`+name]
	return v, ok
}

func (s *Sim) Processes() ([]ProcInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ProcInfo(nil), s.st.Procs...), nil
}

func (s *Sim) KillProcess(pid int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropProc(pid)
	for _, v := range s.st.Services {
		if v.PID == pid {
			v.State, v.PID = "stopped", 0
		}
	}
	s.save()
	return nil
}

func (s *Sim) PortOwner(port int) (ProcInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.st.Services {
		if v.State != "running" {
			continue
		}
		for _, p := range v.Ports {
			if p == port {
				exe := firstExe(v.BinaryPath)
				return ProcInfo{PID: v.PID, Name: filepath.Base(exe), Path: exe}, true
			}
		}
	}
	return ProcInfo{}, false
}

func (s *Sim) Run(ctx context.Context, exe string, args []string, opts RunOpts) (RunResult, error) {
	line := "[模拟执行] " + filepath.Base(exe) + " " + strings.Join(args, " ")
	if opts.OnLine != nil {
		opts.OnLine(strings.TrimSpace(line))
	}
	if s.OnRun != nil {
		s.OnRun(exe, args, opts)
	}
	select {
	case <-ctx.Done():
		return RunResult{}, ctx.Err()
	case <-time.After(s.Delay):
	}
	return RunResult{Output: line + "\n"}, nil
}

func (s *Sim) RunShell(ctx context.Context, line string, opts RunOpts) (RunResult, error) {
	// A pretend uninstaller removes its own "Apps & features" row.
	s.mu.Lock()
	keep := s.st.Uninstall[:0]
	for _, e := range s.st.Uninstall {
		u, q := strings.TrimSpace(e.UninstallString), strings.TrimSpace(e.QuietUninstallString)
		if (u != "" && strings.HasPrefix(line, u)) || (q != "" && strings.HasPrefix(line, q)) {
			continue
		}
		// MSI products are removed by product code, whatever the command.
		if code := e.KeyPath[strings.LastIndex(e.KeyPath, `\`)+1:]; strings.HasPrefix(code, "{") && strings.Contains(line, code) {
			continue
		}
		keep = append(keep, e)
	}
	s.st.Uninstall = keep
	s.save()
	s.mu.Unlock()
	return s.Run(ctx, "cmd.exe", []string{"/c", line}, opts)
}

func (s *Sim) Spawn(exe string, args []string, dir string, env map[string]string, logFile string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.NextPID += 4
	s.st.Procs = append(s.st.Procs, ProcInfo{PID: s.st.NextPID, Name: filepath.Base(exe), Path: exe})
	s.save()
	return s.st.NextPID, nil
}

func (s *Sim) CreateShortcut(lnk, target, args, workDir, icon string) error {
	if err := os.MkdirAll(filepath.Dir(lnk), 0o755); err != nil {
		return err
	}
	return os.WriteFile(lnk, []byte("shortcut -> "+target+" "+args+"\n"), 0o644)
}

func (s *Sim) OpenPath(path string) error { return nil }

func (s *Sim) Drives() []DriveInfo {
	sep := string(filepath.Separator)
	return []DriveInfo{
		{Root: s.c() + sep, FreeBytes: 86 << 30, TotalBytes: 237 << 30},
		{Root: filepath.Join(s.root, "D") + sep, FreeBytes: 412 << 30, TotalBytes: 931 << 30},
	}
}

func (s *Sim) Dir(kind string) string {
	switch kind {
	case "programdata":
		return s.c("ProgramData")
	case "desktop":
		return s.c("Users", "Public", "Desktop")
	case "userdesktop":
		return s.c("Users", "dev", "Desktop")
	case "startmenu":
		return s.c("ProgramData", "Microsoft", "Windows", "Start Menu", "Programs")
	case "home":
		return s.c("Users", "dev")
	case "appdata":
		return s.c("Users", "dev", "AppData", "Roaming")
	case "localappdata":
		return s.c("Users", "dev", "AppData", "Local")
	case "programfiles":
		return s.c("Program Files")
	case "programfilesx86":
		return s.c("Program Files (x86)")
	case "systemroot":
		return s.c("Windows")
	case "temp":
		return s.c("Windows", "TEMP")
	}
	return ""
}
