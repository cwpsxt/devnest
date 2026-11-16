package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PortRow is one port in the service panel.
type PortRow struct {
	Name      string `json:"name"`
	Port      int    `json:"port"`
	Listening bool   `json:"listening"`
	Owner     string `json:"owner"`
	OwnerPID  int    `json:"ownerPid"`
}

// ServiceRow is one line of the service panel.
type ServiceRow struct {
	Name      string    `json:"name"`
	PkgID     string    `json:"pkgId"`
	PkgName   string    `json:"pkgName"`
	Version   string    `json:"version"`
	State     string    `json:"state"` // running | stopped | starting | stopping | missing | unknown
	StartType string    `json:"startType"`
	PID       int       `json:"pid"`
	Ports     []PortRow `json:"ports"`
	Logs      []string  `json:"logs"`
	Dir       string    `json:"dir"`
}

func (m *Manager) portRow(name string, port int) PortRow {
	row := PortRow{Name: name, Port: port}
	if owner, ok := m.P.PortOwner(port); ok {
		row.Listening, row.Owner, row.OwnerPID = true, owner.Name, owner.PID
	}
	return row
}

// Services lists the Windows services that belong to installed packages.
func (m *Manager) Services() []ServiceRow {
	var rows []ServiceRow
	for _, inst := range m.Installed() {
		name := inst.ID
		if pkg := Lookup(inst.ID); pkg != nil {
			name = pkg.Name
		}
		for _, svcName := range inst.Services {
			row := ServiceRow{Name: svcName, PkgID: inst.ID, PkgName: name, Version: inst.Version, State: "missing", Dir: inst.Dir, Ports: []PortRow{}, Logs: []string{}}
			if s, ok := m.P.Service(svcName); ok {
				row.State, row.StartType, row.PID = s.State, s.StartType, s.PID
			}
			for _, p := range inst.Ports {
				if p.Service == svcName || p.Service == "" {
					row.Ports = append(row.Ports, m.portRow(p.Name, p.Port))
				}
			}
			for _, l := range inst.Logs {
				row.Logs = append(row.Logs, l)
			}
			rows = append(rows, row)
		}
	}
	return rows
}

func (m *Manager) ownsService(name string) bool {
	for _, inst := range m.Installed() {
		for _, s := range inst.Services {
			if strings.EqualFold(s, name) {
				return true
			}
		}
	}
	return false
}

// ServiceAction queues start / stop / restart for one of our services.
func (m *Manager) ServiceAction(name, action string) (int, error) {
	if !m.P.IsAdmin() {
		return 0, errNotAdmin
	}
	if !m.ownsService(name) {
		return 0, fmt.Errorf("%s 不是由本工具安装的服务", name)
	}
	titles := map[string]string{"start": "启动", "stop": "停止", "restart": "重启"}
	title, ok := titles[action]
	if !ok {
		return 0, fmt.Errorf("未知操作 %q", action)
	}
	t := m.Enqueue("service", title+"服务 "+name, "", nil, func(t *Task, ctx context.Context) error {
		defer m.changed("services")
		if action == "stop" || action == "restart" {
			t.Set(0.3, "正在停止")
			if err := m.P.StopService(name); err != nil {
				return err
			}
			t.Logf("服务 %s 已停止", name)
		}
		if action == "start" || action == "restart" {
			t.Set(0.6, "正在启动")
			if err := m.P.StartService(name); err != nil {
				return fmt.Errorf("%w。可以在服务面板里打开日志查看原因", err)
			}
			t.Logf("服务 %s 已启动", name)
		}
		return nil
	})
	return t.ID, nil
}

// SetAutoStart switches a service between start-on-boot and manual start.
func (m *Manager) SetAutoStart(name string, auto bool) error {
	if !m.P.IsAdmin() {
		return errNotAdmin
	}
	if !m.ownsService(name) {
		return fmt.Errorf("%s 不是由本工具安装的服务", name)
	}
	st := "manual"
	if auto {
		st = "auto"
	}
	err := m.P.SetServiceStart(name, st)
	m.changed("services")
	return err
}

// PortInfo answers "who is using this port".
func (m *Manager) PortInfo(port int) PortRow {
	return m.portRow("", port)
}

// KillPID ends a process the user picked in the port lookup.
func (m *Manager) KillPID(pid int) error {
	if !m.P.IsAdmin() {
		return errNotAdmin
	}
	if pid <= 4 {
		return fmt.Errorf("PID %d 是 Windows 系统进程，不能结束", pid)
	}
	if pid == os.Getpid() {
		return fmt.Errorf("不能结束本程序自己")
	}
	err := m.P.KillProcess(pid)
	m.changed("services")
	return err
}

// TailFile returns the last part of a text file (service logs).
func TailFile(path string, maxBytes int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	start := fi.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	buf := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return "", err
	}
	s := strings.ToValidUTF8(string(buf), "?")
	if start > 0 {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	return s, nil
}

// ReadLog returns the tail of a log file that belongs to an installed package
// or to this program. Other paths are refused.
func (m *Manager) ReadLog(path string) (string, error) {
	allowed := isUnder(path, m.LogDir()) || isUnder(path, filepath.Join(m.Root(), "logs"))
	for _, inst := range m.Installed() {
		for _, l := range inst.Logs {
			allowed = allowed || strings.EqualFold(l, path)
		}
	}
	if !allowed {
		return "", fmt.Errorf("不允许读取 %s", path)
	}
	return TailFile(path, 128<<10)
}
