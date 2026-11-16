package recipes

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"devnest/internal/core"
	"devnest/internal/platform"
)

// networkService is the well-known SID of "NT AUTHORITY\NetworkService",
// written as a SID so it works on any display language of Windows.
const networkService = "*S-1-5-20"

// ---- PostgreSQL ----

func pgRelease(v, label string, size int64) core.Release {
	f := "postgresql-" + v + "-1-windows-x64-binaries.zip"
	return core.Release{Version: v, Label: label, File: f, Size: size,
		URLs: []string{"https://get.enterprisedb.com/postgresql/" + f}}
}

func installPostgres(c *core.Ctx) error {
	const svc = "PostgreSQL"
	port := c.OptInt("port")
	if err := c.NeedPort(port); err != nil {
		return err
	}
	if err := c.NeedNoService(svc); err != nil {
		return err
	}
	if err := unzipInstall(c); err != nil {
		return err
	}

	fresh := !c.DataExisted
	if fresh {
		c.Step("初始化数据目录")
		if err := os.MkdirAll(filepath.Dir(c.DataDir), 0o755); err != nil {
			return err
		}
		pwFile, done, err := c.TempFile("pw.txt", c.Opt("password")+"\n")
		if err != nil {
			return err
		}
		_, err = c.Run(c.J("bin", "initdb.exe"), "-D", c.DataDir, "-U", "postgres", "-E", "UTF8", "--no-locale",
			"-A", "scram-sha-256", "--pwfile="+pwFile)
		done()
		if err != nil {
			return fmt.Errorf("初始化数据目录失败：%w", err)
		}
	} else {
		c.Logf("数据目录 %s 里已经有数据：沿用它，不重新初始化，账号和密码保持原来的", c.DataDir)
	}

	c.Step("写配置")
	conf := filepath.Join(c.DataDir, "postgresql.conf")
	err := c.Edit(conf, func(s string) string {
		addr := "'127.0.0.1'"
		if c.OptBool("lan") {
			addr = "'*'"
		}
		s = setConf(s, "port", " = ", fmt.Sprint(port))
		s = setConf(s, "listen_addresses", " = ", addr)
		s = setConf(s, "logging_collector", " = ", "on")
		s = setConf(s, "log_directory", " = ", "'log'")
		s = setConf(s, "log_filename", " = ", "'postgresql-%a.log'")
		s = setConf(s, "log_truncate_on_rotation", " = ", "on")
		return s
	})
	if err != nil {
		return err
	}
	if fresh && c.OptBool("lan") {
		err := c.Edit(filepath.Join(c.DataDir, "pg_hba.conf"), func(s string) string {
			return s + "\n# DevNest：允许局域网用密码登录\nhost    all    all    0.0.0.0/0    scram-sha-256\n"
		})
		if err != nil {
			return err
		}
	}

	c.Step("注册并启动服务")
	// The service runs as NetworkService (pg_ctl's default), which needs
	// to own its data and to read the programs.
	for _, g := range [][2]string{{c.DataDir, "(OI)(CI)F"}, {c.Dir, "(OI)(CI)RX"}} {
		if _, err := c.Run("icacls.exe", g[0], "/grant", networkService+":"+g[1], "/Q"); err != nil {
			return fmt.Errorf("设置目录权限失败：%w", err)
		}
	}
	pgctl := c.J("bin", "pg_ctl.exe")
	if _, err := c.Run(pgctl, "register", "-N", svc, "-D", c.DataDir, "-S", "auto", "-w"); err != nil {
		return fmt.Errorf("注册服务失败：%w", err)
	}
	c.AddService(svc, pgctl, core.PortRef{Name: "PostgreSQL", Port: port})
	if err := c.StartService(svc, port); err != nil {
		return fmt.Errorf("%w。详细原因见 %s", err, filepath.Join(c.DataDir, "log"))
	}
	if c.OptBool("lan") {
		c.OpenFirewall(port)
	}
	if err := c.AddPath(c.J("bin")); err != nil {
		return err
	}
	c.Info("连接地址", fmt.Sprintf("127.0.0.1:%d", port))
	if fresh {
		c.Info("账号", "postgres（密码是安装时填的那个）")
	} else {
		c.Info("账号", "沿用原有数据里的账号和密码")
	}
	c.Info("命令行客户端", fmt.Sprintf("psql -h 127.0.0.1 -p %d -U postgres", port))
	c.Info("服务名", svc)
	c.Info("配置文件", conf)
	c.Info("数据目录", c.DataDir)
	return nil
}

// ---- MongoDB ----

func mongoRelease(v, label string, size int64, sha string) core.Release {
	f := "mongodb-windows-x86_64-" + v + ".zip"
	return core.Release{Version: v, Label: label, File: f, Size: size, SHA256: sha,
		URLs: []string{"https://fastdl.mongodb.org/windows/" + f}}
}

func installMongo(c *core.Ctx) error {
	const svc = "MongoDB"
	port := c.OptInt("port")
	if err := c.NeedPort(port); err != nil {
		return err
	}
	if err := c.NeedNoService(svc); err != nil {
		return err
	}
	if err := unzipInstall(c); err != nil {
		return err
	}
	c.Step("写配置")
	logFile := filepath.Join(c.M.Root(), "logs", "mongod.log")
	for _, d := range []string{c.DataDir, filepath.Dir(logFile)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	cfg := c.J("mongod.cfg")
	err := c.Write(cfg, fmt.Sprintf(`# 由 DevNest 生成。修改后在服务面板里重启 MongoDB 生效。
storage:
  dbPath: '%s'
systemLog:
  destination: file
  path: '%s'
  logAppend: true
net:
  port: %d
  bindIp: 127.0.0.1
`, c.DataDir, logFile, port))
	if err != nil {
		return err
	}
	c.LogFile(logFile)

	c.Step("注册并启动服务")
	mongod := c.J("bin", "mongod.exe")
	if _, err := c.Run(mongod, "--config", cfg, "--install", "--serviceName", svc, "--serviceDisplayName", "MongoDB "+c.Rel.Version); err != nil {
		return fmt.Errorf("注册服务失败：%w", err)
	}
	c.AddService(svc, mongod, core.PortRef{Name: "MongoDB", Port: port})
	if err := c.StartService(svc, port); err != nil {
		return fmt.Errorf("%w。详细原因见 %s", err, logFile)
	}
	if err := c.AddPath(c.J("bin")); err != nil {
		return err
	}
	c.Info("连接地址", fmt.Sprintf("mongodb://127.0.0.1:%d", port))
	c.Info("账号", "没有开启登录验证，只有本机能连")
	c.Info("客户端", "安装包里不带命令行客户端 mongosh，可以用 DBeaver、MongoDB Compass 或程序里的驱动来连")
	c.Info("服务名", svc)
	c.Info("配置文件", cfg)
	c.Info("数据目录", c.DataDir)
	return nil
}

// ---- MariaDB ----

func mariaRelease(v, label string, size int64, sha string) core.Release {
	f := "mariadb-" + v + "-winx64.zip"
	p := "/mariadb-" + v + "/winx64-packages/" + f
	return core.Release{Version: v, Label: label, File: f, Size: size, SHA256: sha, URLs: []string{
		"https://mirrors.aliyun.com/mariadb" + p,
		"https://mirrors.tuna.tsinghua.edu.cn/mariadb" + p,
		"https://archive.mariadb.org" + p,
	}}
}

func installMaria(c *core.Ctx) error {
	const svc = "MariaDB"
	port := c.OptInt("port")
	if err := c.NeedPort(port); err != nil {
		return err
	}
	if err := c.NeedNoService(svc); err != nil {
		return err
	}
	if err := unzipInstall(c); err != nil {
		return err
	}
	ini := filepath.Join(c.DataDir, "my.ini")
	fresh := !c.DataExisted
	if fresh {
		c.Step("初始化数据目录并注册服务")
		if err := os.MkdirAll(filepath.Dir(c.DataDir), 0o755); err != nil {
			return err
		}
		_, err := c.Run(c.J("bin", "mariadb-install-db.exe"), "--datadir="+c.DataDir, "--service="+svc, fmt.Sprintf("--port=%d", port))
		if err != nil {
			return fmt.Errorf("初始化数据目录失败：%w", err)
		}
	} else {
		c.Step("注册服务")
		c.Logf("数据目录 %s 里已经有数据：沿用它，不重新初始化，root 密码保持原来的", c.DataDir)
		if _, err := c.Run(c.J("bin", "mariadbd.exe"), "--install", svc, "--defaults-file="+ini); err != nil {
			return fmt.Errorf("注册服务失败：%w", err)
		}
	}
	err := c.Edit(ini, func(s string) string {
		s = core.SetIniKey(s, "mysqld", "datadir", core.Slash(c.DataDir))
		s = core.SetIniKey(s, "mysqld", "port", fmt.Sprint(port))
		s = core.SetIniKey(s, "mysqld", "bind-address", listenAddr(c))
		s = core.SetIniKey(s, "mysqld", "character-set-server", "utf8mb4")
		s = core.SetIniKey(s, "client", "port", fmt.Sprint(port))
		s = core.SetIniKey(s, "client", "plugin-dir", core.Slash(c.J("lib", "plugin")))
		return s
	})
	if err != nil {
		return err
	}

	c.Step("启动服务")
	c.AddService(svc, c.J("bin", "mariadbd.exe"), core.PortRef{Name: "MariaDB", Port: port})
	if err := c.StartService(svc, port); err != nil {
		return fmt.Errorf("%w。详细原因见数据目录 %s 里的 .err 文件", err, c.DataDir)
	}
	if fresh {
		c.Step("设置 root 密码")
		pw := c.Opt("password")
		sql := fmt.Sprintf("ALTER USER 'root'@'localhost' IDENTIFIED BY '%s';\n", pw)
		if c.OptBool("lan") {
			sql += fmt.Sprintf("CREATE USER IF NOT EXISTS 'root'@'%%' IDENTIFIED BY '%s';\nGRANT ALL PRIVILEGES ON *.* TO 'root'@'%%' WITH GRANT OPTION;\n", pw)
		}
		sql += "FLUSH PRIVILEGES;\n"
		var lastErr error
		for try := 0; try < 6; try++ {
			_, lastErr = c.RunOpts(platform.RunOpts{Stdin: sql, Timeout: time.Minute}, c.J("bin", "mariadb.exe"),
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
	c.Info("命令行客户端", fmt.Sprintf("%s -P %d -u root -p", c.J("bin", "mariadb.exe"), port))
	c.Info("服务名", svc)
	c.Info("配置文件", ini)
	c.Info("数据目录", c.DataDir)
	return nil
}

func init() {
	core.Register(&core.Package{
		ID: "postgresql", Name: "PostgreSQL", Category: "db",
		Desc:     "关系型数据库。初始化为 UTF-8，注册为 Windows 服务，设好 postgres 密码",
		Homepage: "https://www.postgresql.org/",
		Notes:    []string{"用的是 EDB 提供的官方 Windows 绿色包（约 390 MB，含 pgAdmin），国内没有镜像，下载可能比较慢；只能核对文件大小。"},
		Releases: []core.Release{
			pgRelease("17.12", "17.12（推荐）", 386263884),
			pgRelease("18.7", "18.7（最新）", 389515503),
			pgRelease("16.16", "16.16", 378078504),
		},
		Options: []core.Option{
			portOption("port", "端口", "5432", "PostgreSQL"),
			{Key: "password", Label: "postgres 账号的密码", Type: "password", Default: "postgres", MinLen: 4,
				Help: "默认密码很弱，只适合本机开发"},
			lanOption,
		},
		Requires: []core.Dep{{ID: "vcredist"}},
		Probe:    []string{"bin/postgres.exe", "bin/initdb.exe", "bin/pg_ctl.exe", "bin/psql.exe"},
		DataName: func(r core.Release) string { return fmt.Sprintf("postgresql-%d", core.Major(r.Version)) },
		Steps:    4,
		Install:  installPostgres,
		Detect: core.Detect{
			ServiceName: []string{`(?i)^postgresql`},
			ServiceBin:  []string{`(?i)\\pg_ctl\.exe`},
			Uninstall:   []string{`^PostgreSQL \d+`},
			EnvVars:     []string{"PGDATA"},
			PathRx:      []string{`(?i)\\PostgreSQL\\\d+\\bin\\?$`},
			Dirs:        []string{`%ProgramFiles%\PostgreSQL`},
			DataDirs:    []string{`%ProgramFiles%\PostgreSQL\*\data`},
			UserDirs:    []string{`%APPDATA%\postgresql`, `%APPDATA%\pgAdmin`},
		},
	})

	core.Register(&core.Package{
		ID: "mongodb", Name: "MongoDB", Category: "db",
		Desc:     "文档数据库（社区版）。注册为 Windows 服务，只监听本机",
		Homepage: "https://www.mongodb.com/",
		Notes: []string{
			"没有开启登录验证，所以固定只监听 127.0.0.1。要让其他电脑访问，请先自己建账号、在 mongod.cfg 里打开 authorization 再改 bindIp。",
			"安装包 800 MB 以上，从 MongoDB 官网下载。",
		},
		Releases: []core.Release{
			mongoRelease("8.0.38", "8.0.38（推荐）", 809171598, "85147ae32f9bb9a2e5a0a3875b0ffcc4423ab7a49bc4f75cb4bbc9811883d3de"),
			mongoRelease("9.0.8", "9.0.8（最新）", 992283654, "ee18c81847efebd869564e8ec42ca37b0cac7fe353c321969c5db16c3b7e4e8e"),
		},
		Options:  []core.Option{portOption("port", "端口", "27017", "MongoDB")},
		Requires: []core.Dep{{ID: "vcredist"}},
		Probe:    []string{"bin/mongod.exe"},
		DataName: func(core.Release) string { return "mongodb" },
		Steps:    3,
		Install:  installMongo,
		Detect: core.Detect{
			ServiceName: []string{`(?i)^mongodb`},
			ServiceBin:  []string{`(?i)\\mongod\.exe`},
			Uninstall:   []string{`^MongoDB \d`, `^MongoDB Compass`, `^MongoDB Shell`},
			PathRx:      []string{`(?i)\\MongoDB\\Server\\[^\\]+\\bin\\?$`},
			Dirs:        []string{`%ProgramFiles%\MongoDB`},
			DataDirs:    []string{`%ProgramFiles%\MongoDB\Server\*\data`, `?:\data\db`},
		},
	})

	core.Register(&core.Package{
		ID: "mariadb", Name: "MariaDB", Category: "db",
		Desc:     "MySQL 的开源分支。注册为 Windows 服务，设好 root 密码；默认用 3307 端口，可以和 MySQL 同时装",
		Homepage: "https://mariadb.org/",
		Releases: []core.Release{
			mariaRelease("11.8.9", "11.8.9（LTS，推荐）", 97016251, "830c46727d9278eae212ae3eca44eeb9e71b2a68704e95f344a64fba7b1963f5"),
			mariaRelease("12.3.3", "12.3.3（最新）", 104081715, ""),
		},
		Options: []core.Option{
			portOption("port", "端口", "3307", "MariaDB"),
			{Key: "password", Label: "root 密码", Type: "password", Default: "root", MinLen: 4, Help: "默认密码很弱，只适合本机开发"},
			lanOption,
		},
		Requires: []core.Dep{{ID: "vcredist"}},
		Probe:    []string{"bin/mariadbd.exe", "bin/mariadb.exe", "bin/mariadb-install-db.exe"},
		DataName: func(r core.Release) string { return "mariadb-" + core.MinorKey(r.Version) },
		Steps:    4,
		Install:  installMaria,
		Detect: core.Detect{
			ServiceName: []string{`(?i)^mariadb`},
			ServiceBin:  []string{`(?i)\\mariadbd\.exe`, `(?i)\\MariaDB[^\\]*\\bin\\mysqld\.exe`},
			Uninstall:   []string{`^MariaDB \d`},
			PathRx:      []string{`(?i)\\MariaDB[^\\]*\\bin\\?$`},
			Dirs:        []string{`%ProgramFiles%\MariaDB*`},
			DataDirs:    []string{`%ProgramFiles%\MariaDB*\data`},
		},
	})
}
