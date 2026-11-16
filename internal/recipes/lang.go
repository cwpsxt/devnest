package recipes

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"devnest/internal/core"
	"devnest/internal/platform"
)

func npmBinary(dir, file string) string {
	return "https://registry.npmmirror.com/-/binary/" + dir + "/" + file
}

// ---- Python ----

func pythonRelease(v, label string, size int64) core.Release {
	f := "python-" + v + "-amd64.exe"
	return core.Release{Version: v, Label: label, File: f, Size: size, URLs: []string{
		npmBinary("python/"+v, f),
		"https://mirrors.huaweicloud.com/python/" + v + "/" + f,
		"https://www.python.org/ftp/python/" + v + "/" + f,
	}}
}

func installPython(c *core.Ctx) error {
	minor := core.MinorKey(c.Rel.Version)
	if c.P.RegKeyExists(`HKLM\SOFTWARE\Python\PythonCore\` + minor + `\InstallPath`) {
		return fmt.Errorf("这台电脑上已经有一份 Python %s（不是本工具装的）。同一个小版本只能装一份，请先到“环境清理”里把它清掉", minor)
	}
	file, err := c.Fetch()
	if err != nil {
		return err
	}
	if err := c.CheckSignature(file); err != nil {
		return err
	}
	c.Step("运行安装程序")
	err = c.Installer(file, []int{3010}, "/quiet", "InstallAllUsers=1", "TargetDir="+c.Dir,
		"PrependPath=0", "AssociateFiles=0", "Shortcuts=0", "Include_launcher=0", "InstallLauncherAllUsers=0",
		"Include_test=0", "Include_doc=0", "Include_tcltk=1", "Include_pip=1", "Include_dev=1")
	if err != nil {
		return fmt.Errorf("%w（安装程序自己的日志在 %%TEMP%% 目录下，文件名以 Python 开头）", err)
	}
	c.Step("配置 pip")
	if c.OptBool("mirror") {
		c.UseMirror("pip")
	}
	if err := c.Check(c.J("python.exe"), "--version"); err != nil {
		return err
	}
	c.Info("命令", "python、pip（新开一个命令行窗口后生效）")
	c.Info("安装位置", c.Dir)
	return nil
}

func uninstallPython(c *core.Ctx) error {
	found, err := c.UninstallProgram(`^Python ` + regexp.QuoteMeta(c.Inst.Version) + ` \(64-bit\)$`)
	if !found && err == nil {
		c.Logf("Windows 的已安装程序列表里没有这份 Python，直接删除文件")
	}
	return err
}

// pythonHomes reads the install folders registered below a PythonCore key:
// <key>\<version>\InstallPath, default value (PEP 514).
func pythonHomes(p platform.Platform, key string) []string {
	var out []string
	for _, tag := range p.RegSubKeys(key) {
		if dir, ok := p.RegGetString(key+`\`+tag+`\InstallPath`, ""); ok && dir != "" {
			out = append(out, filepath.Clean(dir))
		}
	}
	return out
}

// ---- Node.js ----

func nodeRelease(v, label string, size int64, sha string) core.Release {
	f := "node-v" + v + "-win-x64.zip"
	return core.Release{Version: v, Label: label, File: f, Size: size, SHA256: sha, URLs: []string{
		npmBinary("node/v"+v, f),
		"https://nodejs.org/dist/v" + v + "/" + f,
	}}
}

func installNode(c *core.Ctx) error {
	if err := unzipInstall(c); err != nil {
		return err
	}
	c.Step("配置 npm")
	if c.OptBool("mirror") {
		c.UseMirror("npm")
	}
	if err := c.Check(c.J("node.exe"), "--version"); err != nil {
		return err
	}
	if c.OptBool("tools") {
		c.Step("安装 pnpm 和 yarn")
		args := []string{"install", "-g", "pnpm", "yarn", "--no-fund", "--no-audit"}
		if c.OptBool("mirror") {
			args = append(args, "--registry=https://registry.npmmirror.com/")
		}
		opts := platform.RunOpts{Timeout: 10 * time.Minute, Env: map[string]string{"PATH+": c.Dir}}
		if _, err := c.RunOpts(opts, c.J("npm.cmd"), args...); err != nil {
			c.Logf("pnpm / yarn 没有装上（不影响 Node.js 本身，可以稍后手动执行 npm i -g pnpm yarn）：%v", err)
		}
	}
	c.Info("命令", "node、npm、npx（新开一个命令行窗口后生效）")
	c.Info("全局包位置", c.J("node_modules")+"（每个 Node 版本各自一份）")
	return nil
}

// ---- Go ----

func goRelease(v, label string, size int64, sha string) core.Release {
	f := "go" + v + ".windows-amd64.zip"
	return core.Release{Version: v, Label: label, File: f, Size: size, SHA256: sha, URLs: []string{
		"https://mirrors.aliyun.com/golang/" + f,
		"https://golang.google.cn/dl/" + f,
		"https://go.dev/dl/" + f,
	}}
}

func installGo(c *core.Ctx) error {
	if err := unzipInstall(c); err != nil {
		return err
	}
	c.Step("配置")
	if c.OptBool("mirror") {
		c.UseMirror("go")
	}
	if err := c.AddPath(`%USERPROFILE%\go\bin`); err != nil {
		return err
	}
	if err := c.Check(c.J("bin", "go.exe"), "version"); err != nil {
		return err
	}
	c.Info("命令", "go（新开一个命令行窗口后生效）")
	c.Info("GOPATH", `%USERPROFILE%\go（Go 的默认位置，go install 装的工具在它的 bin 目录下）`)
	return nil
}

// ---- Git ----

const gitKey = `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\Git_is1`

func installGit(c *core.Ctx) error {
	if c.P.RegKeyExists(gitKey) {
		return fmt.Errorf("这台电脑上已经装有 Git（不是本工具装的）。可以继续用现有的；要换成这一版，请先到“环境清理”里把它清掉")
	}
	file, err := c.Fetch()
	if err != nil {
		return err
	}
	if err := c.CheckSignature(file); err != nil {
		return err
	}
	c.Step("运行安装程序")
	err = c.Installer(file, nil, "/VERYSILENT", "/NORESTART", "/NOCANCEL", "/SP-", "/SUPPRESSMSGBOXES",
		"/DIR="+c.Dir, "/o:PathOption=BashOnly")
	if err != nil {
		return err
	}
	c.Step("配置")
	git := c.J("cmd", "git.exe")
	for _, kv := range [][2]string{{"core.quotepath", "false"}, {"core.longpaths", "true"}} {
		if _, err := c.Run(git, "config", "--system", kv[0], kv[1]); err != nil {
			c.Logf("设置 %s 没有成功（不影响使用）：%v", kv[0], err)
		}
	}
	if err := c.AddPath(c.J("cmd")); err != nil {
		return err
	}
	if err := c.Check(git, "--version"); err != nil {
		return err
	}
	c.Info("命令", "git（新开一个命令行窗口后生效）；右键菜单里有 Git Bash")
	c.Info("已设置", "core.quotepath=false（中文文件名正常显示）、core.longpaths=true（支持长路径）")
	c.Info("还需要你做", `git config --global user.name "你的名字"，以及 user.email`)
	return nil
}

// innoUninstall runs the Inno Setup uninstaller left in the install folder.
func innoUninstall(c *core.Ctx) error {
	un := c.J("unins000.exe")
	if !c.Exists(un) {
		return nil
	}
	_, err := c.RunOpts(platform.RunOpts{Timeout: 15 * time.Minute}, un, "/VERYSILENT", "/NORESTART", "/SUPPRESSMSGBOXES")
	return err
}

// ---- PHP ----

func phpRelease(v, vs, label, sha string, size int64) core.Release {
	f := "php-" + v + "-nts-Win32-" + vs + "-x64.zip"
	return core.Release{Version: v, Label: label, File: f, Size: size, SHA256: sha, URLs: []string{
		"https://windows.php.net/downloads/releases/" + f,
		"https://windows.php.net/downloads/releases/archives/" + f,
	}}
}

func installPHP(c *core.Ctx) error {
	if err := unzipInstall(c); err != nil {
		return err
	}
	c.Step("生成 php.ini")
	src, _ := os.ReadFile(c.J("php.ini-development"))
	ini := core.Uncomment(string(src), ";", `extension_dir = "ext"`)
	for _, e := range []string{"curl", "exif", "fileinfo", "gd", "intl", "mbstring", "mysqli", "openssl",
		"pdo_mysql", "pdo_pgsql", "pdo_sqlite", "sodium", "sqlite3", "zip"} {
		ini = core.Uncomment(ini, ";", "extension="+e)
	}
	ini = setConf(ini, "date.timezone", " = ", "Asia/Shanghai")
	if err := c.Write(c.J("php.ini"), ini); err != nil {
		return err
	}
	if c.OptBool("composer") {
		c.Step("安装 Composer")
		phar, err := c.FetchRelease(core.Release{Version: "latest", File: "composer.phar", URLs: []string{
			"https://mirrors.aliyun.com/composer/composer.phar",
			"https://getcomposer.org/download/latest-stable/composer.phar",
		}})
		if err == nil {
			err = c.CopyFile(phar, c.J("composer.phar"))
		}
		if err == nil {
			err = c.Write(c.J("composer.bat"), "@echo off\r\n\"%~dp0php.exe\" \"%~dp0composer.phar\" %*\r\n")
		}
		if err != nil {
			c.Logf("Composer 没有装上（不影响 PHP 本身）：%v", err)
		} else if c.OptBool("mirror") {
			_, err := c.Run(c.J("php.exe"), c.J("composer.phar"), "config", "-g", "repo.packagist", "composer", "https://mirrors.aliyun.com/composer/")
			if err != nil {
				c.Logf("Composer 镜像没有设置成功（不影响使用）：%v", err)
			}
		}
	}
	if err := c.Check(c.J("php.exe"), "-v"); err != nil {
		return err
	}
	c.Info("命令", "php、composer（新开一个命令行窗口后生效）")
	c.Info("配置文件", c.J("php.ini"))
	c.Info("内置网页服务器", "在项目目录里执行 php -S localhost:8000")
	return nil
}

// ---- .NET SDK ----

func installDotnet(c *core.Ctx) error {
	name := `^Microsoft \.NET SDK ` + regexp.QuoteMeta(c.Rel.Version) + ` \(x64\)`
	if have, ok := c.HasProgram(name); ok {
		return fmt.Errorf("这台电脑上已经装有 %s，不需要再装", have)
	}
	file, err := c.Fetch()
	if err != nil {
		return err
	}
	if err := c.CheckSignature(file); err != nil {
		return err
	}
	c.Step("运行安装程序")
	if err := c.Installer(file, []int{3010}, "/install", "/quiet", "/norestart"); err != nil {
		return err
	}
	dotnet := filepath.Join(c.P.Dir("programfiles"), "dotnet", "dotnet.exe")
	note := ".NET SDK " + c.Rel.Version + " 由微软官方安装程序装在 " + filepath.Dir(dotnet) + "。\r\n这个文件夹只是 DevNest 的记录。\r\n"
	if err := c.Write(c.J("README.txt"), note); err != nil {
		return err
	}
	if err := c.Check(dotnet, "--version"); err != nil {
		return err
	}
	c.Info("命令", "dotnet（新开一个命令行窗口后生效）")
	c.Info("安装位置", filepath.Dir(dotnet)+"（微软安装程序的固定位置，不能改）")
	return nil
}

func uninstallDotnet(c *core.Ctx) error {
	_, err := c.UninstallProgram(`^Microsoft \.NET SDK ` + regexp.QuoteMeta(c.Inst.Version) + ` \(x64\)`)
	return err
}

// ---- Rust ----

func installRust(c *core.Ctx) error {
	cargoBin := filepath.Join(c.P.Dir("home"), ".cargo", "bin")
	if c.Exists(filepath.Join(cargoBin, "rustup.exe")) {
		return fmt.Errorf("这台电脑上已经装有 Rust（%s）。升级请用 rustup update；要重装请先到“环境清理”里清掉", cargoBin)
	}
	file, err := c.Fetch()
	if err != nil {
		return err
	}
	c.Step("下载并安装工具链（几百 MB，需要几分钟）")
	if c.OptBool("mirror") {
		c.UseMirror("cargo")
	}
	_, err = c.RunOpts(platform.RunOpts{Timeout: time.Hour}, file, "-y", "--default-host", c.Opt("host"),
		"--default-toolchain", "stable", "--profile", "default")
	if err != nil {
		return err
	}
	note := "Rust 按官方惯例装在当前用户目录下的 .cargo 和 .rustup。\r\n这个文件夹只是 DevNest 的记录。\r\n"
	if err := c.Write(c.J("README.txt"), note); err != nil {
		return err
	}
	if err := c.Check(filepath.Join(cargoBin, "rustc.exe"), "--version"); err != nil {
		return err
	}
	c.Info("命令", "rustc、cargo、rustup（新开一个命令行窗口后生效）")
	c.Info("安装位置", filepath.Dir(cargoBin)+" 和同级的 .rustup")
	c.Info("升级", "rustup update")
	return nil
}

func uninstallRust(c *core.Ctx) error {
	rustup := filepath.Join(c.P.Dir("home"), ".cargo", "bin", "rustup.exe")
	if !c.Exists(rustup) {
		return nil
	}
	_, err := c.RunOpts(platform.RunOpts{Timeout: 10 * time.Minute}, rustup, "self", "uninstall", "-y")
	return err
}

func init() {
	core.Register(&core.Package{
		ID: "python", Name: "Python", Category: "lang",
		Desc:     "Python 解释器和 pip。可以同时装多个版本并一键切换",
		Homepage: "https://www.python.org/",
		Notes:    []string{"用的是 python.org 的官方安装程序（从国内镜像下载，运行前核对数字签名）。"},
		Releases: []core.Release{
			pythonRelease("3.14.10", "3.14.10（推荐）", 33498520),
			pythonRelease("3.15.2", "3.15.2（最新）", 37694360),
			pythonRelease("3.13.17", "3.13.17", 29941280),
		},
		Options: []core.Option{mirrorOption("pip 用清华源")},
		Multi:   true, HomeVar: "PYTHON_HOME", PathSubs: []string{"", "Scripts"},
		Probe:     []string{"python.exe", "Scripts/pip.exe"},
		Steps:     2,
		Install:   installPython,
		Uninstall: uninstallPython,
		Detect: core.Detect{
			Uninstall: []string{`^Python \d+\.\d+\.\d+\S* \((64|32)-bit\)$`, `^Python Launcher$`},
			EnvVars:   []string{"PYTHON_HOME", "PYTHONHOME", "PYTHONPATH"},
			PathRx:    []string{`(?i)\\Python\d*(-32)?\\?(Scripts\\?)?$`, `(?i)\\Python\\Python\d+(-32)?\\?(Scripts\\?)?$`, `(?i)\\Python\\Launcher\\?$`},
			Dirs:      []string{`?:\Python3?`, `?:\Python3??`, `%LOCALAPPDATA%\Programs\Python`, `%ProgramFiles%\Python3?`, `%ProgramFiles%\Python3??`},
			UserDirs:  []string{`%APPDATA%\pip`, `%LOCALAPPDATA%\pip`, `%APPDATA%\Python`},
			RegKeys:   []string{`HKLM\SOFTWARE\Python\PythonCore`, `HKCU\SOFTWARE\Python\PythonCore`},
			RegHomes:  pythonHomes,
		},
	})

	core.Register(&core.Package{
		ID: "node", Name: "Node.js", Category: "lang",
		Desc:     "JavaScript 运行环境，带 npm；可选装 pnpm 和 yarn。可以同时装多个版本并一键切换",
		Homepage: "https://nodejs.org/",
		Releases: []core.Release{
			nodeRelease("24.22.3", "24.22.3（LTS，推荐）", 37762649, "e612af99f626ab415ea225af6bce5c971feb448cb5cae250bb1c3fa50c5f7d9f"),
			nodeRelease("26.13.1", "26.13.1（最新）", 41166246, "e2edda75bfb089654705cf65a13b58933bfd5f57148e8ebda916f58a2474f1f7"),
			nodeRelease("22.23.5", "22.23.5（旧 LTS）", 35630346, "4a191082e9706866830379d577d2cc8a50c776fcd9436a8d061b063ed0d9a0e3"),
		},
		Options: []core.Option{
			mirrorOption("npm 用 npmmirror"),
			{Key: "tools", Label: "同时安装 pnpm 和 yarn", Type: "bool", Default: "true"},
		},
		Multi: true, HomeVar: "NODE_HOME", PathSubs: []string{""},
		Probe:   []string{"node.exe", "npm.cmd"},
		Steps:   3,
		Install: installNode,
		Detect: core.Detect{
			Uninstall: []string{`^Node\.js$`, `^NVM for Windows`},
			EnvVars:   []string{"NODE_HOME", "NVM_HOME", "NVM_SYMLINK", "NODE_PATH"},
			PathRx:    []string{`(?i)\\nodejs\\?$`, `(?i)\\nvm\\?$`, `(?i)\\AppData\\Roaming\\npm\\?$`, `(?i)^%NVM_(HOME|SYMLINK)%$`},
			Dirs:      []string{`%ProgramFiles%\nodejs`, `%APPDATA%\nvm`},
			UserDirs:  []string{`%APPDATA%\npm`, `%APPDATA%\npm-cache`, `%LOCALAPPDATA%\npm-cache`, `%LOCALAPPDATA%\pnpm`, `%LOCALAPPDATA%\Yarn`},
		},
	})

	core.Register(&core.Package{
		ID: "go", Name: "Go", Category: "lang",
		Desc:     "Go 语言工具链。可以同时装多个版本并一键切换",
		Homepage: "https://go.dev/",
		Releases: []core.Release{
			goRelease("1.27.4", "1.27.4（推荐）", 79077744, "178ab98950ac69ab25069307dc6ade79f3816e414cce95b4da166a115cde1e7e"),
			goRelease("1.26.11", "1.26.11", 75074581, "71ae086f0137e665f855a811cc119152b6a783e0bbc1b906e709f46d80141eb0"),
		},
		Options: []core.Option{mirrorOption("GOPROXY 用 goproxy.cn")},
		Multi:   true, HomeVar: "GO_HOME", PathSubs: []string{"bin"},
		Probe:   []string{"bin/go.exe", "bin/gofmt.exe"},
		Steps:   2,
		Install: installGo,
		Detect: core.Detect{
			Uninstall: []string{`^Go Programming Language`},
			EnvVars:   []string{"GO_HOME", "GOROOT", "GOPATH", "GOPROXY"},
			PathRx:    []string{`(?i)\\Go\\bin\\?$`},
			Dirs:      []string{`%ProgramFiles%\Go`, `?:\Go`},
			UserDirs:  []string{`%USERPROFILE%\go`, `%LOCALAPPDATA%\go-build`, `%APPDATA%\go`},
		},
	})

	gitFile := "Git-2.56.1-64-bit.exe"
	core.Register(&core.Package{
		ID: "git", Name: "Git", Category: "tool",
		Desc:     "版本管理工具，带 Git Bash。装好后中文文件名正常显示、支持长路径",
		Homepage: "https://git-scm.com/",
		Notes:    []string{"用的是 Git for Windows 官方安装程序（从国内镜像下载，运行前核对数字签名）。"},
		Releases: []core.Release{{Version: "2.56.1", File: gitFile, Size: 66870448, URLs: []string{
			npmBinary("git-for-windows/v2.56.1.windows.1", gitFile),
			"https://mirrors.huaweicloud.com/git-for-windows/v2.56.1.windows.1/" + gitFile,
			gh("git-for-windows/git", "v2.56.1.windows.1", gitFile),
		}}},
		Probe:     []string{"cmd/git.exe", "git-bash.exe"},
		Steps:     2,
		Install:   installGit,
		Uninstall: innoUninstall,
		Detect: core.Detect{
			Uninstall: []string{`^Git( version .*)?$`},
			PathRx:    []string{`(?i)\\Git\\(cmd|bin|usr\\bin|mingw64\\bin)\\?$`},
			Dirs:      []string{`%ProgramFiles%\Git`},
		},
	})

	core.Register(&core.Package{
		ID: "php", Name: "PHP", Category: "lang",
		Desc:     "PHP 命令行和 Composer，常用扩展已打开（curl、gd、mbstring、mysqli、pdo_mysql、openssl、zip 等）",
		Homepage: "https://windows.php.net/",
		Notes:    []string{"装的是 NTS（非线程安全）版，适合命令行、内置服务器和 Nginx + php-cgi；不适合 Apache 的 mod_php。"},
		Releases: []core.Release{
			phpRelease("8.4.27-1", "vs17", "8.4.27（推荐）", "dbae154a0a89791f36c3d48e68bae797878333e7d6cb5721e74617de59b63ab7", 35296081),
			phpRelease("8.3.36-1", "vs16", "8.3.36", "1a097d52b7a68622cdc723073b61cc70caf79c941218ff65b2d8441dbc6b10d4", 34179998),
		},
		Options: []core.Option{
			{Key: "composer", Label: "同时安装 Composer", Type: "bool", Default: "true"},
			mirrorOption("Composer 用阿里云"),
		},
		Requires: []core.Dep{{ID: "vcredist"}},
		Multi:    true, HomeVar: "PHP_HOME", PathSubs: []string{""},
		Probe:   []string{"php.exe", "php-cgi.exe"},
		Steps:   3,
		Install: installPHP,
		Detect: core.Detect{
			EnvVars:  []string{"PHP_HOME", "PHPRC"},
			PathRx:   []string{`(?i)\\php[^\\]*\\?$`, `(?i)\\Composer\\vendor\\bin\\?$`},
			Dirs:     []string{`?:\php`, `?:\php7*`, `?:\php8*`, `?:\php-7*`, `?:\php-8*`, `%ProgramFiles%\PHP`},
			UserDirs: []string{`%APPDATA%\Composer`},
		},
	})

	core.Register(&core.Package{
		ID: "dotnet", Name: ".NET SDK", Category: "lang",
		Desc:     "C# / .NET 开发工具包（dotnet 命令）",
		Homepage: "https://dotnet.microsoft.com/",
		Notes: []string{
			"用微软官方安装程序，固定装在 C:\\Program Files\\dotnet，不会装到本工具的目录里。",
			"“环境清理”不会扫描 .NET：很多普通软件依赖它的运行库，误删会让那些软件打不开。",
		},
		Releases: []core.Release{{Version: "10.0.404", Label: "10.0.404（.NET 10 LTS）", File: "dotnet-sdk-10.0.404-win-x64.exe", Size: 218476784,
			URLs: []string{"https://builds.dotnet.microsoft.com/dotnet/Sdk/10.0.404/dotnet-sdk-10.0.404-win-x64.exe"}}},
		Probe:     []string{"README.txt"},
		Steps:     1,
		Install:   installDotnet,
		Uninstall: uninstallDotnet,
	})

	core.Register(&core.Package{
		ID: "rust", Name: "Rust", Category: "lang",
		Desc:     "Rust 工具链（rustup、rustc、cargo），按官方方式用 rustup 安装最新稳定版",
		Homepage: "https://www.rust-lang.org/",
		Notes: []string{
			"按 Rust 的惯例装在当前用户目录下（.cargo 和 .rustup），不在本工具的目录里。",
			"MSVC 版需要另外装 Visual Studio 生成工具（C++ 桌面开发）才能编译；GNU 版不需要。",
		},
		Releases: []core.Release{{Version: "stable", Label: "最新稳定版", File: "rustup-init.exe", URLs: []string{
			"https://static.rust-lang.org/rustup/dist/x86_64-pc-windows-msvc/rustup-init.exe",
			"https://mirrors.ustc.edu.cn/rust-static/rustup/dist/x86_64-pc-windows-msvc/rustup-init.exe",
		}}},
		Options: []core.Option{
			{Key: "host", Label: "工具链类型", Type: "select", Default: "x86_64-pc-windows-gnu", Choices: []core.Choice{
				{Value: "x86_64-pc-windows-gnu", Label: "GNU（不需要 Visual Studio）"},
				{Value: "x86_64-pc-windows-msvc", Label: "MSVC（需要先装 Visual Studio 生成工具）"},
			}},
			mirrorOption("rustup 和 crates.io"),
		},
		Probe:     []string{"README.txt"},
		Steps:     1,
		Install:   installRust,
		Uninstall: uninstallRust,
		Detect: core.Detect{
			Uninstall: []string{`^Rustup: the Rust toolchain installer$`},
			EnvVars:   []string{"CARGO_HOME", "RUSTUP_HOME", "RUSTUP_DIST_SERVER", "RUSTUP_UPDATE_ROOT"},
			PathRx:    []string{`(?i)\\\.cargo\\bin\\?$`},
			Dirs:      []string{`%USERPROFILE%\.cargo`, `%USERPROFILE%\.rustup`},
		},
	})

	mingwFile := "winlibs-x86_64-posix-seh-gcc-16.2.0-mingw-w64ucrt-14.0.0-r3.zip"
	core.Register(&core.Package{
		ID: "mingw", Name: "MinGW-w64（GCC）", Category: "lang",
		Desc:     "C / C++ 编译器（gcc、g++、gdb、mingw32-make），来自 WinLibs 的绿色包",
		Homepage: "https://winlibs.com/",
		Notes:    []string{"只能从 GitHub 下载，国内可能很慢；可以在设置里填 GitHub 加速地址，或者手动下载后用“导入安装包”。"},
		Releases: []core.Release{{Version: "16.2.0", Label: "GCC 16.2.0", File: mingwFile, Size: 276414382,
			URLs: []string{gh("brechtsanders/winlibs_mingw", "16.2.0posix-23.1.3-14.0.0-ucrt-r3", mingwFile)}}},
		Probe: []string{"bin/gcc.exe", "bin/g++.exe"},
		Steps: 1,
		Install: func(c *core.Ctx) error {
			if err := unzipInstall(c); err != nil {
				return err
			}
			if err := c.AddPath(c.J("bin")); err != nil {
				return err
			}
			c.Info("命令", "gcc、g++、gdb、mingw32-make（新开一个命令行窗口后生效）")
			return c.Check(c.J("bin", "gcc.exe"), "--version")
		},
		Detect: core.Detect{
			PathRx: []string{`(?i)\\mingw(32|64)?\\bin\\?$`},
			Dirs:   []string{`?:\mingw64`, `?:\mingw32`, `?:\MinGW`},
		},
	})
}
