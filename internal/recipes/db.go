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

func mysqlRelease(version, label string, size int64) core.Release {
	series := core.MinorKey(version)
	file := "mysql-" + version + "-winx64.zip"
	// No mainland mirror carries the Windows zip (USTC, Aliyun, Huawei, TUNA,
	// 163 and Sohu were checked), so only Oracle's CDN is listed. A release
	// moves from Downloads to archives once a newer one is out.
	return core.Release{Version: version, Label: label, File: file, Size: size, URLs: []string{
		"https://cdn.mysql.com/Downloads/MySQL-" + series + "/" + file,
		"https://cdn.mysql.com/archives/mysql-" + series + "/" + file,
	}}
}

func installMySQL(c *core.Ctx) error {
	port, svc := c.OptInt("port"), c.Opt("service")
	if err := c.NeedPort(port); err != nil {
		return err
	}
	if err := c.NeedNoService(svc); err != nil {
		return err
	}
	if err := unzipInstall(c); err != nil {
		return err
	}

	c.Step("写入 my.ini")
	auth := ""
	if c.OptBool("native") {
		auth = "default_authentication_plugin=mysql_native_password"
		if core.CompareVersions(c.Rel.Version, "8.4") >= 0 {
			auth = "mysql_native_password=ON"
		}
	}
	errLog := filepath.Join(c.M.Root(), "logs", svc+"-error.log")
	ini := c.J("my.ini")
	err := c.Write(ini, fmt.Sprintf(`# 由 DevNest 生成。修改后在服务面板里重启 %s 生效。
[mysqld]
port=%d
basedir=%s
datadir=%s
bind-address=%s
character-set-server=utf8mb4
default-time-zone='+08:00'
max_connections=200
max_allowed_packet=64M
log-error=%s
%s

[mysql]
default-character-set=utf8mb4

[client]
port=%d
default-character-set=utf8mb4
`, svc, port, core.Slash(c.Dir), core.Slash(c.DataDir), listenAddr(c), core.Slash(errLog), auth, port))
	if err != nil {
		return err
	}
	c.LogFile(errLog)

	mysqld := c.J("bin", "mysqld.exe")
	fresh := !c.DataExisted
	if fresh {
		c.Step("初始化数据目录")
		if err := os.MkdirAll(filepath.Dir(c.DataDir), 0o755); err != nil {
			return err
		}
		if _, err := c.Run(mysqld, "--defaults-file="+ini, "--initialize-insecure", "--console"); err != nil {
			return fmt.Errorf("初始化数据目录失败：%w", err)
		}
	} else {
		c.Logf("数据目录 %s 里已经有数据：沿用它，不重新初始化，root 密码保持原来的", c.DataDir)
	}

	c.Step("注册并启动服务")
	if _, err := c.Run(mysqld, "--install", svc, "--defaults-file="+ini); err != nil {
		return fmt.Errorf("注册服务失败：%w", err)
	}
	c.AddService(svc, mysqld, core.PortRef{Name: "MySQL", Port: port})
	if err := c.StartService(svc, port); err != nil {
		return fmt.Errorf("%w。详细原因见 %s", err, errLog)
	}

	if fresh {
		c.Step("设置 root 密码")
		pw := c.Opt("password")
		with := ""
		if c.OptBool("native") {
			with = "WITH mysql_native_password "
		}
		sql := fmt.Sprintf("ALTER USER 'root'@'localhost' IDENTIFIED %sBY '%s';\n", with, pw)
		if c.OptBool("lan") {
			sql += fmt.Sprintf("CREATE USER IF NOT EXISTS 'root'@'%%' IDENTIFIED %sBY '%s';\nGRANT ALL PRIVILEGES ON *.* TO 'root'@'%%' WITH GRANT OPTION;\n", with, pw)
		}
		sql += "FLUSH PRIVILEGES;\n"
		var lastErr error
		for try := 0; try < 6; try++ {
			_, lastErr = c.RunOpts(platform.RunOpts{Stdin: sql, Timeout: time.Minute}, c.J("bin", "mysql.exe"),
				"--no-defaults", "--protocol=TCP", "-h127.0.0.1", fmt.Sprintf("-P%d", port), "-uroot", "--skip-password")
			if lastErr == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
		if lastErr != nil {
			return fmt.Errorf("设置 root 密码失败：%w", lastErr)
		}
	}
	if c.OptBool("lan") {
		c.OpenFirewall(port)
	}
	c.Info("连接地址", fmt.Sprintf("127.0.0.1:%d", port))
	if fresh {
		c.Info("账号", "root（密码是安装时填的那个）")
	} else {
		c.Info("账号", "沿用原有数据里的账号和密码")
	}
	c.Info("服务名", svc)
	c.Info("配置文件", ini)
	c.Info("数据目录", c.DataDir)
	return nil
}

func installRedis(c *core.Ctx) error {
	port := c.OptInt("port")
	if err := c.NeedPort(port); err != nil {
		return err
	}
	if err := c.NeedNoService("Redis"); err != nil {
		return err
	}
	if err := unzipInstall(c); err != nil {
		return err
	}
	c.Step("修改 redis.conf")
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return err
	}
	conf := c.J("redis.conf")
	err := c.Edit(conf, func(s string) string {
		s = setConf(s, "port", " ", fmt.Sprint(port))
		s = setConf(s, "bind", " ", listenAddr(c))
		s = setConf(s, "dir", " ", core.Slash(c.DataDir))
		s = setConf(s, "logfile", " ", `""`)
		s = setConf(s, "appendonly", " ", "yes")
		if pw := c.Opt("password"); pw != "" {
			s = setConf(s, "requirepass", " ", pw)
			s = setConf(s, "protected-mode", " ", "yes")
		} else if c.OptBool("lan") {
			s = setConf(s, "protected-mode", " ", "no")
		}
		return s
	})
	if err != nil {
		return err
	}
	c.Step("注册并启动服务")
	err = c.WrapService(core.WrapSpec{
		Name: "Redis", DisplayName: "Redis " + c.Rel.Version,
		// The config file is named relative to the working folder: this build
		// runs on the MSYS2 runtime, where Redis takes "C:/..." for a relative
		// path and looks for it under the current folder.
		Exe: c.J("redis-server.exe"), Args: []string{"redis.conf"}, Dir: c.Dir,
		StopTimeout: 20, Ports: []core.PortRef{{Name: "Redis", Port: port}},
	})
	if err != nil {
		return err
	}
	if err := c.AddPath(c.Dir); err != nil {
		return err
	}
	if c.OptBool("lan") {
		c.OpenFirewall(port)
	}
	c.Info("连接地址", fmt.Sprintf("127.0.0.1:%d", port))
	if c.Opt("password") == "" {
		c.Info("密码", "无")
	} else {
		c.Info("密码", "安装时填的那个（也写在 redis.conf 的 requirepass 里）")
	}
	c.Info("命令行客户端", "redis-cli -p "+fmt.Sprint(port))
	c.Info("配置文件", conf)
	c.Info("数据目录", c.DataDir)
	return c.StartService("Redis", port)
}

func installNeo4j(c *core.Ctx) error {
	http, bolt := c.OptInt("http"), c.OptInt("bolt")
	for _, p := range []int{http, bolt} {
		if err := c.NeedPort(p); err != nil {
			return err
		}
	}
	if err := c.NeedNoService("Neo4j"); err != nil {
		return err
	}
	jdk, err := c.Dep(jdk21)
	if err != nil {
		return err
	}
	if err := unzipInstall(c); err != nil {
		return err
	}
	c.Step("修改 neo4j.conf")
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return err
	}
	conf := c.J("conf", "neo4j.conf")
	err = c.Edit(conf, func(s string) string {
		s = setConf(s, "server.directories.data", "=", core.Slash(c.DataDir))
		s = setConf(s, "server.http.listen_address", "=", fmt.Sprintf(":%d", http))
		s = setConf(s, "server.bolt.listen_address", "=", fmt.Sprintf(":%d", bolt))
		// "neo4j console" refuses to start while a Windows service of this
		// name is running, taking it for a second copy of itself. The default
		// name is neo4j, which is the very service that runs the console here.
		s = setConf(s, "server.windows_service_name", "=", "neo4j-native")
		if c.OptBool("lan") {
			s = setConf(s, "server.default_listen_address", "=", "0.0.0.0")
		}
		return s
	})
	if err != nil {
		return err
	}
	env := map[string]string{"JAVA_HOME": jdk.Dir, "NEO4J_HOME": c.Dir, "PATH+": filepath.Join(jdk.Dir, "bin")}
	if !c.DataExisted {
		c.Step("设置初始密码")
		if _, err := c.RunOpts(platform.RunOpts{Env: env}, c.J("bin", "neo4j-admin.bat"), "dbms", "set-initial-password", c.Opt("password")); err != nil {
			return fmt.Errorf("设置初始密码失败：%w", err)
		}
	} else {
		c.Logf("数据目录 %s 里已经有数据：沿用它，密码保持原来的", c.DataDir)
	}
	c.Step("注册并启动服务")
	err = c.WrapService(core.WrapSpec{
		Name: "Neo4j", DisplayName: "Neo4j " + c.Rel.Version,
		Exe: c.J("bin", "neo4j.bat"), Args: []string{"console"}, Env: env, StopTimeout: 60,
		Ports: []core.PortRef{{Name: "浏览器 HTTP", Port: http}, {Name: "Bolt", Port: bolt}},
	})
	if err != nil {
		return err
	}
	if c.OptBool("lan") {
		c.OpenFirewall(http)
		c.OpenFirewall(bolt)
	}
	c.Info("浏览器界面", localURL(http))
	c.Info("Bolt 连接", fmt.Sprintf("bolt://localhost:%d", bolt))
	c.Info("账号", "neo4j（密码是安装时填的那个）")
	c.Info("使用的 JDK", jdk.Dir)
	c.Info("配置文件", conf)
	c.Info("数据目录", c.DataDir)
	c.LogFile(c.J("logs", "neo4j.log"))
	return c.StartService("Neo4j", bolt)
}

func neo4jRelease(version, label string, size int64) core.Release {
	file := "neo4j-community-" + version + "-windows.zip"
	return core.Release{Version: version, Label: label, File: file, Size: size, URLs: []string{
		"https://we-yun.com/doc/neo4j/" + version + "/" + file,
		"https://dist.neo4j.org/" + file,
	}}
}

func init() {
	core.Register(&core.Package{
		ID: "mysql", Name: "MySQL", Category: "db",
		Desc:     "最常用的关系型数据库。自动初始化数据目录、注册 Windows 服务、设置 root 密码，字符集 utf8mb4",
		Homepage: "https://dev.mysql.com/",
		Releases: []core.Release{
			mysqlRelease("8.0.46", "8.0.46（8.0 系列）", 248009349),
			mysqlRelease("8.4.11", "8.4.11（LTS）", 281191914),
		},
		Options: []core.Option{
			portOption("port", "端口", "3306", "MySQL"),
			{Key: "password", Label: "root 密码", Type: "password", Default: "root", MinLen: 4,
				Help: "默认是 root，只适合本机开发；建议改成自己的密码"},
			{Key: "service", Label: "服务名", Type: "text", Default: "MySQL", Pattern: `^[A-Za-z][A-Za-z0-9_]{1,30}$`,
				Help: "字母开头，只含字母、数字、下划线"},
			lanOption,
			{Key: "native", Label: "兼容老客户端（mysql_native_password）", Type: "bool", Default: "false",
				Help: "Navicat 11、老版 PHP/驱动连不上时才需要勾选"},
		},
		Requires: []core.Dep{{ID: "vcredist"}},
		HomeVar:  "MYSQL_HOME", PathSubs: []string{"bin"},
		Probe:    []string{"bin/mysqld.exe", "bin/mysql.exe"},
		DataName: func(r core.Release) string { return "mysql-" + core.MinorKey(r.Version) },
		Steps:    5,
		Install:  installMySQL,
		Detect: core.Detect{
			ServiceName: []string{`(?i)^mysql`},
			ServiceBin:  []string{`(?i)\\mysqld(-nt)?\.exe`},
			Uninstall:   []string{`(?i)^MySQL (Server|Installer|Router|Notifier|Examples|Documents)`},
			EnvVars:     []string{"MYSQL_HOME"},
			PathRx:      []string{`(?i)\\mysql[^\\]*(\\[^\\]+)*\\bin\\?$`, `(?i)%MYSQL_HOME%`},
			Dirs:        []string{`%ProgramFiles%\MySQL`, `%ProgramFiles(x86)%\MySQL`, `?:\mysql*`},
			DataDirs:    []string{`%ProgramData%\MySQL`, "data", "Data"},
			RegKeys:     []string{`HKLM\SOFTWARE\MySQL AB`, `HKLM\SOFTWARE\WOW6432Node\MySQL AB`},
			Procs:       []string{"mysqld.exe"},
		},
	})

	core.Register(&core.Package{
		ID: "redis", Name: "Redis", Category: "db",
		Desc:     "内存键值数据库 / 缓存。注册为 Windows 服务，开启 AOF 持久化",
		Homepage: "https://github.com/redis-windows/redis-windows",
		Notes:    []string{"Redis 官方不提供 Windows 版，这里用的是社区项目 redis-windows 编译的版本，从 GitHub 下载。"},
		Releases: []core.Release{{
			Version: "8.10.3", File: "Redis-8.10.3-Windows-x64-msys2.zip", Size: 14036243,
			URLs: []string{gh("redis-windows/redis-windows", "8.10.3", "Redis-8.10.3-Windows-x64-msys2.zip")},
		}},
		Options: []core.Option{
			portOption("port", "端口", "6379", "Redis"),
			{Key: "password", Label: "访问密码", Type: "password", Default: "", Help: "留空表示不设密码（只允许本机访问时可以留空）"},
			lanOption,
		},
		Probe:    []string{"redis-server.exe", "redis-cli.exe", "redis.conf"},
		DataName: func(core.Release) string { return "redis" },
		Steps:    3,
		Install:  installRedis,
		Detect: core.Detect{
			ServiceName: []string{`(?i)^redis`},
			ServiceBin:  []string{`(?i)\\redis-server\.exe`, `(?i)\\RedisService\.exe`},
			Uninstall:   []string{`(?i)^Redis( on Windows| for Windows)?$`, `(?i)^Memurai`},
			PathRx:      []string{`(?i)\\redis[^\\]*\\?$`},
			Dirs:        []string{`%ProgramFiles%\Redis`, `?:\Redis*`},
			Procs:       []string{"redis-server.exe"},
		},
	})

	core.Register(&core.Package{
		ID: "neo4j", Name: "Neo4j", Category: "db",
		Desc:     "图数据库（社区版）。自动设置初始密码、注册 Windows 服务，需要 JDK 21（没有会自动先装）",
		Homepage: "https://neo4j.com/",
		Notes: []string{"Neo4j 官方下载站在中国大陆无法访问，这里从国内镜像 we-yun.com 下载；镜像不提供校验值，只能核对文件大小。",
			"首次启动要半分钟左右。"},
		Releases: []core.Release{
			neo4jRelease("2026.10.2", "2026.10.2（最新）", 246302174),
			neo4jRelease("5.26.33", "5.26.33（LTS）", 139609118),
		},
		Options: []core.Option{
			portOption("http", "浏览器端口（HTTP）", "7474", "HTTP"),
			portOption("bolt", "Bolt 端口", "7687", "Bolt"),
			{Key: "password", Label: "neo4j 用户的密码", Type: "password", Default: "neo4j123", MinLen: 8, Help: "至少 8 位，不能是 neo4j"},
			lanOption,
		},
		Requires: []core.Dep{jdk21},
		HomeVar:  "NEO4J_HOME", PathSubs: []string{"bin"},
		Probe:    []string{"bin/neo4j.bat", "bin/neo4j-admin.bat", "conf/neo4j.conf"},
		DataName: func(core.Release) string { return "neo4j" },
		Steps:    4,
		Install:  installNeo4j,
		Detect: core.Detect{
			ServiceName: []string{`(?i)^neo4j`},
			Uninstall:   []string{`(?i)^Neo4j`},
			EnvVars:     []string{"NEO4J_HOME"},
			PathRx:      []string{`(?i)\\neo4j[^\\]*\\bin\\?$`, `(?i)%NEO4J_HOME%`},
			Dirs:        []string{`?:\neo4j*`, `%ProgramFiles%\Neo4j*`},
			DataDirs:    []string{"data"},
			UserDirs:    []string{`%USERPROFILE%\.Neo4jDesktop*`},
		},
	})

	core.Register(&core.Package{
		ID: "vcredist", Name: "VC++ 运行库", Category: "dep",
		Desc:     "Microsoft Visual C++ 2015–2022 运行库（x64）。MySQL、PostgreSQL、MongoDB、PHP 等需要它，缺少时会自动安装",
		Homepage: "https://learn.microsoft.com/cpp/windows/latest-supported-vc-redist",
		Releases: []core.Release{{Version: "14", Label: "2015–2022 最新版", File: "vc_redist.x64.exe",
			URLs: []string{"https://aka.ms/vs/17/release/vc_redist.x64.exe"}}},
		Steps: 1,
		Install: func(c *core.Ctx) error {
			sys32 := filepath.Join(c.P.Dir("systemroot"), "System32")
			missing := []string{}
			for _, dll := range []string{"vcruntime140.dll", "vcruntime140_1.dll", "msvcp140.dll"} {
				if !c.Exists(filepath.Join(sys32, dll)) {
					missing = append(missing, dll)
				}
			}
			if len(missing) == 0 && !c.Sim() {
				c.Logf("系统里已经有 VC++ 运行库，不用再装")
				return nil
			}
			c.Logf("缺少 %s", strings.Join(missing, "、"))
			file, err := c.Fetch()
			if err != nil {
				return err
			}
			if err := c.CheckSignature(file); err != nil {
				return err
			}
			c.Step("安装运行库")
			// 3010: installed, reboot recommended. 1638: a newer version is present.
			return c.Installer(file, []int{3010, 1638}, "/install", "/quiet", "/norestart")
		},
	})
}
