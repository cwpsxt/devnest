package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"devnest/internal/platform"
)

// MirrorItem is one tool whose download source can be pointed at a mirror in
// mainland China.
type MirrorItem struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Desc    string   `json:"desc"`
	File    string   `json:"file"`
	Choices []Choice `json:"choices"`
	Current string   `json:"current"`
	Custom  bool     `json:"custom"`
}

type mirrorDef struct {
	id, name, desc string
	choices        []Choice // the first one is the recommended mirror
	custom         bool
	file           func(m *Manager) string
	get            func(m *Manager) string
	set            func(m *Manager, url string) error // url "" restores the official source
}

func (m *Manager) home(parts ...string) string {
	return filepath.Join(append([]string{m.P.Dir("home")}, parts...)...)
}

func readText(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// writeConfig saves a config file, keeping a one-time copy of the original.
func writeConfig(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		if _, err := os.Stat(path + ".devnest-bak"); os.IsNotExist(err) {
			copyFile(path, path+".devnest-bak")
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func eol(content string) string {
	if strings.Contains(content, "\r\n") || content == "" {
		return "\r\n"
	}
	return "\n"
}

// SetKeyLine sets `key<sep>value` in a flat key/value file (.npmrc, go env),
// replacing an existing line for the key. An empty value removes the line.
func SetKeyLine(content, key, sep, value string) string {
	nl := eol(content)
	rx := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `\s*(=|\s)`)
	var out []string
	done := false
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if rx.MatchString(line) {
			if value != "" && !done {
				out = append(out, key+sep+value)
				done = true
			}
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if value != "" && !done {
		out = append(out, key+sep+value)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, nl) + nl
}

// GetKeyLine reads the value written by SetKeyLine. A key with nothing after
// it has the value ""; the blanks are matched with [ \t] and not \s, which
// would run on into the next line and return that instead.
func GetKeyLine(content, key string) string {
	rx := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(key) + `[ \t]*(?:=|[ \t])[ \t]*"?([^"\r\n]*?)"?[ \t]*\r?$`)
	if m := rx.FindStringSubmatch(content); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// SetIniKey sets key = value inside [section] of an ini file. An empty value
// removes the key.
func SetIniKey(content, section, key, value string) string {
	nl := eol(content)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	keyRx := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `\s*=`)
	var out []string
	in, found, done := false, false, false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			if in && !done && value != "" {
				out = append(out, key+" = "+value)
				done = true
			}
			in = strings.EqualFold(trim, "["+section+"]")
			found = found || in
			out = append(out, line)
			continue
		}
		if in && keyRx.MatchString(line) {
			if value != "" && !done {
				out = append(out, key+" = "+value)
				done = true
			}
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if value != "" && !done {
		if !found {
			if len(out) > 0 {
				out = append(out, "")
			}
			out = append(out, "["+section+"]")
		}
		out = append(out, key+" = "+value)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, nl) + nl
}

func GetIniKey(content, section, key string) string {
	in := false
	keyRx := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `\s*=\s*(.*)$`)
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") {
			in = strings.EqualFold(trim, "["+section+"]")
			continue
		}
		if in {
			if m := keyRx.FindStringSubmatch(line); m != nil {
				return strings.TrimSpace(m[1])
			}
		}
	}
	return ""
}

const blockStart, blockEnd = "# >>> devnest mirror >>>", "# <<< devnest mirror <<<"

var blockRx = regexp.MustCompile(`(?s)\r?\n?` + regexp.QuoteMeta(blockStart) + `.*?` + regexp.QuoteMeta(blockEnd) + `\r?\n?`)

// SetBlock replaces (or with empty body removes) the block of lines this
// program manages inside a file it otherwise leaves alone.
func SetBlock(content, body string) string {
	nl := eol(content)
	rest := strings.TrimRight(blockRx.ReplaceAllString(content, nl), "\r\n")
	if body == "" {
		if rest == "" {
			return ""
		}
		return rest + nl
	}
	block := blockStart + nl + strings.ReplaceAll(strings.TrimSpace(body), "\n", nl) + nl + blockEnd + nl
	if rest == "" {
		return block
	}
	return rest + nl + nl + block
}

func GetBlock(content string) string {
	i := strings.Index(content, blockStart)
	j := strings.Index(content, blockEnd)
	if i < 0 || j < i {
		return ""
	}
	return content[i+len(blockStart) : j]
}

var mavenMirrorRx = regexp.MustCompile(`(?s)\s*<mirror>\s*<id>devnest-mirror</id>.*?</mirror>`)
var mavenURLRx = regexp.MustCompile(`(?s)<id>devnest-mirror</id>.*?<url>([^<]*)</url>`)

// MavenMirrorXML is the mirror element written into settings.xml.
func MavenMirrorXML(url string) string {
	return "\n    <mirror>\n      <id>devnest-mirror</id>\n      <mirrorOf>central</mirrorOf>\n      <name>DevNest mirror</name>\n      <url>" + url + "</url>\n    </mirror>"
}

// SetMavenMirror inserts, replaces or removes our mirror in a settings.xml.
func SetMavenMirror(content, url string) (string, error) {
	if strings.TrimSpace(content) == "" {
		if url == "" {
			return "", nil
		}
		content = `<?xml version="1.0" encoding="UTF-8"?>
<settings xmlns="http://maven.apache.org/SETTINGS/1.2.0"
          xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:schemaLocation="http://maven.apache.org/SETTINGS/1.2.0 https://maven.apache.org/xsd/settings-1.2.0.xsd">
  <mirrors>
  </mirrors>
</settings>
`
	}
	content = mavenMirrorRx.ReplaceAllString(content, "")
	if url == "" {
		return content, nil
	}
	// Comments can contain sample <mirrors> tags; only look outside them.
	plain := regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllStringFunc(content, func(s string) string { return strings.Repeat(" ", len(s)) })
	if i := strings.Index(plain, "<mirrors>"); i >= 0 {
		at := i + len("<mirrors>")
		return content[:at] + MavenMirrorXML(url) + content[at:], nil
	}
	if i := strings.LastIndex(plain, "</settings>"); i >= 0 {
		return content[:i] + "  <mirrors>" + MavenMirrorXML(url) + "\n  </mirrors>\n" + content[i:], nil
	}
	return "", errors.New("settings.xml 的结构无法识别（没有 </settings>），请手动修改")
}

func gradleInit(url string) string {
	plugin := "https://plugins.gradle.org/m2"
	if strings.Contains(url, "maven.aliyun.com") {
		plugin = "https://maven.aliyun.com/repository/gradle-plugin"
	}
	return `// 由 DevNest 生成：把 Maven 中央仓库换成国内镜像。删除本文件即可恢复。
// devnest-mirror-url: ` + url + `
def MIRROR = '` + url + `'
def PLUGIN_MIRROR = '` + plugin + `'
def rewrite = { repos ->
    repos.all { repo ->
        if (repo instanceof MavenArtifactRepository) {
            def u = repo.url.toString()
            if (u.startsWith('https://repo.maven.apache.org/maven2') || u.startsWith('https://repo1.maven.org/maven2') || u.startsWith('https://jcenter.bintray.com')) {
                repo.url = MIRROR
            } else if (u.startsWith('https://plugins.gradle.org/m2')) {
                repo.url = PLUGIN_MIRROR
            }
        }
    }
}
gradle.beforeSettings { settings ->
    rewrite(settings.pluginManagement.repositories)
    if (settings.metaClass.respondsTo(settings, 'getDependencyResolutionManagement')) {
        rewrite(settings.dependencyResolutionManagement.repositories)
    }
}
allprojects {
    buildscript { rewrite(repositories) }
    rewrite(repositories)
}
`
}

func cargoBlock(url string) string {
	return "[source.crates-io]\nreplace-with = 'devnest-mirror'\n\n[source.devnest-mirror]\nregistry = \"" + url + "\""
}

var mirrorDefs = []mirrorDef{
	{
		id: "npm", name: "npm / pnpm / yarn", desc: "Node.js 包的下载源，写入用户目录下的 .npmrc 和 .yarnrc", custom: true,
		choices: []Choice{
			{"https://registry.npmmirror.com/", "npmmirror（原淘宝镜像）"},
			{"https://mirrors.cloud.tencent.com/npm/", "腾讯云"},
			{"https://repo.huaweicloud.com/repository/npm/", "华为云"},
		},
		file: func(m *Manager) string { return m.home(".npmrc") },
		get:  func(m *Manager) string { return GetKeyLine(readText(m.home(".npmrc")), "registry") },
		set: func(m *Manager, url string) error {
			if err := writeConfig(m.home(".npmrc"), SetKeyLine(readText(m.home(".npmrc")), "registry", "=", url)); err != nil {
				return err
			}
			v := ""
			if url != "" {
				v = `"` + url + `"`
			}
			return writeConfig(m.home(".yarnrc"), SetKeyLine(readText(m.home(".yarnrc")), "registry", " ", v))
		},
	},
	{
		id: "pip", name: "pip", desc: "Python 包的下载源，写入 %APPDATA%\\pip\\pip.ini", custom: true,
		choices: []Choice{
			{"https://pypi.tuna.tsinghua.edu.cn/simple", "清华大学"},
			{"https://mirrors.aliyun.com/pypi/simple/", "阿里云"},
			{"https://mirrors.cloud.tencent.com/pypi/simple/", "腾讯云"},
			{"https://mirrors.ustc.edu.cn/pypi/simple/", "中科大"},
		},
		file: func(m *Manager) string { return filepath.Join(m.P.Dir("appdata"), "pip", "pip.ini") },
		get: func(m *Manager) string {
			return GetIniKey(readText(filepath.Join(m.P.Dir("appdata"), "pip", "pip.ini")), "global", "index-url")
		},
		set: func(m *Manager, url string) error {
			path := filepath.Join(m.P.Dir("appdata"), "pip", "pip.ini")
			return writeConfig(path, SetIniKey(readText(path), "global", "index-url", url))
		},
	},
	{
		id: "maven", name: "Maven", desc: "中央仓库镜像，写入用户目录下的 .m2\\settings.xml（只增改一段 mirror，其他内容不动）", custom: true,
		choices: []Choice{
			{"https://maven.aliyun.com/repository/public", "阿里云"},
			{"https://mirrors.cloud.tencent.com/nexus/repository/maven-public/", "腾讯云"},
			{"https://repo.huaweicloud.com/repository/maven/", "华为云"},
		},
		file: func(m *Manager) string { return m.home(".m2", "settings.xml") },
		get: func(m *Manager) string {
			if mm := mavenURLRx.FindStringSubmatch(readText(m.home(".m2", "settings.xml"))); mm != nil {
				return strings.TrimSpace(mm[1])
			}
			return ""
		},
		set: func(m *Manager, url string) error {
			path := m.home(".m2", "settings.xml")
			next, err := SetMavenMirror(readText(path), url)
			if err != nil {
				return err
			}
			if next == "" {
				return nil
			}
			return writeConfig(path, next)
		},
	},
	{
		id: "gradle", name: "Gradle", desc: "把 Maven 中央仓库和插件仓库换成镜像，写入用户目录下的 .gradle\\init.d\\devnest-mirror.gradle", custom: true,
		choices: []Choice{
			{"https://maven.aliyun.com/repository/public", "阿里云"},
			{"https://mirrors.cloud.tencent.com/nexus/repository/maven-public/", "腾讯云"},
			{"https://repo.huaweicloud.com/repository/maven/", "华为云"},
		},
		file: func(m *Manager) string { return m.home(".gradle", "init.d", "devnest-mirror.gradle") },
		get: func(m *Manager) string {
			return GetKeyLine(readText(m.home(".gradle", "init.d", "devnest-mirror.gradle")), "// devnest-mirror-url:")
		},
		set: func(m *Manager, url string) error {
			path := m.home(".gradle", "init.d", "devnest-mirror.gradle")
			if url == "" {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return err
				}
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			return os.WriteFile(path, []byte(gradleInit(url)), 0o644)
		},
	},
	{
		id: "go", name: "Go 模块代理", desc: "GOPROXY，写入 %APPDATA%\\go\\env（等同于 go env -w）", custom: true,
		choices: []Choice{
			{"https://goproxy.cn,direct", "goproxy.cn（七牛云）"},
			{"https://mirrors.aliyun.com/goproxy/,direct", "阿里云"},
			{"https://goproxy.io,direct", "goproxy.io"},
		},
		file: func(m *Manager) string { return filepath.Join(m.P.Dir("appdata"), "go", "env") },
		get: func(m *Manager) string {
			return GetKeyLine(readText(filepath.Join(m.P.Dir("appdata"), "go", "env")), "GOPROXY")
		},
		set: func(m *Manager, url string) error {
			path := filepath.Join(m.P.Dir("appdata"), "go", "env")
			next := SetKeyLine(readText(path), "GOPROXY", "=", url)
			// The go command wants LF line ends in this file.
			return writeConfig(path, strings.ReplaceAll(next, "\r\n", "\n"))
		},
	},
	{
		id: "cargo", name: "Rust（cargo / rustup）", desc: "crates.io 索引镜像写入用户目录下的 .cargo\\config.toml，rustup 下载地址写入用户环境变量",
		choices: []Choice{
			{"sparse+https://rsproxy.cn/index/", "rsproxy.cn（字节跳动）"},
			{"sparse+https://mirrors.ustc.edu.cn/crates.io-index/", "中科大"},
			{"sparse+https://mirrors.tuna.tsinghua.edu.cn/crates.io-index/", "清华大学"},
		},
		file: func(m *Manager) string { return m.home(".cargo", "config.toml") },
		get: func(m *Manager) string {
			return GetKeyLine(GetBlock(readText(m.home(".cargo", "config.toml"))), "registry")
		},
		set: func(m *Manager, url string) error {
			path := m.home(".cargo", "config.toml")
			old := readText(path)
			outside := blockRx.ReplaceAllString(old, "\n")
			if url != "" && strings.Contains(outside, "[source.crates-io]") {
				return errors.New(".cargo\\config.toml 里已经有你自己写的 [source.crates-io]，为避免冲突没有改动，请手动调整")
			}
			body := ""
			if url != "" {
				body = cargoBlock(url)
			}
			next := SetBlock(old, body)
			if next == "" && old == "" {
				return nil
			}
			if err := writeConfig(path, next); err != nil {
				return err
			}
			dist := map[string][2]string{
				"rsproxy.cn":                {"https://rsproxy.cn", "https://rsproxy.cn/rustup"},
				"mirrors.ustc.edu.cn":       {"https://mirrors.ustc.edu.cn/rust-static", "https://mirrors.ustc.edu.cn/rust-static/rustup"},
				"mirrors.tuna.tsinghua.edu": {"https://mirrors.tuna.tsinghua.edu.cn/rustup", "https://mirrors.tuna.tsinghua.edu.cn/rustup/rustup"},
			}
			for host, v := range dist {
				if strings.Contains(url, host) {
					m.P.SetEnv(platform.User, "RUSTUP_DIST_SERVER", v[0])
					m.P.SetEnv(platform.User, "RUSTUP_UPDATE_ROOT", v[1])
					m.P.BroadcastEnv()
					return nil
				}
			}
			m.P.DeleteEnv(platform.User, "RUSTUP_DIST_SERVER")
			m.P.DeleteEnv(platform.User, "RUSTUP_UPDATE_ROOT")
			m.P.BroadcastEnv()
			return nil
		},
	},
	{
		id: "docker", name: "Docker 镜像加速", desc: "registry-mirrors，写入用户目录下的 .docker\\daemon.json。公共加速地址经常失效，请填你自己可用的地址；改完需要重启 Docker Desktop", custom: true,
		file: func(m *Manager) string { return m.home(".docker", "daemon.json") },
		get: func(m *Manager) string {
			var cfg map[string]any
			if json.Unmarshal([]byte(readText(m.home(".docker", "daemon.json"))), &cfg) != nil {
				return ""
			}
			if list, ok := cfg["registry-mirrors"].([]any); ok && len(list) > 0 {
				return fmt.Sprint(list[0])
			}
			return ""
		},
		set: func(m *Manager, url string) error {
			path := m.home(".docker", "daemon.json")
			cfg := map[string]any{}
			if old := strings.TrimSpace(readText(path)); old != "" {
				if err := json.Unmarshal([]byte(old), &cfg); err != nil {
					return fmt.Errorf("daemon.json 不是合法的 JSON，没有改动：%w", err)
				}
			} else if url == "" {
				return nil
			}
			if url == "" {
				delete(cfg, "registry-mirrors")
			} else {
				cfg["registry-mirrors"] = []string{url}
			}
			b, _ := json.MarshalIndent(cfg, "", "  ")
			return writeConfig(path, string(b)+"\n")
		},
	},
}

// Mirrors lists the configurable download sources and their current values.
func (m *Manager) Mirrors() []MirrorItem {
	out := make([]MirrorItem, 0, len(mirrorDefs))
	for _, d := range mirrorDefs {
		choices := d.choices
		if choices == nil {
			choices = []Choice{}
		}
		out = append(out, MirrorItem{ID: d.id, Name: d.name, Desc: d.desc, File: d.file(m), Choices: choices, Current: d.get(m), Custom: d.custom})
	}
	return out
}

// SetMirror points one tool at a mirror; an empty url restores the official
// source.
func (m *Manager) SetMirror(id, url string) error {
	url = strings.TrimSpace(url)
	for _, d := range mirrorDefs {
		if d.id != id {
			continue
		}
		if url != "" {
			known := false
			for _, c := range d.choices {
				known = known || c.Value == url
			}
			if !known {
				if !d.custom {
					return errors.New("这一项只能从列表里选")
				}
				if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
					return errors.New("地址必须以 http:// 或 https:// 开头")
				}
				if strings.ContainsAny(url, " \"'<>\r\n") {
					return errors.New("地址里不能有空格、引号或尖括号")
				}
			}
		}
		err := d.set(m, url)
		m.changed("mirrors")
		return err
	}
	return fmt.Errorf("没有这个镜像项：%s", id)
}

// SetAllMirrors applies the recommended mirror to every tool that has one.
// It returns one message per tool that could not be changed.
func (m *Manager) SetAllMirrors() []string {
	var problems []string
	for _, d := range mirrorDefs {
		if len(d.choices) == 0 {
			continue
		}
		if err := d.set(m, d.choices[0].Value); err != nil {
			problems = append(problems, d.name+"："+err.Error())
		}
	}
	m.changed("mirrors")
	if problems == nil {
		problems = []string{}
	}
	return problems
}
