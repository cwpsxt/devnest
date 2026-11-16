package core

import (
	"testing"

	"devnest/internal/platform"
)

func TestQuietUninstall(t *testing.T) {
	cases := []struct {
		name  string
		entry platform.UninstallEntry
		want  string
		ok    bool
	}{
		{"the vendor's own quiet command wins",
			platform.UninstallEntry{UninstallString: `"C:\App\uninstall.exe"`, QuietUninstallString: `"C:\App\uninstall.exe" /quiet`},
			`"C:\App\uninstall.exe" /quiet`, true},
		{"the EDB PostgreSQL uninstaller is InstallBuilder, not NSIS",
			platform.UninstallEntry{UninstallString: `"C:\Program Files\PostgreSQL\17\uninstall-postgresql.exe"`},
			`"C:\Program Files\PostgreSQL\17\uninstall-postgresql.exe" --mode unattended`, true},
		{"msi installed with /I is removed with /x",
			platform.UninstallEntry{UninstallString: `MsiExec.exe /I{3D4C8E4E-0000-4A6B-9C3D-000000000057}`},
			`msiexec.exe /x {3D4C8E4E-0000-4A6B-9C3D-000000000057} /qn /norestart`, true},
		{"msi with /X and a space",
			platform.UninstallEntry{UninstallString: `MsiExec.exe /X {64A3A4F4-B792-11D6-A78A-00B0D0180202}`},
			`msiexec.exe /x {64A3A4F4-B792-11D6-A78A-00B0D0180202} /qn /norestart`, true},
		{"inno setup",
			platform.UninstallEntry{UninstallString: `"C:\Program Files\Git\unins000.exe"`},
			`"C:\Program Files\Git\unins000.exe" /VERYSILENT /SUPPRESSMSGBOXES /NORESTART`, true},
		{"inno setup without quotes",
			platform.UninstallEntry{UninstallString: `C:\Git\unins000.exe`},
			`"C:\Git\unins000.exe" /VERYSILENT /SUPPRESSMSGBOXES /NORESTART`, true},
		{"wix bundle such as python",
			platform.UninstallEntry{UninstallString: `"C:\ProgramData\Package Cache\{abc}\python-3.12.0-amd64.exe" /uninstall`},
			`"C:\ProgramData\Package Cache\{abc}\python-3.12.0-amd64.exe" /uninstall /quiet /norestart`, true},
		{"nsis",
			platform.UninstallEntry{UninstallString: `"C:\Program Files\Erlang OTP\Uninstall.exe"`},
			`"C:\Program Files\Erlang OTP\Uninstall.exe" /S`, true},
		{"docker desktop",
			platform.UninstallEntry{UninstallString: `"C:\Program Files\Docker\Docker\Docker Desktop Installer.exe" uninstall`},
			`"C:\Program Files\Docker\Docker\Docker Desktop Installer.exe" uninstall --quiet`, true},
		{"an unknown installer is left to the user",
			platform.UninstallEntry{UninstallString: `"C:\Program Files\Thing\remove.exe" --gui`}, "", false},
		{"nothing recorded", platform.UninstallEntry{}, "", false},
	}
	for _, c := range cases {
		got, ok := quietUninstall(c.entry)
		if got != c.want || ok != c.ok {
			t.Errorf("%s:\n got  %q, %v\n want %q, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestMatchAnyTreatsSlashesAsBackslashes(t *testing.T) {
	rx := []string{`(?i)\\mysql[^\\]*\\bin\\?$`}
	for _, s := range []string{`C:\Program Files\MySQL\bin`, `C:/Program Files/MySQL/bin`, `c:\mysql\bin\`} {
		if !matchAny(rx, s) {
			t.Errorf("%q should match", s)
		}
	}
	if matchAny(rx, `C:\Program Files\MySQL\docs`) {
		t.Error("a docs folder is not a bin folder")
	}
}

func TestExeOf(t *testing.T) {
	for in, want := range map[string]string{
		`"C:\Program Files\MySQL\bin\mysqld.exe" --defaults-file=x MySQL`: `C:\Program Files\MySQL\bin\mysqld.exe`,
		`C:\redis\redis-server.exe --service-run`:                         `C:\redis\redis-server.exe`,
		`C:\tools\thing`: `C:\tools\thing`,
	} {
		if got := exeOf(in); got != want {
			t.Errorf("exeOf(%q) = %q, want %q", in, got, want)
		}
	}
}
