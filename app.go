package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"devnest/internal/core"
	"devnest/internal/platform"
	_ "devnest/internal/recipes"
)

// App is what the UI talks to. Every exported method is callable from the
// front end; the real work lives in core.Manager.
type App struct {
	mu  sync.RWMutex
	ctx context.Context
	m   *core.Manager
}

func NewApp() *App {
	a := &App{m: core.NewManager(platform.New())}
	a.m.Emit = func(name string, data any) {
		a.mu.RLock()
		ctx := a.ctx
		a.mu.RUnlock()
		if ctx != nil {
			runtime.EventsEmit(ctx, name, data)
		}
	}
	return a
}

func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
}

// beforeClose asks before quitting while something is being installed:
// closing the window would abandon the work half done.
func (a *App) beforeClose(ctx context.Context) (prevent bool) {
	if !a.m.Busy() {
		return false
	}
	choice, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
		Type:          runtime.QuestionDialog,
		Title:         "还有任务没做完",
		Message:       "现在退出会中断正在进行的安装或清理，可能留下装到一半的软件。\n\n确定要退出吗？",
		Buttons:       []string{"Yes", "No"},
		DefaultButton: "No",
		CancelButton:  "No",
	})
	return err != nil || choice != "Yes"
}

func (a *App) secondInstance(options.SecondInstanceData) {
	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()
	if ctx != nil {
		runtime.WindowUnminimise(ctx)
		runtime.Show(ctx)
	}
}

// ---- overview, catalogue, installing ----

func (a *App) Overview() core.Overview        { return a.m.Overview() }
func (a *App) Catalog() []core.PackageView    { return a.m.Catalog() }
func (a *App) Presets() []core.Preset         { return a.m.Presets() }
func (a *App) Installed() []core.Installed    { return orEmpty(a.m.Installed()) }
func (a *App) Settings() core.Settings        { return a.m.Settings() }
func (a *App) Mirrors() []core.MirrorItem     { return a.m.Mirrors() }
func (a *App) Services() []core.ServiceRow    { return orEmpty(a.m.Services()) }
func (a *App) Tasks() []core.Task             { return orEmpty(a.m.Tasks()) }
func (a *App) TaskLog(id int) []string        { return orEmpty(a.m.TaskLog(id)) }
func (a *App) CancelTask(id int)              { a.m.CancelTask(id) }
func (a *App) ClearTasks()                    { a.m.ClearTasks() }
func (a *App) PortInfo(port int) core.PortRow { return a.m.PortInfo(port) }

func (a *App) Install(req core.InstallReq) ([]int, error) { return a.m.Install(req) }

func (a *App) InstallMany(reqs []core.InstallReq) core.BatchResult { return a.m.InstallMany(reqs) }

func (a *App) Uninstall(id, version, dataMode string) (int, error) {
	return a.m.Uninstall(id, version, dataMode)
}

func (a *App) Activate(id, version string) (int, error) { return a.m.Activate(id, version) }

// ImportInstaller lets the user pick an installer downloaded by other means
// (a browser, another machine) and puts it into the cache after checking it.
// It returns the chosen path, or "" when the dialog was cancelled.
func (a *App) ImportInstaller(id, version string) (string, error) {
	pkg := core.Lookup(id)
	if pkg == nil {
		return "", errors.New("没有这个软件：" + id)
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "选择已经下载好的 " + pkg.Name + " " + version + " 安装包"})
	if err != nil || path == "" {
		return "", err
	}
	return path, a.m.ImportInstaller(id, version, path)
}

// ---- services ----

func (a *App) ServiceAction(name, action string) (int, error) { return a.m.ServiceAction(name, action) }
func (a *App) SetAutoStart(name string, auto bool) error      { return a.m.SetAutoStart(name, auto) }
func (a *App) KillPID(pid int) error                          { return a.m.KillPID(pid) }
func (a *App) ReadLog(path string) (string, error)            { return a.m.ReadLog(path) }

// ---- mirrors ----

func (a *App) SetMirror(id, url string) error { return a.m.SetMirror(id, url) }
func (a *App) SetAllMirrors() []string        { return orEmpty(a.m.SetAllMirrors()) }

// ---- cleaning ----

func (a *App) Scan(ids []string) ([]core.Finding, error) {
	found, err := a.m.Scan(ids)
	return orEmpty(found), err
}

func (a *App) Clean(ids []string) (int, error) { return a.m.Clean(ids) }

// ---- settings ----

func (a *App) SaveSettings(s core.Settings) error { return a.m.SaveSettings(s) }
func (a *App) CheckRoot(root string) string {
	if err := a.m.CheckRoot(root); err != nil {
		return err.Error()
	}
	return ""
}
func (a *App) ClearCache() error { return a.m.ClearCache() }

// PickFolder opens the folder chooser; "" means the user cancelled.
func (a *App) PickFolder(title string) (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: title, CanCreateDirectories: true})
}

// OpenPath shows a folder or file that already exists in the file manager.
func (a *App) OpenPath(path string) error {
	if _, err := os.Stat(path); err != nil {
		return errors.New("这个位置不存在：" + path)
	}
	return a.m.P.OpenPath(path)
}

// OpenURL opens a web page in the default browser. Only http(s) is allowed.
func (a *App) OpenURL(url string) error {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return errors.New("只能打开网页地址")
	}
	runtime.BrowserOpenURL(a.ctx, url)
	return nil
}

func (a *App) CopyText(text string) error { return runtime.ClipboardSetText(a.ctx, text) }

// orEmpty turns a nil slice into an empty one so the UI receives [] and not null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
