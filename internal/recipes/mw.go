package recipes

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"devnest/internal/core"
	"devnest/internal/platform"
)

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// fileURL turns a Windows path into a file:/// URL, the one form every Java
// logging library accepts.
func fileURL(path string) string { return "file:///" + core.Slash(path) }

func javaOf(c *core.Ctx, minMajor int) (string, core.Installed, error) {
	jdk, err := c.Dep(core.Dep{ID: "jdk", MinMajor: minMajor})
	if err != nil {
		return "", jdk, err
	}
	return filepath.Join(jdk.Dir, "bin", "java.exe"), jdk, nil
}

// ---- Nginx ----

var nginxListen = regexp.MustCompile(`(?m)^(\s*)listen\s+[^;#]*;`)

func installNginx(c *core.Ctx) error {
	const svc = "Nginx"
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
	conf := c.J("conf", "nginx.conf")
	listen := fmt.Sprintf("listen       %s:%d;", listenAddr(c), port)
	err := c.Edit(conf, func(s string) string {
		done := false
		return nginxListen.ReplaceAllStringFunc(s, func(m string) string {
			if done {
				return m
			}
			done = true
			return nginxListen.ReplaceAllString(m, "${1}"+listen)
		})
	})
	if err != nil {
		return err
	}
	nginx := c.J("nginx.exe")
	if _, err := c.RunOpts(platform.RunOpts{Dir: c.Dir, Timeout: time.Minute}, nginx, "-t", "-p", c.Dir); err != nil {
		return fmt.Errorf("nginx 配置检查没有通过：%w", err)
	}
	c.Step("注册并启动服务")
	err = c.WrapService(core.WrapSpec{
		Name: svc, DisplayName: "Nginx " + c.Rel.Version,
		Exe: nginx, Args: []string{"-p", c.Dir}, Dir: c.Dir,
		StopExe: nginx, StopArgs: []string{"-p", c.Dir, "-s", "quit"}, StopTimeout: 15,
		Ports: []core.PortRef{{Name: "HTTP", Port: port}},
	})
	if err != nil {
		return err
	}
	c.LogFile(c.J("logs", "error.log"))
	c.LogFile(c.J("logs", "access.log"))
	if c.OptBool("lan") {
		c.OpenFirewall(port)
	}
	c.Info("访问地址", localURL(port))
	c.Info("网站根目录", c.J("html"))
	c.Info("配置文件", conf)
	c.Info("改完配置", "在服务面板里重启 Nginx 生效")
	return c.StartService(svc, port)
}

// ---- Erlang ----

func installErlang(c *core.Ctx) error {
	file, err := c.Fetch()
	if err != nil {
		return err
	}
	c.Step("运行安装程序")
	if err := c.Installer(file, nil, "/S", "/D="+c.Dir); err != nil {
		return err
	}
	if err := c.SetEnv("ERLANG_HOME", c.Dir); err != nil {
		return err
	}
	if err := c.AddPath(c.J("bin")); err != nil {
		return err
	}
	c.Info("命令", "erl、erlc（新开一个命令行窗口后生效）")
	return nil
}

func nsisUninstall(c *core.Ctx) error {
	un := c.J("Uninstall.exe")
	if !c.Exists(un) {
		return nil
	}
	_, err := c.RunOpts(platform.RunOpts{Timeout: 15 * time.Minute}, un, "/S", "_?="+c.Dir)
	return err
}

// ---- RabbitMQ ----

func installRabbit(c *core.Ctx) error {
	const svc = "RabbitMQ"
	port, mgmt := c.OptInt("port"), c.OptInt("mgmt")
	for _, p := range []int{port, mgmt} {
		if err := c.NeedPort(p); err != nil {
			return err
		}
	}
	if err := c.NeedNoService(svc); err != nil {
		return err
	}
	erl, err := c.Dep(core.Dep{ID: "erlang"})
	if err != nil {
		return err
	}
	if err := unzipInstall(c); err != nil {
		return err
	}

	c.Step("写配置")
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return err
	}
	// The node name is pinned to "localhost" so that a computer name with
	// Chinese characters, or a later rename, cannot break the broker. The
	// home folder is pinned so the broker's cookie file has a known place.
	vol := filepath.VolumeName(c.DataDir)
	env := map[string]string{
		"ERLANG_HOME":       erl.Dir,
		"RABBITMQ_BASE":     c.DataDir,
		"RABBITMQ_NODENAME": "rabbit@localhost",
		"ERL_EPMD_ADDRESS":  "127.0.0.1",
		"HOMEDRIVE":         vol,
		"HOMEPATH":          strings.TrimPrefix(c.DataDir, vol),
	}
	addr := listenAddr(c)
	conf := filepath.Join(c.DataDir, "rabbitmq.conf")
	err = c.Write(conf, fmt.Sprintf("# 由 DevNest 生成。修改后在服务面板里重启 RabbitMQ 生效。\nlisteners.tcp.1 = %s:%d\nmanagement.tcp.ip = %s\nmanagement.tcp.port = %d\n",
		addr, port, addr, mgmt))
	if err != nil {
		return err
	}
	_, err = c.RunOpts(platform.RunOpts{Env: env, Timeout: 5 * time.Minute}, c.J("sbin", "rabbitmq-plugins.bat"),
		"enable", "rabbitmq_management", "--offline")
	if err != nil {
		return fmt.Errorf("打开管理界面插件失败：%w", err)
	}

	c.Step("注册并启动服务")
	err = c.WrapService(core.WrapSpec{
		Name: svc, DisplayName: "RabbitMQ " + c.Rel.Version,
		Exe: c.J("sbin", "rabbitmq-server.bat"), Dir: c.Dir, Env: env,
		StopExe: c.J("sbin", "rabbitmqctl.bat"), StopArgs: []string{"stop"}, StopTimeout: 60,
		Ports: []core.PortRef{{Name: "AMQP", Port: port}, {Name: "管理界面", Port: mgmt}},
	})
	if err != nil {
		return err
	}
	for _, kv := range [][2]string{{"RABBITMQ_BASE", c.DataDir}, {"RABBITMQ_NODENAME", "rabbit@localhost"}} {
		if err := c.SetEnv(kv[0], kv[1]); err != nil {
			return err
		}
	}
	if err := c.AddPath(c.J("sbin")); err != nil {
		return err
	}
	if err := c.StartService(svc, port); err != nil {
		return err
	}
	// rabbitmqctl only talks to the broker when both use the same cookie.
	cookie := filepath.Join(c.DataDir, ".erlang.cookie")
	mine := filepath.Join(c.P.Dir("home"), ".erlang.cookie")
	switch {
	case !c.Exists(cookie):
		if !c.Sim() {
			c.Logf("没有找到 %s，命令行工具 rabbitmqctl 可能连不上（管理界面不受影响）", cookie)
		}
	case c.Exists(mine):
		c.Logf("%s 已经存在，没有覆盖。rabbitmqctl 连不上时，把 %s 复制过去即可", mine, cookie)
	default:
		if err := c.CopyFile(cookie, mine); err != nil {
			c.Logf("复制 cookie 文件失败（rabbitmqctl 可能连不上，管理界面不受影响）：%v", err)
		} else {
			c.Also(mine)
		}
	}
	if c.OptBool("lan") {
		c.OpenFirewall(port)
		c.OpenFirewall(mgmt)
	}
	c.Info("连接地址", fmt.Sprintf("amqp://guest:guest@127.0.0.1:%d", port))
	c.Info("管理界面", localURL(mgmt))
	c.Info("账号", "guest / guest（RabbitMQ 规定这个账号只能从本机登录）")
	if c.OptBool("lan") {
		c.Info("给其他电脑用", "先建一个自己的账号：rabbitmqctl add_user 名字 密码，再 rabbitmqctl set_user_tags 名字 administrator，再 rabbitmqctl set_permissions 名字 \".*\" \".*\" \".*\"")
	}
	c.Info("配置文件", conf)
	c.Info("数据目录", c.DataDir)
	return nil
}

// ---- Kafka ----

func kafkaUUID() string {
	for {
		id := base64.RawURLEncoding.EncodeToString(randomBytes(16))
		if !strings.HasPrefix(id, "-") {
			return id
		}
	}
}

func installKafka(c *core.Ctx) error {
	const svc = "Kafka"
	port, ctrl := c.OptInt("port"), c.OptInt("controller")
	for _, p := range []int{port, ctrl} {
		if err := c.NeedPort(p); err != nil {
			return err
		}
	}
	if err := c.NeedNoService(svc); err != nil {
		return err
	}
	java, jdk, err := javaOf(c, 17)
	if err != nil {
		return err
	}
	if err := unzipInstall(c); err != nil {
		return err
	}

	c.Step("写配置")
	conf := c.J("config", "server.properties")
	err = c.Edit(conf, func(s string) string {
		s = setConf(s, "log.dirs", "=", core.Slash(c.DataDir))
		s = setConf(s, "listeners", "=", fmt.Sprintf("PLAINTEXT://%s:%d,CONTROLLER://127.0.0.1:%d", listenAddr(c), port, ctrl))
		s = setConf(s, "advertised.listeners", "=", fmt.Sprintf("PLAINTEXT://localhost:%d,CONTROLLER://localhost:%d", port, ctrl))
		s = setConf(s, "controller.quorum.bootstrap.servers", "=", fmt.Sprintf("localhost:%d", ctrl))
		return s
	})
	if err != nil {
		return err
	}

	// Kafka's own .bat files put every jar on one command line, which goes
	// over the Windows length limit, and call wmic, which Windows 11 no
	// longer has. Java is started directly with a wildcard class path.
	cp := c.J("libs", "*")
	if !c.DataExisted {
		c.Step("初始化存储")
		if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
			return err
		}
		_, err := c.Run(java, "-cp", cp, "-Dlog4j2.configurationFile="+fileURL(c.J("config", "tools-log4j2.yaml")),
			"kafka.tools.StorageTool", "format", "--standalone", "-t", kafkaUUID(), "-c", conf)
		if err != nil {
			return fmt.Errorf("初始化存储失败：%w", err)
		}
	} else {
		c.Logf("数据目录 %s 里已经有数据：沿用它，不重新初始化", c.DataDir)
	}

	c.Step("注册并启动服务")
	heap := c.Opt("heap")
	err = c.WrapService(core.WrapSpec{
		Name: svc, DisplayName: "Apache Kafka " + c.Rel.Version, Exe: java, Dir: c.Dir,
		Args: []string{"-Xmx" + heap, "-Xms" + heap, "-server", "-XX:+UseG1GC", "-XX:MaxGCPauseMillis=20",
			"-XX:InitiatingHeapOccupancyPercent=35", "-XX:+ExplicitGCInvokesConcurrent",
			"-Dkafka.logs.dir=" + c.J("logs"), "-Dlog4j2.configurationFile=" + fileURL(c.J("config", "log4j2.yaml")),
			"-cp", cp, "kafka.Kafka", conf},
		StopTimeout: 60,
		Ports:       []core.PortRef{{Name: "Kafka", Port: port}, {Name: "控制器", Port: ctrl}},
	})
	if err != nil {
		return err
	}
	c.LogFile(c.J("logs", "server.log"))
	if c.OptBool("lan") {
		c.OpenFirewall(port)
		c.Info("给其他电脑用", "还要把 server.properties 里 advertised.listeners 的 localhost 改成本机的局域网 IP，再重启服务")
	}
	c.Info("连接地址", fmt.Sprintf("localhost:%d", port))
	c.Info("运行方式", "KRaft 单节点，不需要 ZooKeeper")
	c.Info("使用的 JDK", jdk.Dir)
	c.Info("配置文件", conf)
	c.Info("数据目录", c.DataDir)
	return c.StartService(svc, port)
}

// ---- Elasticsearch ----

func installElastic(c *core.Ctx) error {
	const svc = "Elasticsearch"
	const transport = 9300
	port := c.OptInt("port")
	for _, p := range []int{port, transport} {
		if err := c.NeedPort(p); err != nil {
			return err
		}
	}
	if err := c.NeedNoService(svc); err != nil {
		return err
	}
	if err := unzipInstall(c); err != nil {
		return err
	}
	c.Step("写配置")
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return err
	}
	yml := c.J("config", "elasticsearch.yml")
	err := c.Edit(yml, func(s string) string {
		return core.SetBlock(s, fmt.Sprintf(`cluster.name: devnest
node.name: node-1
path.data: '%s'
path.logs: '%s'
network.host: 127.0.0.1
http.port: %d
transport.port: %d
discovery.type: single-node
xpack.security.enabled: false`, c.DataDir, c.J("logs"), port, transport))
	})
	if err != nil {
		return err
	}
	heap := c.Opt("heap")
	if err := c.Write(c.J("config", "jvm.options.d", "devnest.options"), "-Xms"+heap+"\n-Xmx"+heap+"\n"); err != nil {
		return err
	}
	c.Step("注册并启动服务（第一次启动要一两分钟）")
	err = c.WrapService(core.WrapSpec{
		Name: svc, DisplayName: "Elasticsearch " + c.Rel.Version,
		Exe: c.J("bin", "elasticsearch.bat"), Dir: c.Dir, StopTimeout: 60,
		Ports: []core.PortRef{{Name: "HTTP", Port: port}, {Name: "节点通信", Port: transport}},
	})
	if err != nil {
		return err
	}
	c.LogFile(c.J("logs", "devnest.log"))
	c.Info("访问地址", localURL(port))
	c.Info("账号", "关闭了登录验证（xpack.security.enabled: false），只有本机能连")
	c.Info("内存", "堆内存 "+heap+"，在 config\\jvm.options.d\\devnest.options 里改")
	c.Info("配置文件", yml)
	c.Info("数据目录", c.DataDir)
	return c.StartService(svc, port)
}

// ---- Nacos ----

// nacosProps fills in application.properties: the ports, and the three
// secrets Nacos 3 refuses to start without. The file ships with those keys
// present and empty (startup.cmd asks for them, and it is not used here).
// Values that are already there are kept, so a reinstall over existing data
// does not invalidate anything.
func nacosProps(s string, port, console int) string {
	s = setConf(s, "nacos.server.main.port", "=", fmt.Sprint(port))
	s = setConf(s, "nacos.console.port", "=", fmt.Sprint(console))
	for key, gen := range map[string]func() string{
		"nacos.core.auth.plugin.nacos.token.secret.key": func() string { return base64.StdEncoding.EncodeToString(randomBytes(48)) },
		"nacos.core.auth.server.identity.key":           func() string { return "devnest" },
		"nacos.core.auth.server.identity.value":         func() string { return hex.EncodeToString(randomBytes(16)) },
	} {
		if core.GetKeyLine(s, key) == "" {
			s = setConf(s, key, "=", gen())
		}
	}
	return s
}

func installNacos(c *core.Ctx) error {
	const svc = "Nacos"
	port, console := c.OptInt("port"), c.OptInt("console")
	for _, p := range []int{port, port + 1000, console} {
		if err := c.NeedPort(p); err != nil {
			return err
		}
	}
	if err := c.NeedNoService(svc); err != nil {
		return err
	}
	java, jdk, err := javaOf(c, 17)
	if err != nil {
		return err
	}
	if err := unzipInstall(c); err != nil {
		return err
	}

	c.Step("写配置")
	// Configuration and the built-in database live in the data folder
	// (nacos.home), so they survive an upgrade or a reinstall.
	confDir := filepath.Join(c.DataDir, "conf")
	props := filepath.Join(confDir, "application.properties")
	if !c.Exists(props) {
		if err := c.CopyTree(c.J("conf"), confDir); err != nil {
			return err
		}
	}
	err = c.Edit(props, func(s string) string { return nacosProps(s, port, console) })
	if err != nil {
		return err
	}

	c.Step("注册并启动服务")
	home, dir := core.Slash(c.DataDir), core.Slash(c.Dir)
	err = c.WrapService(core.WrapSpec{
		Name: svc, DisplayName: "Nacos " + c.Rel.Version, Exe: java, Dir: c.DataDir,
		Args: []string{"-Xms512m", "-Xmx512m", "-Xmn256m",
			"--add-opens=java.base/java.lang=ALL-UNNAMED", "--add-opens=java.base/java.lang.reflect=ALL-UNNAMED",
			"--add-opens=java.base/java.util=ALL-UNNAMED",
			"-Dnacos.standalone=true", "-Dnacos.deployment.type=merged",
			"-Dloader.path=" + dir + "/plugins," + dir + "/plugins/health," + dir + "/plugins/cmdb," + dir + "/plugins/selector",
			"-Dnacos.home=" + home,
			"-jar", c.J("target", "nacos-server.jar"),
			"--spring.config.additional-location=file:" + home + "/conf/",
			"--logging.config=" + home + "/conf/nacos-logback.xml",
			"nacos.nacos"},
		StopTimeout: 30,
		Ports:       []core.PortRef{{Name: "服务端口", Port: port}, {Name: "gRPC", Port: port + 1000}, {Name: "控制台", Port: console}},
	})
	if err != nil {
		return err
	}
	c.LogFile(filepath.Join(c.DataDir, "logs", "nacos.log"))
	if c.OptBool("lan") {
		for _, p := range []int{port, port + 1000, console} {
			c.OpenFirewall(p)
		}
	}
	c.Info("控制台", localURL(console))
	c.Info("账号", "第一次打开控制台时，会让你给 nacos 账号设一个密码")
	c.Info("程序里填的地址", fmt.Sprintf("127.0.0.1:%d", port))
	c.Info("运行方式", "单机模式，数据存在自带的数据库里")
	c.Info("使用的 JDK", jdk.Dir)
	c.Info("配置文件", props)
	c.Info("数据目录", c.DataDir)
	return c.StartService(svc, port)
}

func init() {
	core.Register(&core.Package{
		ID: "nginx", Name: "Nginx", Category: "mw",
		Desc:     "网页服务器 / 反向代理。注册为 Windows 服务",
		Homepage: "https://nginx.org/",
		Releases: []core.Release{
			{Version: "1.30.6", Label: "1.30.6（稳定版，推荐）", File: "nginx-1.30.6.zip", Size: 2773293,
				URLs: []string{"https://mirrors.huaweicloud.com/nginx/nginx-1.30.6.zip", "https://nginx.org/download/nginx-1.30.6.zip"}},
			{Version: "1.31.8", Label: "1.31.8（主线版）", File: "nginx-1.31.8.zip", Size: 2794487,
				URLs: []string{"https://mirrors.huaweicloud.com/nginx/nginx-1.31.8.zip", "https://nginx.org/download/nginx-1.31.8.zip"}},
		},
		Options: []core.Option{portOption("port", "端口", "80", "HTTP"), lanOption},
		Probe:   []string{"nginx.exe", "conf/nginx.conf"},
		Steps:   3,
		Install: installNginx,
		Detect: core.Detect{
			ServiceName: []string{`(?i)^nginx`},
			ServiceBin:  []string{`(?i)\\nginx[^\\]*\.exe`},
			Procs:       []string{"nginx.exe"},
			Dirs:        []string{`?:\nginx`, `?:\nginx-1*`},
		},
	})

	erl := "otp_win64_29.1.2.exe"
	core.Register(&core.Package{
		ID: "erlang", Name: "Erlang/OTP", Category: "lang",
		Desc:      "Erlang 运行环境，RabbitMQ 需要它",
		Homepage:  "https://www.erlang.org/",
		Notes:     []string{"只能从 GitHub 下载（约 150 MB），国内可能很慢；可以在设置里填 GitHub 加速地址，或者手动下载后用“导入安装包”。"},
		Releases:  []core.Release{{Version: "29.1.2", File: erl, Size: 155770072, URLs: []string{gh("erlang/otp", "OTP-29.1.2", erl)}}},
		Probe:     []string{"bin/erl.exe", "bin/erlc.exe"},
		Steps:     1,
		Install:   installErlang,
		Uninstall: nsisUninstall,
		Detect: core.Detect{
			Uninstall: []string{`^Erlang OTP`},
			EnvVars:   []string{"ERLANG_HOME"},
			PathRx:    []string{`(?i)\\(Erlang OTP|erl-?[\d.]+)\\bin\\?$`},
			Dirs:      []string{`%ProgramFiles%\Erlang OTP`, `%ProgramFiles%\erl*`},
			UserDirs:  []string{},
		},
	})

	rabbit := "rabbitmq-server-windows-4.4.0.zip"
	core.Register(&core.Package{
		ID: "rabbitmq", Name: "RabbitMQ", Category: "mw",
		Desc:     "消息队列。注册为 Windows 服务，打开网页管理界面",
		Homepage: "https://www.rabbitmq.com/",
		Notes:    []string{"需要 Erlang，会自动一起装。两个安装包都只能从 GitHub 下载，国内可能很慢。"},
		Releases: []core.Release{{Version: "4.4.0", File: rabbit, Size: 71254469, URLs: []string{gh("rabbitmq/rabbitmq-server", "v4.4.0", rabbit)}}},
		Options: []core.Option{
			portOption("port", "AMQP 端口", "5672", "AMQP"),
			portOption("mgmt", "管理界面端口", "15672", "管理界面"),
			lanOption,
		},
		Requires: []core.Dep{{ID: "erlang"}},
		Probe:    []string{"sbin/rabbitmq-server.bat", "sbin/rabbitmqctl.bat", "sbin/rabbitmq-plugins.bat"},
		DataName: func(core.Release) string { return "rabbitmq" },
		Steps:    3,
		Install:  installRabbit,
		Detect: core.Detect{
			ServiceName: []string{`(?i)^RabbitMQ$`},
			Uninstall:   []string{`^RabbitMQ Server`},
			EnvVars:     []string{"RABBITMQ_BASE", "RABBITMQ_NODENAME", "RABBITMQ_SERVER"},
			PathRx:      []string{`(?i)\\rabbitmq_server-[^\\]+\\sbin\\?$`},
			Dirs:        []string{`%ProgramFiles%\RabbitMQ Server`},
			DataDirs:    []string{`%APPDATA%\RabbitMQ`},
		},
	})

	kafka := "kafka_2.13-4.4.0.tgz"
	core.Register(&core.Package{
		ID: "kafka", Name: "Kafka", Category: "mw",
		Desc:     "消息队列 / 流平台。单节点 KRaft 模式（不需要 ZooKeeper），注册为 Windows 服务",
		Homepage: "https://kafka.apache.org/",
		Notes: []string{
			"需要 JDK 17 或更高，没有的话会自动装 JDK 21。",
			"Kafka 官方不支持在 Windows 上用于生产：运行时间长了，清理旧日志段时可能因为文件被占用而退出。只建议用来做本机开发调试。",
		},
		Releases: []core.Release{{Version: "4.4.0", File: kafka, Size: 135295923,
			SumURL: "https://archive.apache.org/dist/kafka/4.4.0/" + kafka + ".sha512",
			URLs: []string{
				"https://mirrors.aliyun.com/apache/kafka/4.4.0/" + kafka,
				"https://mirrors.tuna.tsinghua.edu.cn/apache/kafka/4.4.0/" + kafka,
				"https://archive.apache.org/dist/kafka/4.4.0/" + kafka,
				// The publisher's own address comes last. archive keeps every
				// release for good but is slow (a quarter of an hour for this
				// file); dlcdn is fast and only has the current releases.
				"https://dlcdn.apache.org/kafka/4.4.0/" + kafka,
			}}},
		Options: []core.Option{
			portOption("port", "端口", "9092", "Kafka"),
			portOption("controller", "控制器端口（内部用）", "9093", "控制器"),
			{Key: "heap", Label: "内存", Type: "select", Default: "512m", Choices: []core.Choice{
				{Value: "512m", Label: "512 MB"}, {Value: "1g", Label: "1 GB"}, {Value: "2g", Label: "2 GB"}}},
			lanOption,
		},
		Requires: []core.Dep{{ID: "jdk", MinMajor: 17}},
		Probe:    []string{"config/server.properties", "bin/windows/kafka-topics.bat"},
		DataName: func(core.Release) string { return "kafka" },
		Steps:    4,
		Install:  installKafka,
		Detect: core.Detect{
			ServiceName: []string{`(?i)^kafka`},
			EnvVars:     []string{"KAFKA_HOME"},
			PathRx:      []string{`(?i)\\kafka[^\\]*\\bin\\windows\\?$`},
			Dirs:        []string{`?:\kafka`, `?:\kafka_2*`},
			DataDirs:    []string{`?:\tmp\kraft-combined-logs`, `?:\tmp\kafka-logs`},
		},
	})

	es := "elasticsearch-9.5.6-windows-x86_64.zip"
	core.Register(&core.Package{
		ID: "elasticsearch", Name: "Elasticsearch", Category: "mw",
		Desc:     "搜索引擎。单节点，注册为 Windows 服务，只监听本机；自带 Java 运行环境",
		Homepage: "https://www.elastic.co/elasticsearch",
		Notes: []string{
			"为了开发方便关闭了登录验证和 HTTPS，所以固定只监听 127.0.0.1，不提供局域网选项。",
			"安装包 640 MB 以上，从 Elastic 官网下载，国内没有镜像。",
		},
		Releases: []core.Release{{Version: "9.5.6", File: es, Size: 674226618,
			SumURL: "https://artifacts.elastic.co/downloads/elasticsearch/" + es + ".sha512",
			URLs:   []string{"https://artifacts.elastic.co/downloads/elasticsearch/" + es}}},
		Options: []core.Option{
			portOption("port", "HTTP 端口", "9200", "HTTP"),
			{Key: "heap", Label: "内存", Type: "select", Default: "1g", Choices: []core.Choice{
				{Value: "512m", Label: "512 MB"}, {Value: "1g", Label: "1 GB"}, {Value: "2g", Label: "2 GB"}, {Value: "4g", Label: "4 GB"}}},
		},
		Probe:    []string{"bin/elasticsearch.bat", "config/elasticsearch.yml"},
		DataName: func(core.Release) string { return "elasticsearch" },
		Steps:    3,
		Install:  installElastic,
		Detect: core.Detect{
			ServiceName: []string{`(?i)^elasticsearch`},
			EnvVars:     []string{"ES_HOME", "ES_JAVA_HOME"},
			Dirs:        []string{`?:\elasticsearch-*`},
		},
	})

	core.Register(&core.Package{
		ID: "nacos", Name: "Nacos", Category: "mw",
		Desc:     "服务注册与配置中心（Spring Cloud Alibaba 常用）。单机模式，注册为 Windows 服务",
		Homepage: "https://nacos.io/",
		Notes: []string{
			"需要 JDK 17 或更高，没有的话会自动装 JDK 21。",
			"Nacos 自己会监听所有网卡；不勾选“允许局域网”时只是不放行 Windows 防火墙。",
			"控制台默认用 8080 端口，和 Tomcat 的默认端口相同，两个都装时请改其中一个。",
		},
		Releases: []core.Release{{Version: "3.2.0", File: "nacos-server-3.2.0.zip", Size: 217969764,
			URLs: []string{"https://download.nacos.io/nacos-server/nacos-server-3.2.0.zip", gh("alibaba/nacos", "3.2.0", "nacos-server-3.2.0.zip")}}},
		Options: []core.Option{
			portOption("port", "服务端口", "8848", "服务端口"),
			portOption("console", "控制台端口", "8080", "控制台"),
			lanOption,
		},
		Requires: []core.Dep{{ID: "jdk", MinMajor: 17}},
		Probe:    []string{"target/nacos-server.jar", "conf/application.properties"},
		DataName: func(core.Release) string { return "nacos" },
		Steps:    3,
		Install:  installNacos,
		Detect: core.Detect{
			ServiceName: []string{`(?i)^nacos`},
			Dirs:        []string{`?:\nacos`},
		},
	})
}
