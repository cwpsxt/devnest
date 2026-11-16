package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"devnest/internal/platform"
)

// CheckSignature refuses a downloaded program whose Authenticode signature
// Windows does not accept. It is the integrity check for installers whose
// publisher gives no checksum we can pin.
func (c *Ctx) CheckSignature(file string) error {
	if c.Sim() {
		c.Logf("[模拟] 核对数字签名 %s", filepath.Base(file))
		return nil
	}
	script := "$s = Get-AuthenticodeSignature -LiteralPath $env:DEVNEST_FILE; " +
		"$n = ''; if ($s.SignerCertificate) { $n = $s.SignerCertificate.Subject }; " +
		"Write-Output ('SIG|' + $s.Status + '|' + $n)"
	res, err := c.P.Run(c, "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script},
		platform.RunOpts{Timeout: 3 * time.Minute, Env: map[string]string{"DEVNEST_FILE": file}})
	if err != nil {
		return fmt.Errorf("没能核对安装包的数字签名：%w", err)
	}
	for _, line := range strings.Split(res.Output, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "|", 3)
		if len(parts) != 3 || parts[0] != "SIG" {
			continue
		}
		if parts[1] != "Valid" {
			return fmt.Errorf("安装包 %s 的数字签名不被 Windows 认可（状态：%s），已拒绝运行。可能是下载损坏或被篡改，请在设置里清理下载缓存后重试", filepath.Base(file), parts[1])
		}
		c.Logf("数字签名有效：%s", parts[2])
		return nil
	}
	return fmt.Errorf("没能核对安装包的数字签名：PowerShell 没有返回结果")
}

// UninstallProgram runs the silent uninstaller that Windows has on record for
// the program whose display name matches nameRx. found is false when Windows
// lists no such program.
func (c *Ctx) UninstallProgram(nameRx string) (found bool, err error) {
	rx, err := regexp.Compile(nameRx)
	if err != nil {
		return false, err
	}
	entries, err := c.P.UninstallEntries()
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if !rx.MatchString(e.DisplayName) {
			continue
		}
		cmd, ok := quietUninstall(e)
		if !ok {
			return true, fmt.Errorf("%s 没有静默卸载方式，请到 Windows“设置 → 应用”里手动卸载", e.DisplayName)
		}
		c.Logf("卸载 %s", e.DisplayName)
		return true, c.RunShell(cmd, 3010, 1605, 1614)
	}
	return false, nil
}

// HasProgram reports whether Windows lists an installed program whose display
// name matches nameRx, and returns that name.
func (c *Ctx) HasProgram(nameRx string) (string, bool) {
	rx, err := regexp.Compile(nameRx)
	if err != nil {
		return "", false
	}
	entries, _ := c.P.UninstallEntries()
	for _, e := range entries {
		if rx.MatchString(e.DisplayName) {
			return e.DisplayName, true
		}
	}
	return "", false
}

// UseMirror applies the recommended mirror for a tool (see mirrorDefs) unless
// a source has already been chosen. A failure is logged, not returned: the
// software works without it, only slower.
func (c *Ctx) UseMirror(id string) {
	for _, d := range mirrorDefs {
		if d.id != id || len(d.choices) == 0 {
			continue
		}
		if cur := d.get(c.M); cur != "" {
			c.Logf("%s 的下载源已经设为 %s，保持不变", d.name, cur)
			return
		}
		ch := d.choices[0]
		if err := d.set(c.M, ch.Value); err != nil {
			c.Logf("设置 %s 国内镜像没有成功（可以稍后在“镜像加速”里再设）：%v", d.name, err)
			return
		}
		c.Logf("%s 的下载源设为 %s（%s）", d.name, ch.Label, ch.Value)
		c.M.changed("mirrors")
		return
	}
}

// TempFile writes content to a private file outside the install folder and
// returns its path with a function that deletes it. It is for values such as
// passwords that a tool only accepts from a file.
func (c *Ctx) TempFile(name, content string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "devnest-")
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	return path, func() { os.RemoveAll(dir) }, nil
}

// CopyFile copies one file, creating the destination folder.
func (c *Ctx) CopyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return copyFile(src, dst)
}

// CopyTree copies a folder and everything in it.
func (c *Ctx) CopyTree(src, dst string) error { return copyTree(src, dst) }

// Uncomment activates lines that are commented out with the given prefix,
// for each wanted setting (matched on the text after the prefix).
func Uncomment(content, prefix string, wanted ...string) string {
	nl := eol(content)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, prefix) {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(t, prefix))
		for _, w := range wanted {
			if body == w {
				lines[i] = body
			}
		}
	}
	return strings.Join(lines, nl)
}

var hexOnly = regexp.MustCompile(`^[0-9a-f]+$`)

// spacedDigest reads the layout printed by "gpg --print-md", which Apache
// projects such as Kafka publish: "file: AB12CD34 EF56..." spread over
// several lines.
func spacedDigest(txt string) string {
	i := strings.LastIndex(txt, ":")
	if i < 0 {
		return ""
	}
	h := strings.ToLower(strings.Join(strings.Fields(txt[i+1:]), ""))
	if hexOnly.MatchString(h) && (len(h) == 128 || len(h) == 64) {
		return h
	}
	return ""
}
