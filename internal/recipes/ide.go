package recipes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"devnest/internal/core"
	"devnest/internal/platform"
)

// ---- JetBrains IDEs ----

var menuOption = core.Option{Key: "menu", Label: "右键菜单里加上“用它打开”", Type: "bool", Default: "true"}

// installJetBrains drives the official installer with a silent.config file,
// the unattended mode JetBrains documents. exts are file types the installer
// offers to associate; all are declined.
func installJetBrains(exts ...string) func(c *core.Ctx) error {
	return func(c *core.Ctx) error {
		file, err := c.Fetch()
		if err != nil {
			return err
		}
		c.Step("运行安装程序（需要几分钟）")
		menu := "0"
		if c.OptBool("menu") {
			menu = "1"
		}
		lines := []string{"mode=admin", "launcher32=0", "launcher64=0", "updatePATH=0", "updateContextMenu=" + menu,
			"jre32=0", "regenerationSharedArchive=1"}
		for _, e := range exts {
			lines = append(lines, e+"=0")
		}
		// The installer takes these paths unquoted, so they live under the
		// root folder, which cannot contain spaces.
		cfg := filepath.Join(c.M.CacheDir(), c.Pkg.ID+"-silent.config")
		log := filepath.Join(c.M.CacheDir(), c.Pkg.ID+"-install.log")
		if err := c.Write(cfg, strings.Join(lines, "\r\n")+"\r\n"); err != nil {
			return err
		}
		defer os.Remove(cfg)
		if err := c.Installer(file, nil, "/S", "/CONFIG="+cfg, "/LOG="+log, "/D="+c.Dir); err != nil {
			return fmt.Errorf("%w（安装程序的日志：%s）", err, log)
		}
		c.Shortcut(c.Pkg.Name, c.J(filepath.FromSlash(c.Pkg.Probe[0])), "")
		c.Info("启动", "开始菜单里的 "+c.Pkg.Name)
		c.Info("安装位置", c.Dir)
		c.Info("授权", "官方原版，没有做任何激活；没有订阅时可以用其中的免费功能")
		return nil
	}
}

func uninstallJetBrains(c *core.Ctx) error {
	un := c.J("bin", "Uninstall.exe")
	if !c.Exists(un) {
		return nil
	}
	// "_?=" keeps the NSIS uninstaller in the foreground so we can wait. It
	// has to name the folder the uninstaller itself is in, which here is bin:
	// the script works out the installation from there, and given the
	// installation folder instead it goes looking one level too high and
	// never comes back.
	_, err := c.RunOpts(platform.RunOpts{Timeout: 15 * time.Minute}, un, "/S", "_?="+filepath.Dir(un))
	return err
}

func jetbrainsDetect(product string, cfgNames ...string) core.Detect {
	d := core.Detect{
		Uninstall: []string{`^` + product},
		Dirs:      []string{`%ProgramFiles%\JetBrains\` + product + `*`},
	}
	for _, n := range cfgNames {
		d.UserDirs = append(d.UserDirs, `%APPDATA%\JetBrains\`+n+`*`, `%LOCALAPPDATA%\JetBrains\`+n+`*`)
	}
	return d
}

// ---- VS Code ----

const vscodeKey = `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{EA457B21-F73E-494C-ACAB-524FDE069978}_is1`

func installVSCode(c *core.Ctx) error {
	if c.P.RegKeyExists(vscodeKey) {
		return fmt.Errorf("这台电脑上已经装有 VS Code（不是本工具装的）。可以继续用现有的；要重装请先到“环境清理”里把它清掉")
	}
	file, err := c.Fetch()
	if err != nil {
		return err
	}
	c.Step("运行安装程序")
	tasks := "!runcode,!desktopicon,!addtopath,!associatewithfiles"
	if c.OptBool("menu") {
		tasks += ",addcontextmenufiles,addcontextmenufolders"
	} else {
		tasks += ",!addcontextmenufiles,!addcontextmenufolders"
	}
	err = c.Installer(file, nil, "/VERYSILENT", "/NORESTART", "/SP-", "/SUPPRESSMSGBOXES", "/DIR="+c.Dir, "/MERGETASKS="+tasks)
	if err != nil {
		return err
	}
	if err := c.AddPath(c.J("bin")); err != nil {
		return err
	}
	if c.OptBool("zh") {
		c.Step("安装中文语言包")
		_, err := c.RunOpts(platform.RunOpts{Timeout: 5 * time.Minute}, c.J("bin", "code.cmd"),
			"--install-extension", "MS-CEINTL.vscode-language-pack-zh-hans", "--force")
		if err != nil {
			c.Logf("中文语言包没有装上（不影响使用，可以在 VS Code 的扩展里搜 Chinese 手动装）：%v", err)
		} else {
			argv := filepath.Join(c.P.Dir("home"), ".vscode", "argv.json")
			if !c.Exists(argv) {
				if err := c.Write(argv, "{\n\t\"locale\": \"zh-cn\"\n}\n"); err != nil {
					c.Logf("没能把界面语言设成中文（在 VS Code 里按 Ctrl+Shift+P 输入 Configure Display Language 可以手动设）：%v", err)
				}
			}
		}
	}
	c.Shortcut("Visual Studio Code", c.J("Code.exe"), "")
	c.Info("启动", "开始菜单里的 Visual Studio Code，或者在命令行输入 code")
	c.Info("升级", "VS Code 会自己更新，这里显示的版本号是安装时的版本")
	return nil
}

// ---- Apifox ----

func installApifox(c *core.Ctx) error {
	if have, ok := c.HasProgram(`^Apifox`); ok {
		return fmt.Errorf("这台电脑上已经装有 %s", have)
	}
	archive, err := c.Fetch()
	if err != nil {
		return err
	}
	c.Step("解压安装程序")
	tmp := filepath.Join(c.M.CacheDir(), "apifox-setup")
	os.RemoveAll(tmp)
	defer os.RemoveAll(tmp)
	if err := c.UnpackTo(archive, tmp); err != nil {
		return err
	}
	setups, _ := filepath.Glob(filepath.Join(tmp, "*.exe"))
	if len(setups) == 0 && !c.Sim() {
		return fmt.Errorf("下载的压缩包里没有找到安装程序，Apifox 可能改了打包方式")
	}
	setup := filepath.Join(tmp, "Apifox.exe")
	if len(setups) > 0 {
		setup = setups[0]
	}
	c.Step("运行安装程序")
	if err := c.Installer(setup, nil, "/S"); err != nil {
		return err
	}
	if err := c.Write(c.J("README.txt"), "Apifox 由它自己的安装程序安装，位置由安装程序决定。\r\n这个文件夹只是 DevNest 的记录。\r\n"); err != nil {
		return err
	}
	if _, ok := c.HasProgram(`^Apifox`); !ok && !c.Sim() {
		return fmt.Errorf("安装程序运行完了，但 Windows 的已安装程序列表里没有 Apifox")
	}
	c.Info("启动", "开始菜单或桌面上的 Apifox")
	c.Info("安装位置", "由 Apifox 安装程序决定（通常在当前用户的 AppData\\Local\\Programs 下）")
	return nil
}

// ---- Docker Desktop ----

func dockerSetup(c *core.Ctx) string {
	return filepath.Join(c.P.Dir("programfiles"), "Docker", "Docker", "Docker Desktop Installer.exe")
}

func installDocker(c *core.Ctx) error {
	if have, ok := c.HasProgram(`^Docker Desktop$`); ok {
		return fmt.Errorf("这台电脑上已经装有 %s。要重装请先到“环境清理”里把它清掉", have)
	}
	file, err := c.Fetch()
	if err != nil {
		return err
	}
	if err := c.CheckSignature(file); err != nil {
		return err
	}
	c.Step("检查 WSL")
	if _, err := c.RunOpts(platform.RunOpts{Timeout: 2 * time.Minute}, "wsl.exe", "--status"); err != nil {
		c.Logf("WSL 还没有启用，现在启用（完成后需要重启电脑）")
		if _, err := c.RunOpts(platform.RunOpts{Timeout: 30 * time.Minute, OKCodes: []int{3010}}, "wsl.exe", "--install", "--no-distribution"); err != nil {
			c.Logf("自动启用 WSL 没有成功：%v。Docker Desktop 第一次启动时会再提示一次", err)
		}
	}
	c.Step("运行安装程序（需要几分钟）")
	if err := c.Installer(file, []int{3010}, "install", "--quiet", "--accept-license", "--backend=wsl-2"); err != nil {
		return err
	}
	note := "Docker Desktop 由官方安装程序装在 " + filepath.Dir(dockerSetup(c)) + "。\r\n这个文件夹只是 DevNest 的记录。\r\n"
	if err := c.Write(c.J("README.txt"), note); err != nil {
		return err
	}
	c.Info("启动", "开始菜单里的 Docker Desktop。第一次启动前请先重启电脑")
	c.Info("安装位置", filepath.Dir(dockerSetup(c))+"（官方安装程序的固定位置）")
	c.Info("镜像加速", "拉镜像慢的话，在“镜像加速”页填上你能用的 Docker 镜像地址")
	return nil
}

func uninstallDocker(c *core.Ctx) error {
	setup := dockerSetup(c)
	if !c.Exists(setup) {
		return nil
	}
	_, err := c.RunOpts(platform.RunOpts{Timeout: 30 * time.Minute, OKCodes: []int{3010}}, setup, "uninstall", "--quiet")
	return err
}

func init() {
	core.Register(&core.Package{
		ID: "idea", Name: "IntelliJ IDEA", Category: "ide",
		Desc:     "JetBrains 的 Java / Kotlin 开发工具，官方原版安装包",
		Homepage: "https://www.jetbrains.com/idea/",
		Notes: []string{
			"只安装官方原版，不含任何激活或破解。从 2025.3 起 IDEA 只有这一个安装包：没有订阅也能用免费功能，付费功能需要自己的订阅。",
			"安装包 1.1 GB，从 JetBrains 官网下载。",
		},
		Releases: []core.Release{{Version: "2026.3", File: "idea-2026.3.exe", Size: 1213323256,
			SHA256: "9b7e91d41e315df62024f8fff4cbc882eb1dad28edb45f1facf9b4c53f7484ac",
			URLs:   []string{"https://download.jetbrains.com/idea/idea-2026.3.exe"}}},
		Options:   []core.Option{menuOption},
		Probe:     []string{"bin/idea64.exe"},
		Steps:     1,
		Install:   installJetBrains(".java", ".groovy", ".kt", ".kts"),
		Uninstall: uninstallJetBrains,
		Detect:    jetbrainsDetect("IntelliJ IDEA", "IntelliJIdea", "IdeaIC"),
	})

	core.Register(&core.Package{
		ID: "pycharm", Name: "PyCharm", Category: "ide",
		Desc:     "JetBrains 的 Python 开发工具，官方原版安装包",
		Homepage: "https://www.jetbrains.com/pycharm/",
		Notes:    []string{"只安装官方原版，不含任何激活或破解。没有订阅也能用免费功能，付费功能需要自己的订阅。"},
		Releases: []core.Release{{Version: "2026.3", File: "pycharm-2026.3.exe", Size: 994179952,
			SHA256: "60433d41f010b826f918d6869d3f67bbcffa1bbb9f10c942ba702d88e98fa4e1",
			URLs:   []string{"https://download.jetbrains.com/python/pycharm-2026.3.exe"}}},
		Options:   []core.Option{menuOption},
		Probe:     []string{"bin/pycharm64.exe"},
		Steps:     1,
		Install:   installJetBrains(".py"),
		Uninstall: uninstallJetBrains,
		Detect:    jetbrainsDetect("PyCharm", "PyCharm"),
	})

	core.Register(&core.Package{
		ID: "vscode", Name: "VS Code", Category: "ide",
		Desc:     "微软的轻量代码编辑器，可选装中文语言包和右键菜单",
		Homepage: "https://code.visualstudio.com/",
		Releases: []core.Release{{Version: "1.146.0", File: "VSCodeSetup-x64-1.146.0.exe", Size: 286108016,
			SHA256: "972fcb16d819532a25af8dc1976e1c80508e080bf56eb65bfd6a35eed983a35b",
			URLs: []string{
				"https://vscode.download.prss.microsoft.com/dbazure/download/stable/69047191fc427f2f92828279aae1d90d8eeda7ab/VSCodeSetup-x64-1.146.0.exe",
				"https://update.code.visualstudio.com/1.146.0/win32-x64/stable",
			}}},
		Options: []core.Option{
			{Key: "zh", Label: "安装中文语言包", Type: "bool", Default: "true"},
			menuOption,
		},
		Probe:     []string{"Code.exe", "bin/code.cmd"},
		Steps:     2,
		Install:   installVSCode,
		Uninstall: innoUninstall,
		Detect: core.Detect{
			Uninstall: []string{`^Microsoft Visual Studio Code`},
			PathRx:    []string{`(?i)\\Microsoft VS Code\\bin\\?$`},
			Dirs:      []string{`%ProgramFiles%\Microsoft VS Code`, `%LOCALAPPDATA%\Programs\Microsoft VS Code`},
			UserDirs:  []string{`%APPDATA%\Code`, `%USERPROFILE%\.vscode`},
		},
	})

	dbeaver := "dbeaver-ce-26.2.5-windows-x86_64.zip"
	core.Register(&core.Package{
		ID: "dbeaver", Name: "DBeaver", Category: "tool",
		Desc:     "免费的数据库图形客户端，能连 MySQL、PostgreSQL、MongoDB 等；自带 Java 运行环境",
		Homepage: "https://dbeaver.io/",
		Releases: []core.Release{{Version: "26.2.5", File: dbeaver, Size: 126280946, URLs: []string{
			"https://dbeaver.io/files/26.2.5/" + dbeaver,
			gh("dbeaver/dbeaver", "26.2.5", dbeaver),
		}}},
		Probe: []string{"dbeaver.exe"},
		Steps: 1,
		Install: func(c *core.Ctx) error {
			if err := unzipInstall(c); err != nil {
				return err
			}
			c.Shortcut("DBeaver", c.J("dbeaver.exe"), "")
			c.Info("启动", "开始菜单 → DevNest → DBeaver")
			c.Info("第一次连数据库", "会提示下载对应的驱动，需要联网")
			return nil
		},
		Detect: core.Detect{
			Uninstall: []string{`^DBeaver`},
			Dirs:      []string{`%ProgramFiles%\DBeaver`, `%LOCALAPPDATA%\DBeaver`},
			UserDirs:  []string{`%APPDATA%\DBeaverData`},
		},
	})

	core.Register(&core.Package{
		ID: "apifox", Name: "Apifox", Category: "tool",
		Desc:     "接口调试和文档工具（类似 Postman）",
		Homepage: "https://apifox.com/",
		Notes:    []string{"下载的是官网的“最新版”地址，版本随官网变化；安装位置由它自己的安装程序决定。"},
		Releases: []core.Release{{Version: "latest", Label: "官网最新版", File: "Apifox-windows-latest.zip",
			URLs: []string{"https://file-assets.apifox.com/download/Apifox-windows-latest.zip"}}},
		Probe:   []string{"README.txt"},
		Steps:   2,
		Install: installApifox,
		Uninstall: func(c *core.Ctx) error {
			_, err := c.UninstallProgram(`^Apifox`)
			return err
		},
		Detect: core.Detect{
			Uninstall: []string{`^Apifox`},
			UserDirs:  []string{`%APPDATA%\apifox`},
		},
	})

	core.Register(&core.Package{
		ID: "docker", Name: "Docker Desktop", Category: "tool",
		Desc:     "在 Windows 上运行容器（基于 WSL 2）",
		Homepage: "https://www.docker.com/products/docker-desktop/",
		Notes: []string{
			"需要 Windows 10 22H2 或 Windows 11，并且在 BIOS 里打开了虚拟化。装完要重启电脑。",
			"下载的是官网的“最新版”地址（约 600 MB），固定装在 C:\\Program Files\\Docker。",
			"Docker Desktop 对大公司商用是收费的，请自行确认授权。",
		},
		Releases: []core.Release{{Version: "latest", Label: "官网最新版", File: "DockerDesktopInstaller.exe",
			URLs: []string{"https://desktop.docker.com/win/main/amd64/Docker%20Desktop%20Installer.exe"}}},
		Probe:     []string{"README.txt"},
		Steps:     2,
		Install:   installDocker,
		Uninstall: uninstallDocker,
		Detect: core.Detect{
			Uninstall: []string{`^Docker Desktop$`},
			Dirs:      []string{`%ProgramFiles%\Docker`, `%ProgramData%\DockerDesktop`},
			UserDirs:  []string{`%APPDATA%\Docker`, `%APPDATA%\Docker Desktop`},
		},
	})
}
