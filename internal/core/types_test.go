package core

import (
	"strings"
	"testing"
)

func optionPkg() *Package {
	return &Package{ID: "x", Name: "X", Options: []Option{
		{Key: "port", Label: "端口", Type: "port", Default: "3306"},
		{Key: "password", Label: "密码", Type: "password", Default: "root", MinLen: 4},
		{Key: "lan", Label: "局域网", Type: "bool", Default: "false"},
		{Key: "mode", Label: "模式", Type: "select", Default: "a", Choices: []Choice{{"a", "A"}, {"b", "B"}}},
	}}
}

func TestCheckOptionsFillsDefaults(t *testing.T) {
	got, err := optionPkg().CheckOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"port": "3306", "password": "root", "lan": "false", "mode": "a"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestCheckOptionsRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]string
		msg  string
	}{
		{"port is not a number", map[string]string{"port": "abc"}, "端口"},
		{"port out of range", map[string]string{"port": "70000"}, "端口"},
		{"password too short", map[string]string{"password": "abc"}, "至少 4 位"},
		{"password too long", map[string]string{"password": strings.Repeat("a", 33)}, "最多 32 位"},
		{"password with a quote", map[string]string{"password": `ab"cd`}, "只能用"},
		{"password with a space", map[string]string{"password": "ab cd"}, "只能用"},
		{"unknown choice", map[string]string{"mode": "z"}, "无效的选项"},
	}
	for _, c := range cases {
		_, err := optionPkg().CheckOptions(c.in)
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: got error %v, want one mentioning %q", c.name, err, c.msg)
		}
	}
}

func TestCheckOptionsRefusesWeakPasswordOnTheNetwork(t *testing.T) {
	p := optionPkg()
	for name, pw := range map[string]string{"the default": "root", "short": "abc1234"} {
		if _, err := p.CheckOptions(map[string]string{"lan": "true", "password": pw}); err == nil {
			t.Errorf("%s password was accepted for a service open to the network", name)
		}
	}
	if _, err := p.CheckOptions(map[string]string{"lan": "true", "password": "Str0ng-Pass"}); err != nil {
		t.Errorf("a strong password was refused: %v", err)
	}
	if _, err := p.CheckOptions(map[string]string{"lan": "false", "password": "root"}); err != nil {
		t.Errorf("the default password must stay usable for local-only installs: %v", err)
	}

	// Redis style: the password may be empty locally but not on the network.
	r := &Package{ID: "r", Options: []Option{
		{Key: "password", Label: "访问密码", Type: "password", Default: ""},
		{Key: "lan", Type: "bool", Default: "false"},
	}}
	if _, err := r.CheckOptions(map[string]string{"lan": "true"}); err == nil {
		t.Error("an empty password was accepted for a service open to the network")
	}
	if _, err := r.CheckOptions(nil); err != nil {
		t.Errorf("an empty password must stay usable for local-only installs: %v", err)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"21.0.13", "21.0.9", 1}, {"8.0.46", "8.4.11", -1}, {"3.14.10", "3.14.10", 0}, {"10.1", "9.0.124", 1}} {
		got := CompareVersions(c.a, c.b)
		if (got > 0) != (c.want > 0) || (got < 0) != (c.want < 0) {
			t.Errorf("CompareVersions(%q, %q) = %d, want sign of %d", c.a, c.b, got, c.want)
		}
	}
}

func TestGetKeyLineStaysOnItsLine(t *testing.T) {
	conf := "a.key=\r\na.value=\r\n\r\n### a comment\r\n#b.key=old\r\nb.key=\r\nc.key = \"quoted\"\r\nd.key=plain\n// e: spaced\n"
	for key, want := range map[string]string{
		"a.key": "", "a.value": "", "b.key": "", "c.key": "quoted", "d.key": "plain", "// e:": "spaced", "missing": "",
	} {
		if got := GetKeyLine(conf, key); got != want {
			t.Errorf("GetKeyLine(%q) = %q, want %q", key, got, want)
		}
	}
}
