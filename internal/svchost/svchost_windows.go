//go:build windows

package svchost

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"

	"devnest/internal/platform"
)

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procAttachConsole         = kernel32.NewProc("AttachConsole")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
	procSetConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
)

// Run is the entry point when Windows starts us as a service.
func Run(configPath string) error {
	cfg, err := Load(configPath)
	if err != nil {
		return err
	}
	return svc.Run(cfg.Name, &host{cfg: cfg})
}

type host struct {
	cfg Config
	log *os.File
}

func (h *host) logf(format string, a ...any) {
	if h.log != nil {
		fmt.Fprintf(h.log, "[DevNest %s] %s\r\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
	}
}

func (h *host) openLog() {
	if h.cfg.LogFile == "" {
		return
	}
	os.MkdirAll(filepath.Dir(h.cfg.LogFile), 0o755)
	// Keep one previous log so the file cannot grow without limit.
	if fi, err := os.Stat(h.cfg.LogFile); err == nil && fi.Size() > 20<<20 {
		os.Remove(h.cfg.LogFile + ".1")
		os.Rename(h.cfg.LogFile, h.cfg.LogFile+".1")
	}
	// Not O_APPEND: Go would open the handle with append-only access, and the
	// Cygwin/MSYS2 runtime (Redis for Windows is built on it) treats an
	// inherited handle without plain write access as read-only, so everything
	// the program prints would be lost. The program inherits this very handle
	// and shares its position, so both sides keep appending.
	f, err := os.OpenFile(h.cfg.LogFile, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	f.Seek(0, io.SeekEnd)
	h.log = f
}

// newJob creates a job object that kills every process in it when closed, so
// stopping the service never leaves children (java.exe behind a .bat) running.
func newJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func (h *host) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	s <- svc.Status{State: svc.StartPending}
	h.openLog()
	if h.log != nil {
		defer h.log.Close()
	}
	job, err := newJob()
	if err != nil {
		h.logf("创建作业对象失败：%v", err)
		return true, 1
	}
	defer windows.CloseHandle(job)

	cmd := platform.NewCommand(context.Background(), h.cfg.Exe, h.cfg.Args, h.cfg.Env)
	cmd.Dir = h.cfg.Dir
	if h.log != nil {
		cmd.Stdout, cmd.Stderr = h.log, h.log
	}
	h.logf("启动 %s %v", h.cfg.Exe, h.cfg.Args)
	if err := cmd.Start(); err != nil {
		h.logf("启动失败：%v", err)
		return true, 2
	}
	if ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid)); err == nil {
		if err := windows.AssignProcessToJobObject(job, ph); err != nil {
			h.logf("加入作业对象失败：%v", err)
		}
		windows.CloseHandle(ph)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			h.logf("程序自己退出了：%v", err)
			s <- svc.Status{State: svc.StopPending}
			if err != nil {
				return true, 3
			}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				s <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				timeout := time.Duration(h.cfg.StopTimeoutSec) * time.Second
				if timeout <= 0 {
					timeout = 30 * time.Second
				}
				s <- svc.Status{State: svc.StopPending, WaitHint: uint32((timeout + 10*time.Second) / time.Millisecond)}
				h.stop(cmd, done, timeout)
				windows.TerminateJobObject(job, 0)
				h.logf("已停止")
				return false, 0
			}
		}
	}
}

// stop asks the program to exit: first with its own stop command or Ctrl+C,
// then, after the timeout, by force.
func (h *host) stop(cmd *exec.Cmd, done <-chan error, timeout time.Duration) {
	if h.cfg.StopExe != "" {
		h.logf("执行停止命令 %s %v", h.cfg.StopExe, h.cfg.StopArgs)
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		stop := platform.NewCommand(ctx, h.cfg.StopExe, h.cfg.StopArgs, h.cfg.Env)
		stop.Dir = h.cfg.Dir
		if h.log != nil {
			stop.Stdout, stop.Stderr = h.log, h.log
		}
		if err := stop.Run(); err != nil {
			h.logf("停止命令失败：%v", err)
			sendCtrlC(cmd.Process.Pid)
		}
	} else {
		h.logf("发送 Ctrl+C")
		if err := sendCtrlC(cmd.Process.Pid); err != nil {
			h.logf("发送 Ctrl+C 失败：%v", err)
		}
	}
	select {
	case <-done:
	case <-time.After(timeout):
		h.logf("%s 内没有退出，强制结束", timeout)
		cmd.Process.Kill()
	}
}

// sendCtrlC attaches to the child's (hidden) console and raises Ctrl+C there.
// We ignore the signal ourselves while doing so.
func sendCtrlC(pid int) error {
	if r, _, err := procAttachConsole.Call(uintptr(pid)); r == 0 {
		return fmt.Errorf("AttachConsole: %w", err)
	}
	defer procFreeConsole.Call()
	procSetConsoleCtrlHandler.Call(0, 1)
	defer func() {
		// Give the event time to be delivered before we listen to Ctrl+C again.
		time.Sleep(500 * time.Millisecond)
		procSetConsoleCtrlHandler.Call(0, 0)
	}()
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_C_EVENT, 0)
}
