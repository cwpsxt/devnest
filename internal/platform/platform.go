// Package platform hides everything that is specific to Windows (registry,
// services, environment variables, processes) behind one interface. On other
// systems a simulated implementation is used so the UI and the install flows
// can be developed and tested without a Windows machine.
package platform

import (
	"context"
	"strings"
	"time"
)

type Scope int

const (
	System Scope = iota
	User
)

type ServiceInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	BinaryPath  string `json:"binaryPath"`
	State       string `json:"state"`     // running | stopped | starting | stopping | unknown
	StartType   string `json:"startType"` // auto | manual | disabled | other
	PID         int    `json:"pid"`
}

type ServiceSpec struct {
	Name        string
	DisplayName string
	Description string
	Exe         string
	Args        []string
	StartType   string // auto | manual
}

// UninstallEntry is one row of "Apps & features" (the Uninstall registry keys).
type UninstallEntry struct {
	KeyPath              string `json:"keyPath"`
	DisplayName          string `json:"displayName"`
	DisplayVersion       string `json:"displayVersion"`
	Publisher            string `json:"publisher"`
	InstallLocation      string `json:"installLocation"`
	UninstallString      string `json:"uninstallString"`
	QuietUninstallString string `json:"quietUninstallString"`
}

type ProcInfo struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type DriveInfo struct {
	Root       string `json:"root"`
	FreeBytes  uint64 `json:"freeBytes"`
	TotalBytes uint64 `json:"totalBytes"`
}

type RunOpts struct {
	Dir string
	// Env is added on top of the current environment. The special key
	// "PATH+" prepends its value to PATH instead of replacing it.
	Env     map[string]string
	Timeout time.Duration
	Stdin   string
	OnLine  func(line string)
	// OKCodes lists exit codes that count as success. Empty means only 0.
	OKCodes []int
}

type RunResult struct {
	ExitCode int
	Output   string
}

type Platform interface {
	Simulated() bool
	IsAdmin() bool
	OSName() string

	GetEnv(scope Scope, name string) (string, bool)
	SetEnv(scope Scope, name, value string) error
	DeleteEnv(scope Scope, name string) error
	// BroadcastEnv tells running programs (Explorer, new terminals) that the
	// environment changed.
	BroadcastEnv()

	Services() ([]ServiceInfo, error)
	Service(name string) (ServiceInfo, bool)
	CreateService(spec ServiceSpec) error
	DeleteService(name string) error
	StartService(name string) error
	StopService(name string) error
	SetServiceStart(name, startType string) error

	UninstallEntries() ([]UninstallEntry, error)
	// Registry paths are written as `HKLM\SOFTWARE\...` or `HKCU\...`.
	RegKeyExists(path string) bool
	RegDeleteTree(path string) error
	RegGetString(path, name string) (string, bool)
	// RegSubKeys lists the names of the keys directly below a key.
	RegSubKeys(path string) []string

	Processes() ([]ProcInfo, error)
	KillProcess(pid int) error
	PortOwner(port int) (ProcInfo, bool)

	Run(ctx context.Context, exe string, args []string, opts RunOpts) (RunResult, error)
	// RunShell runs a complete command line through cmd.exe. It is meant for
	// uninstall strings read from the registry, which are already quoted.
	RunShell(ctx context.Context, line string, opts RunOpts) (RunResult, error)
	// Spawn starts a long-running background process that outlives this
	// program and returns its PID.
	Spawn(exe string, args []string, dir string, env map[string]string, logFile string) (int, error)

	CreateShortcut(lnk, target, args, workDir, icon string) error
	OpenPath(path string) error

	Drives() []DriveInfo
	// Dir returns a well-known folder: programdata, desktop, startmenu, home,
	// temp, appdata, localappdata, programfiles, programfilesx86, systemroot.
	Dir(kind string) string
	ExpandEnv(s string) string
}

// SplitPath splits a PATH value into its entries, dropping empty ones.
func SplitPath(v string) []string {
	var out []string
	for _, e := range strings.Split(v, ";") {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// NormPath makes two spellings of the same PATH entry compare equal.
func NormPath(e string) string {
	e = strings.TrimSpace(strings.Trim(strings.TrimSpace(e), `"`))
	e = strings.ReplaceAll(e, "/", `\`)
	for len(e) > 3 && strings.HasSuffix(e, `\`) {
		e = e[:len(e)-1]
	}
	return strings.ToLower(e)
}

func pathName(p Platform, scope Scope) string {
	for _, n := range []string{"Path", "PATH"} {
		if _, ok := p.GetEnv(scope, n); ok {
			return n
		}
	}
	return "Path"
}

func PathEntries(p Platform, scope Scope) []string {
	v, _ := p.GetEnv(scope, pathName(p, scope))
	return SplitPath(v)
}

// AddPath puts entry into PATH. With front set the entry goes first so it wins
// over other installations of the same tool. An existing copy is moved.
func AddPath(p Platform, scope Scope, entry string, front bool) error {
	want := NormPath(entry)
	var rest []string
	for _, e := range PathEntries(p, scope) {
		if NormPath(e) != want {
			rest = append(rest, e)
		}
	}
	if front {
		rest = append([]string{entry}, rest...)
	} else {
		rest = append(rest, entry)
	}
	return p.SetEnv(scope, pathName(p, scope), strings.Join(rest, ";"))
}

// RemovePath removes every PATH entry for which match returns true and
// reports the removed entries.
func RemovePath(p Platform, scope Scope, match func(entry string) bool) ([]string, error) {
	var keep, removed []string
	for _, e := range PathEntries(p, scope) {
		if match(e) {
			removed = append(removed, e)
		} else {
			keep = append(keep, e)
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	if len(keep) == 0 && scope == User {
		// A user PATH with nothing left is simply deleted. The system PATH is
		// never emptied; SetEnv refuses that.
		return removed, p.DeleteEnv(scope, pathName(p, scope))
	}
	return removed, p.SetEnv(scope, pathName(p, scope), strings.Join(keep, ";"))
}

func RemovePathEntry(p Platform, scope Scope, entry string) error {
	want := NormPath(entry)
	_, err := RemovePath(p, scope, func(e string) bool { return NormPath(e) == want })
	return err
}
