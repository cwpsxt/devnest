package recipes

import (
	"fmt"
	"os"
	"strings"

	"devnest/internal/core"
)

// temurin builds the release entry for one Eclipse Temurin JDK. tag is the
// GitHub release name ("jdk-21.0.13+9").
func temurin(version, label, tag, file string, size int64, sha string) core.Release {
	major := fmt.Sprint(core.Major(version))
	enc := strings.ReplaceAll(tag, "+", "%2B")
	return core.Release{
		Version: version, Label: label, File: file, Size: size, SHA256: sha,
		URLs: []string{
			"https://mirrors.tuna.tsinghua.edu.cn/Adoptium/" + major + "/jdk/x64/windows/" + file,
			"https://mirrors.ustc.edu.cn/adoptium/releases/temurin" + major + "-binaries/" + enc + "/" + file,
			gh("adoptium/temurin"+major+"-binaries", enc, file),
		},
	}
}

var jdk21 = core.Dep{ID: "jdk", Version: "21"}
var anyJDK = core.Dep{ID: "jdk", MinMajor: 8}

func init() {
	core.Register(&core.Package{
		ID: "jdk", Name: "JDK", Category: "lang",
		Desc:     "Java 开发工具包（Eclipse Temurin，免费的 OpenJDK 发行版）。自动配置 JAVA_HOME 和 PATH，可以同时装多个版本并切换",
		Homepage: "https://adoptium.net/",
		Releases: []core.Release{
			temurin("21.0.13", "21（LTS，推荐）", "jdk-21.0.13+9", "OpenJDK21U-jdk_x64_windows_hotspot_21.0.13_9.zip", 205421281, "a4dbaa05fca4bd89d84584e58c459bd732efebb1722af80ab13c6516832004f9"),
			temurin("25.0.5", "25（LTS）", "jdk-25.0.5+8", "OpenJDK25U-jdk_x64_windows_hotspot_25.0.5_8.zip", 141285173, "636bdc13dc649ddc7d5921c5c7d23cca6e44fd19d662b45cc9c02135a6c96c42"),
			temurin("17.0.21", "17（LTS）", "jdk-17.0.21+9", "OpenJDK17U-jdk_x64_windows_hotspot_17.0.21_9.zip", 191117380, "98518ecf554465057ee5509462631deeee374bc368c12a411d225cd29381f84b"),
			temurin("11.0.33", "11（LTS）", "jdk-11.0.33+10", "OpenJDK11U-jdk_x64_windows_hotspot_11.0.33_10.zip", 199525070, "cff2e75657c96a718974732b4a7b2c81a245a0b2ea44e6d8ea08cd0248d8fa9d"),
			temurin("8.0.512", "8（LTS）", "jdk8u512-b09", "OpenJDK8U-jdk_x64_windows_hotspot_8u512b09.zip", 106490535, "20f248ddd600f900cd9b235d50d54cdb27e7b750f140d381176a7612c53663cd"),
		},
		Options: []core.Option{core.ActiveOption},
		Multi:   true, HomeVar: "JAVA_HOME", PathSubs: []string{"bin"},
		Probe: []string{"bin/java.exe", "bin/javac.exe"},
		Steps: 2,
		Install: func(c *core.Ctx) error {
			if err := unzipInstall(c); err != nil {
				return err
			}
			c.Step("自检")
			c.Info("安装位置", c.Dir)
			return c.Check(c.J("bin", "java.exe"), "-version")
		},
		Detect: core.Detect{
			Uninstall: []string{`(?i)^(Java\(TM\) SE Development Kit|Java SE Development Kit|Java \d+ Update|Java\(TM\) \d+|Eclipse Temurin JDK|OpenJDK|Microsoft Build of OpenJDK|Amazon Corretto|Zulu|Liberica JDK|Oracle JDK)`},
			EnvVars:   []string{"JAVA_HOME", "JRE_HOME", "CLASSPATH"},
			PathRx:    []string{`(?i)\\(java|jdk|jre|openjdk|temurin)[^\\]*(\\[^\\]+)*\\bin\\?$`, `(?i)\\javapath\\?$`, `(?i)%(JAVA_HOME|JRE_HOME)%`},
			Dirs: []string{`%ProgramFiles%\Java`, `%ProgramFiles(x86)%\Java`, `%ProgramFiles%\Eclipse Adoptium`, `%ProgramFiles%\Microsoft\jdk-*`,
				`%ProgramFiles%\Zulu`, `%ProgramFiles%\Amazon Corretto`, `%ProgramFiles%\BellSoft`, `%ProgramFiles%\Common Files\Oracle\Java`,
				`%ProgramFiles(x86)%\Common Files\Oracle\Java`, `?:\jdk*`, `?:\Java\jdk*`},
			RegKeys: []string{`HKLM\SOFTWARE\JavaSoft`, `HKLM\SOFTWARE\WOW6432Node\JavaSoft`},
		},
	})

	core.Register(&core.Package{
		ID: "maven", Name: "Maven", Category: "tool",
		Desc:     "Java 项目构建工具。自动配置 MAVEN_HOME、阿里云镜像和本地仓库位置",
		Homepage: "https://maven.apache.org/",
		Releases: []core.Release{{
			Version: "3.9.16", File: "apache-maven-3.9.16-bin.zip", Size: 9395475,
			SHA512: "ed41650d42485cfc243fad22158caf9cbb5dc408ce7a09ddb94dd42a019de929ca43065bfa450612cf12bf78b5cafa3884b96c090de326ff590448c933454af3",
			URLs: []string{
				"https://maven.aliyun.com/repository/public/org/apache/maven/apache-maven/3.9.16/apache-maven-3.9.16-bin.zip",
				"https://repo.maven.apache.org/maven2/org/apache/maven/apache-maven/3.9.16/apache-maven-3.9.16-bin.zip",
			},
		}},
		Options:  []core.Option{mirrorOption("阿里云 Maven 仓库")},
		Requires: []core.Dep{anyJDK},
		HomeVar:  "MAVEN_HOME", PathSubs: []string{"bin"},
		Probe:    []string{"bin/mvn.cmd", "conf/settings.xml"},
		DataName: func(core.Release) string { return "maven-repo" },
		Steps:    3,
		Install: func(c *core.Ctx) error {
			if err := unzipInstall(c); err != nil {
				return err
			}
			c.Step("写入 settings.xml")
			settings := c.J("conf", "settings.xml")
			if !c.Exists(settings + ".orig") {
				os.Rename(settings, settings+".orig")
			}
			mirror := ""
			if c.OptBool("mirror") {
				mirror = core.MavenMirrorXML("https://maven.aliyun.com/repository/public")
			}
			if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
				return err
			}
			err := c.Write(settings, `<?xml version="1.0" encoding="UTF-8"?>
<!-- 由 DevNest 生成。原始文件保存在 settings.xml.orig -->
<settings xmlns="http://maven.apache.org/SETTINGS/1.2.0"
          xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:schemaLocation="http://maven.apache.org/SETTINGS/1.2.0 https://maven.apache.org/xsd/settings-1.2.0.xsd">
  <localRepository>`+core.Slash(c.DataDir)+`</localRepository>
  <mirrors>`+mirror+`
  </mirrors>
</settings>
`)
			if err != nil {
				return err
			}
			c.Step("自检")
			c.Info("本地仓库", c.DataDir)
			c.Info("配置文件", settings)
			return c.Check(c.J("bin", "mvn.cmd"), "-v")
		},
		Detect: core.Detect{
			EnvVars:  []string{"MAVEN_HOME", "M2_HOME", "M2"},
			PathRx:   []string{`(?i)\\apache-maven[^\\]*\\bin\\?$`, `(?i)%(MAVEN_HOME|M2_HOME|M2)%`},
			Dirs:     []string{`?:\apache-maven*`, `%ProgramFiles%\apache-maven*`, `%ProgramFiles%\Apache\maven*`},
			UserDirs: []string{`%USERPROFILE%\.m2`},
		},
	})

	core.Register(&core.Package{
		ID: "gradle", Name: "Gradle", Category: "tool",
		Desc:     "Java / Kotlin / Android 项目构建工具。自动配置 GRADLE_HOME，依赖缓存放到数据盘",
		Homepage: "https://gradle.org/",
		Releases: []core.Release{{
			Version: "9.9.1", File: "gradle-9.9.1-bin.zip", Size: 134921139,
			SumURL: "https://services.gradle.org/distributions/gradle-9.9.1-bin.zip.sha256",
			URLs: []string{
				"https://mirrors.aliyun.com/gradle/distributions/v9.9.1/gradle-9.9.1-bin.zip",
				"https://mirrors.huaweicloud.com/gradle/gradle-9.9.1-bin.zip",
				"https://services.gradle.org/distributions/gradle-9.9.1-bin.zip",
			},
		}},
		Requires: []core.Dep{{ID: "jdk", MinMajor: 17}},
		HomeVar:  "GRADLE_HOME", PathSubs: []string{"bin"},
		Probe:    []string{"bin/gradle.bat"},
		DataName: func(core.Release) string { return "gradle-home" },
		Steps:    3,
		Install: func(c *core.Ctx) error {
			if err := unzipInstall(c); err != nil {
				return err
			}
			c.Step("配置缓存目录")
			if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
				return err
			}
			if err := c.SetEnv("GRADLE_USER_HOME", c.DataDir); err != nil {
				return err
			}
			c.Step("自检")
			c.Info("依赖缓存（GRADLE_USER_HOME）", c.DataDir)
			c.Info("国内镜像", "在「镜像加速」页里一键配置")
			return c.Check(c.J("bin", "gradle.bat"), "--version")
		},
		Detect: core.Detect{
			EnvVars:  []string{"GRADLE_HOME", "GRADLE_USER_HOME"},
			PathRx:   []string{`(?i)\\gradle-[^\\]*\\bin\\?$`, `(?i)%GRADLE_HOME%`},
			Dirs:     []string{`?:\gradle-*`, `%ProgramFiles%\Gradle`},
			UserDirs: []string{`%USERPROFILE%\.gradle`},
		},
	})

	for _, t := range []struct {
		ver, label string
		size       int64
	}{
		{"11.0.29", "11.0（Jakarta EE 11，需要 JDK 17+）", 16768879},
		{"10.1.61", "10.1（Jakarta EE 10，需要 JDK 11+）", 16490991},
		{"9.0.124", "9.0（Java EE 8，javax 包名）", 15631975},
	} {
		file := "apache-tomcat-" + t.ver + "-windows-x64.zip"
		major := fmt.Sprint(core.Major(t.ver))
		tomcatReleases = append(tomcatReleases, core.Release{
			Version: t.ver, Label: t.label, File: file, Size: t.size,
			SumURL: "https://archive.apache.org/dist/tomcat/tomcat-" + major + "/v" + t.ver + "/bin/" + file + ".sha512",
			URLs: []string{
				"https://mirrors.tuna.tsinghua.edu.cn/apache/tomcat/tomcat-" + major + "/v" + t.ver + "/bin/" + file,
				"https://mirrors.aliyun.com/apache/tomcat/tomcat-" + major + "/v" + t.ver + "/bin/" + file,
				"https://archive.apache.org/dist/tomcat/tomcat-" + major + "/v" + t.ver + "/bin/" + file,
				"https://dlcdn.apache.org/tomcat/tomcat-" + major + "/v" + t.ver + "/bin/" + file,
			},
		})
	}
	core.Register(&core.Package{
		ID: "tomcat", Name: "Tomcat", Category: "mw",
		Desc:     "Java Web 应用服务器。注册为 Windows 服务，控制台日志改为 UTF-8 避免乱码",
		Homepage: "https://tomcat.apache.org/",
		Releases: tomcatReleases,
		Options:  []core.Option{portOption("port", "HTTP 端口", "8080", "HTTP")},
		Requires: []core.Dep{{ID: "jdk", MinMajor: 17}},
		HomeVar:  "CATALINA_HOME",
		Probe:    []string{"bin/catalina.bat", "conf/server.xml"},
		Steps:    4,
		Install: func(c *core.Ctx) error {
			port := c.OptInt("port")
			if err := c.NeedPort(port); err != nil {
				return err
			}
			if err := c.NeedNoService("Tomcat"); err != nil {
				return err
			}
			jdk, err := c.Dep(core.Dep{ID: "jdk", MinMajor: 17})
			if err != nil {
				return err
			}
			if err := unzipInstall(c); err != nil {
				return err
			}
			c.Step("修改配置")
			if err := c.Edit(c.J("conf", "server.xml"), func(old string) string {
				return strings.Replace(old, `<Connector port="8080" protocol="HTTP/1.1"`, fmt.Sprintf(`<Connector port="%d" protocol="HTTP/1.1"`, port), 1)
			}); err != nil {
				return err
			}
			c.Step("注册服务")
			err = c.WrapService(core.WrapSpec{
				Name: "Tomcat", DisplayName: "Apache Tomcat " + c.Rel.Version,
				Exe: c.J("bin", "catalina.bat"), Args: []string{"run"},
				Env: map[string]string{"JAVA_HOME": jdk.Dir, "JRE_HOME": jdk.Dir, "CATALINA_HOME": c.Dir, "CATALINA_BASE": c.Dir,
					"JAVA_OPTS": "-Dfile.encoding=UTF-8 -Dsun.stdout.encoding=UTF-8 -Dsun.stderr.encoding=UTF-8"},
				StopTimeout: 30, Ports: []core.PortRef{{Name: "HTTP", Port: port}},
			})
			if err != nil {
				return err
			}
			c.Step("启动")
			c.Info("访问地址", localURL(port))
			c.Info("部署目录", c.J("webapps"))
			c.Info("使用的 JDK", jdk.Dir)
			return c.StartService("Tomcat", port)
		},
		Detect: core.Detect{
			ServiceName: []string{`(?i)^tomcat\d*$`},
			Uninstall:   []string{`(?i)^Apache Tomcat`},
			EnvVars:     []string{"CATALINA_HOME", "CATALINA_BASE", "TOMCAT_HOME"},
			PathRx:      []string{`(?i)\\apache-tomcat[^\\]*\\bin\\?$`, `(?i)%(CATALINA_HOME|TOMCAT_HOME)%`},
			Dirs:        []string{`?:\apache-tomcat*`, `%ProgramFiles%\Apache Software Foundation\Tomcat*`},
		},
	})
}

var tomcatReleases []core.Release
