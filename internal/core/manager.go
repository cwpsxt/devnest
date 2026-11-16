package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"devnest/internal/platform"
)

const AppVersion = "0.1.0"

type Settings struct {
	// Root is where everything is installed: <Root>/apps, data, cache, backup.
	Root string `json:"root"`
	// GithubProxy is an optional prefix put in front of github.com download
	// addresses (GitHub is slow from mainland China).
	GithubProxy string `json:"githubProxy"`
	// DesktopShortcuts controls whether IDEs get a desktop shortcut.
	DesktopShortcuts bool `json:"desktopShortcuts"`
}

// Manager owns the settings, the record of what is installed and the task
// queue. All exported methods are safe to call from the UI thread.
type Manager struct {
	P    platform.Platform
	Emit func(event string, data any)

	mu       sync.Mutex
	settings Settings
	state    map[string]*Installed
	lastScan map[string]Finding

	taskMu  sync.Mutex
	tasks   []*Task
	nextID  int
	wake    chan struct{}
	started bool
}

func NewManager(p platform.Platform) *Manager {
	m := &Manager{P: p, Emit: func(string, any) {}, state: map[string]*Installed{}, wake: make(chan struct{}, 1)}
	m.load()
	return m
}

// ConfigDir is where settings and state live (not under Root, so they survive
// moving the install folder).
func (m *Manager) ConfigDir() string {
	return filepath.Join(m.P.Dir("programdata"), "DevNest")
}

func (m *Manager) LogDir() string { return filepath.Join(m.ConfigDir(), "logs") }

func (m *Manager) load() {
	if b, err := os.ReadFile(filepath.Join(m.ConfigDir(), "settings.json")); err == nil {
		json.Unmarshal(b, &m.settings)
	} else {
		m.settings.DesktopShortcuts = true
	}
	if m.settings.Root == "" {
		m.settings.Root = m.defaultRoot()
	}
	if b, err := os.ReadFile(filepath.Join(m.ConfigDir(), "state.json")); err == nil {
		var list []*Installed
		if json.Unmarshal(b, &list) == nil {
			for _, i := range list {
				m.state[i.Key()] = i
			}
		}
	}
}

// defaultRoot prefers the non-system drive with the most free space, like
// D:\DevNest, and falls back to the system drive.
func (m *Manager) defaultRoot() string {
	drives := m.P.Drives()
	sys := strings.ToUpper(filepath.VolumeName(m.P.Dir("systemroot")))
	best := ""
	var bestFree uint64
	for _, d := range drives {
		isSys := sys != "" && strings.HasPrefix(strings.ToUpper(d.Root), sys)
		if m.P.Simulated() {
			isSys = strings.HasSuffix(strings.TrimRight(d.Root, `\/`), "C")
		}
		if !isSys && d.FreeBytes > bestFree {
			best, bestFree = d.Root, d.FreeBytes
		}
	}
	if best == "" && len(drives) > 0 {
		best = drives[0].Root
	}
	if best == "" {
		best = `C:\`
	}
	return filepath.Join(best, "DevNest")
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (m *Manager) saveState() error {
	list := make([]*Installed, 0, len(m.state))
	for _, k := range sortedKeys(m.state) {
		list = append(list, m.state[k])
	}
	return writeJSON(filepath.Join(m.ConfigDir(), "state.json"), list)
}

func (m *Manager) Settings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

var rootRx = regexp.MustCompile(`^[A-Za-z]:\\[A-Za-z0-9_.\-\\]*$`)

// CheckRoot explains why a folder cannot be used as the install root. MySQL,
// Neo4j, Kafka and several batch scripts break on spaces or non-ASCII
// characters in their path, so those are refused up front.
func (m *Manager) CheckRoot(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return errors.New("安装目录不能为空")
	}
	if m.P.Simulated() {
		return nil
	}
	if !rootRx.MatchString(root) {
		return errors.New(`安装目录只能用英文字母、数字、下划线、点和短横线，不能有空格和中文，例如 D:\DevNest`)
	}
	if len(strings.Trim(root[2:], `\`)) == 0 {
		return errors.New(`不要直接用盘符根目录，请指定一个文件夹，例如 D:\DevNest`)
	}
	if len(root) > 60 {
		return errors.New("安装目录太长，请控制在 60 个字符以内")
	}
	low := strings.ToLower(root)
	for _, bad := range []string{m.P.Dir("systemroot"), m.P.Dir("programfiles"), m.P.Dir("programfilesx86")} {
		if bad != "" && strings.HasPrefix(low, strings.ToLower(bad)) {
			return errors.New("不要装到 Windows 或 Program Files 目录下")
		}
	}
	return nil
}

func (m *Manager) SaveSettings(s Settings) error {
	s.Root = strings.TrimRight(strings.TrimSpace(s.Root), `\/`)
	s.GithubProxy = strings.TrimSpace(s.GithubProxy)
	if err := m.CheckRoot(s.Root); err != nil {
		return err
	}
	if s.GithubProxy != "" && !strings.HasPrefix(s.GithubProxy, "https://") {
		return errors.New("GitHub 加速地址必须以 https:// 开头")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !strings.EqualFold(s.Root, m.settings.Root) && len(m.state) > 0 {
		return errors.New("已经有软件装在当前目录下，要换安装目录请先卸载它们")
	}
	m.settings = s
	return writeJSON(filepath.Join(m.ConfigDir(), "settings.json"), s)
}

func (m *Manager) Root() string       { return m.Settings().Root }
func (m *Manager) AppsDir() string    { return filepath.Join(m.Root(), "apps") }
func (m *Manager) DataRoot() string   { return filepath.Join(m.Root(), "data") }
func (m *Manager) CacheDir() string   { return filepath.Join(m.Root(), "cache") }
func (m *Manager) BackupRoot() string { return filepath.Join(m.Root(), "backup") }
func (m *Manager) Fetcher() *Fetcher {
	return NewFetcher(m.CacheDir(), m.Settings().GithubProxy, m.P.Simulated())
}
func (m *Manager) underRoot(p string) bool { return isUnder(p, m.Root()) }

func isUnder(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	p := platform.NormPath(path)
	r := platform.NormPath(root)
	return p == r || strings.HasPrefix(p, r+`\`)
}

// ---------- installed state ----------

func (m *Manager) Installed() []Installed {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Installed, 0, len(m.state))
	for _, k := range sortedKeys(m.state) {
		out = append(out, *m.state[k])
	}
	return out
}

func (m *Manager) versionsOf(id string) []*Installed {
	var out []*Installed
	for _, i := range m.state {
		if i.ID == id {
			out = append(out, i)
		}
	}
	sort.Slice(out, func(a, b int) bool { return CompareVersions(out[a].Version, out[b].Version) > 0 })
	return out
}

// VersionsOf returns the installed versions of a package, newest first.
func (m *Manager) VersionsOf(id string) []Installed {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Installed
	for _, i := range m.versionsOf(id) {
		out = append(out, *i)
	}
	return out
}

// Find returns the installed version satisfying a dependency, preferring the
// active one.
func (m *Manager) Find(d Dep) (Installed, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var hit *Installed
	for _, i := range m.versionsOf(d.ID) {
		if d.Matches(i.Version) && (hit == nil || i.Active) {
			hit = i
		}
	}
	if hit == nil {
		return Installed{}, false
	}
	return *hit, true
}

func (m *Manager) changed(what string) { m.Emit("changed", what) }

// ---------- tasks ----------

type Task struct {
	ID       int       `json:"id"`
	Kind     string    `json:"kind"`
	Title    string    `json:"title"`
	PkgID    string    `json:"pkgId,omitempty"`
	State    string    `json:"state"` // queued | running | done | failed | canceled
	Progress float64   `json:"progress"`
	Message  string    `json:"message"`
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended"`

	m        *Manager
	run      func(t *Task, ctx context.Context) error
	cancel   context.CancelFunc
	after    []int
	logs     []string
	logFile  *os.File
	secrets  []string
	lastEmit time.Time
}

func (t *Task) snapshot() Task {
	return Task{ID: t.ID, Kind: t.Kind, Title: t.Title, PkgID: t.PkgID, State: t.State, Progress: t.Progress,
		Message: t.Message, Error: t.Error, Started: t.Started, Ended: t.Ended}
}

func (t *Task) emit(force bool) {
	if !force && time.Since(t.lastEmit) < 120*time.Millisecond {
		return
	}
	t.lastEmit = time.Now()
	t.m.Emit("task", t.snapshot())
}

// Logf adds a line to the task log. Anything registered as a secret is masked.
func (t *Task) Logf(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	t.m.taskMu.Lock()
	for _, s := range t.secrets {
		if s != "" {
			line = strings.ReplaceAll(line, s, "******")
		}
	}
	line = time.Now().Format("15:04:05") + " " + line
	t.logs = append(t.logs, line)
	if len(t.logs) > 4000 {
		t.logs = t.logs[len(t.logs)-3000:]
	}
	if t.logFile != nil {
		t.logFile.WriteString(line + "\n")
	}
	t.m.taskMu.Unlock()
	t.m.Emit("log", map[string]any{"id": t.ID, "line": line})
}

func (t *Task) Secret(s string) {
	if len(s) < 3 {
		return
	}
	t.m.taskMu.Lock()
	t.secrets = append(t.secrets, s)
	t.m.taskMu.Unlock()
}

func (t *Task) Set(progress float64, message string) {
	t.m.taskMu.Lock()
	if progress >= 0 {
		t.Progress = progress
	}
	if message != "" {
		t.Message = message
	}
	t.m.taskMu.Unlock()
	t.emit(false)
}

// Enqueue adds a task to the queue. Tasks run one at a time, in order; a task
// listed in after must have succeeded or this one is skipped as failed.
func (m *Manager) Enqueue(kind, title, pkgID string, after []int, run func(t *Task, ctx context.Context) error) *Task {
	m.taskMu.Lock()
	m.nextID++
	t := &Task{ID: m.nextID, Kind: kind, Title: title, PkgID: pkgID, State: "queued", Message: "排队中", m: m, run: run, after: after}
	m.tasks = append(m.tasks, t)
	if !m.started {
		m.started = true
		go m.worker()
	}
	m.taskMu.Unlock()
	t.emit(true)
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return t
}

func (m *Manager) nextQueued() *Task {
	m.taskMu.Lock()
	defer m.taskMu.Unlock()
	for _, t := range m.tasks {
		if t.State == "queued" {
			return t
		}
	}
	return nil
}

func (m *Manager) taskByID(id int) *Task {
	for _, t := range m.tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (m *Manager) worker() {
	for {
		t := m.nextQueued()
		if t == nil {
			<-m.wake
			continue
		}
		m.runTask(t)
	}
}

func (m *Manager) runTask(t *Task) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.taskMu.Lock()
	blocked := ""
	for _, id := range t.after {
		if dep := m.taskByID(id); dep != nil && dep.State != "done" {
			blocked = dep.Title
		}
	}
	t.State, t.Started, t.Message, t.cancel = "running", time.Now(), "准备中", cancel
	os.MkdirAll(m.LogDir(), 0o755)
	name := fmt.Sprintf("%s-%03d-%s.log", t.Started.Format("20060102-150405"), t.ID, t.Kind)
	t.logFile, _ = os.Create(filepath.Join(m.LogDir(), name))
	m.taskMu.Unlock()
	t.emit(true)
	t.Logf("开始：%s", t.Title)

	var err error
	if blocked != "" {
		err = fmt.Errorf("前置任务「%s」没有成功，已跳过", blocked)
	} else {
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("程序内部错误：%v", r)
				}
			}()
			err = t.run(t, ctx)
		}()
	}

	m.taskMu.Lock()
	t.Ended = time.Now()
	switch {
	case err == nil:
		t.State, t.Progress, t.Message = "done", 1, "完成"
	case errors.Is(err, context.Canceled):
		t.State, t.Message, t.Error = "canceled", "已取消", "已取消"
	default:
		t.State, t.Message, t.Error = "failed", "失败", err.Error()
	}
	m.taskMu.Unlock()
	if err == nil {
		t.Logf("完成，用时 %s", t.Ended.Sub(t.Started).Round(time.Second))
	} else {
		t.Logf("失败：%v", err)
	}
	m.taskMu.Lock()
	if t.logFile != nil {
		t.logFile.Close()
		t.logFile = nil
	}
	m.taskMu.Unlock()
	t.emit(true)
	m.changed("task")
}

func (m *Manager) Tasks() []Task {
	m.taskMu.Lock()
	defer m.taskMu.Unlock()
	out := make([]Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, t.snapshot())
	}
	return out
}

func (m *Manager) TaskLog(id int) []string {
	m.taskMu.Lock()
	defer m.taskMu.Unlock()
	if t := m.taskByID(id); t != nil {
		return append([]string(nil), t.logs...)
	}
	return nil
}

// CancelTask stops a running task or drops a queued one.
func (m *Manager) CancelTask(id int) {
	m.taskMu.Lock()
	t := m.taskByID(id)
	if t == nil {
		m.taskMu.Unlock()
		return
	}
	switch t.State {
	case "queued":
		t.State, t.Message, t.Error, t.Ended = "canceled", "已取消", "已取消", time.Now()
	case "running":
		if t.cancel != nil {
			t.cancel()
		}
	}
	m.taskMu.Unlock()
	t.emit(true)
}

// ClearTasks forgets finished tasks.
func (m *Manager) ClearTasks() {
	m.taskMu.Lock()
	keep := m.tasks[:0]
	for _, t := range m.tasks {
		if t.State == "queued" || t.State == "running" {
			keep = append(keep, t)
		}
	}
	m.tasks = keep
	m.taskMu.Unlock()
	m.changed("task")
}

// Busy reports whether a task is queued or running.
func (m *Manager) Busy() bool {
	m.taskMu.Lock()
	defer m.taskMu.Unlock()
	for _, t := range m.tasks {
		if t.State == "queued" || t.State == "running" {
			return true
		}
	}
	return false
}

// WaitIdle blocks until the queue is empty (used by tests and the CLI).
func (m *Manager) WaitIdle() {
	for m.Busy() {
		time.Sleep(20 * time.Millisecond)
	}
}
