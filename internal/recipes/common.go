// Package recipes holds the catalogue: one entry per piece of software, with
// its download addresses and the steps to install and configure it. Adding
// software means adding a core.Register call here.
package recipes

import (
	"fmt"
	"regexp"
	"strings"

	"devnest/internal/core"
)

var lanOption = core.Option{Key: "lan", Label: "允许局域网其他电脑访问", Type: "bool", Default: "false",
	Help: "默认只有本机能连。勾选后会监听所有网卡并放行 Windows 防火墙；有密码的软件必须改掉默认密码（至少 8 位）"}

// dbPassword is the default password of every database account the catalogue
// sets up. It only suits a machine nobody else can reach, which is why the
// "lan" option refuses an option's default password.
const dbPassword = "12345678"

func passwordOption(label string, minLen int, rule string) core.Option {
	help := "默认是 " + dbPassword + "，只适合本机开发"
	if rule != "" {
		help += "；" + rule
	}
	return core.Option{Key: "password", Label: label, Type: "password", Default: dbPassword, MinLen: minLen, Help: help}
}

func portOption(key, label, def, portName string) core.Option {
	return core.Option{Key: key, Label: label, Type: "port", Default: def, PortName: portName}
}

func mirrorOption(what string) core.Option {
	return core.Option{Key: "mirror", Label: "使用国内镜像（" + what + "）", Type: "bool", Default: "true"}
}

// listenAddr picks the bind address from the "lan" option.
func listenAddr(c *core.Ctx) string {
	if c.OptBool("lan") {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// setConf sets key<sep>value in a line-based config file: an existing active
// line for the key is replaced, otherwise the line is appended. Files such as
// neo4j.conf refuse to start when a key appears twice, so appending blindly
// is not an option.
func setConf(content, key, sep, value string) string {
	nl := "\n"
	if strings.Contains(content, "\r\n") {
		nl = "\r\n"
	}
	rx := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `(\s|=|:)`)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	done := false
	out := lines[:0]
	for _, l := range lines {
		if rx.MatchString(l) {
			if !done {
				out = append(out, key+sep+value)
				done = true
			}
			continue
		}
		out = append(out, l)
	}
	if !done {
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		out = append(out, key+sep+value, "")
	}
	return strings.Join(out, nl)
}

// unzipInstall is the whole recipe for software that only needs unpacking.
func unzipInstall(c *core.Ctx) error {
	file, err := c.Fetch()
	if err != nil {
		return err
	}
	c.Step("解压")
	return c.Unpack(file)
}

func localURL(port int) string { return fmt.Sprintf("http://localhost:%d", port) }

func gh(repo, tag, file string) string {
	return "https://github.com/" + repo + "/releases/download/" + tag + "/" + file
}
