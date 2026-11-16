package recipes

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"devnest/internal/core"
	"devnest/internal/platform"
)

// These tests drive the whole engine against the simulated platform. They
// prove the bookkeeping (state, environment variables, services, undo), not
// that the real installers behave: that needs Windows.

func newSim(t *testing.T) (*core.Manager, *platform.Sim) {
	t.Helper()
	sim := platform.NewSim(t.TempDir())
	sim.Delay = 0
	return core.NewManager(sim), sim
}

// settle waits for the queue and fails the test if any of the tasks failed.
func settle(t *testing.T, m *core.Manager, what string, ids ...int) {
	t.Helper()
	m.WaitIdle()
	want := map[int]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, task := range m.Tasks() {
		if len(ids) > 0 && !want[task.ID] {
			continue
		}
		if task.State != "done" {
			t.Fatalf("%s: task %d %q ended %s: %s\n%s", what, task.ID, task.Title, task.State, task.Error,
				strings.Join(m.TaskLog(task.ID), "\n"))
		}
	}
}

func cleanSeed(t *testing.T, m *core.Manager) {
	t.Helper()
	found, err := m.Scan(nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, f := range found {
		ids = append(ids, f.ID)
	}
	id, err := m.Clean(ids)
	if err != nil {
		t.Fatal(err)
	}
	settle(t, m, "clean", id)
}

func systemPath(sim *platform.Sim) string {
	v, _ := sim.GetEnv(platform.System, "Path")
	return v
}

func TestScanFindsAndCleansTheSeededMachine(t *testing.T) {
	m, sim := newSim(t)
	found, err := m.Scan(nil)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, f := range found {
		kinds[f.PkgID+"/"+f.Kind]++
	}
	for _, want := range []string{"mysql/service", "mysql/uninstall", "mysql/path", "jdk/env", "jdk/uninstall", "node/uninstall"} {
		if kinds[want] == 0 {
			t.Errorf("scan did not report %s; got %v", want, kinds)
		}
	}
	for _, f := range found {
		if strings.Contains(f.Title, "7-Zip") || strings.Contains(f.Title, "Spooler") {
			t.Errorf("scan reported unrelated software: %+v", f)
		}
	}

	cleanSeed(t, m)

	if _, ok := sim.Service("MySQL57"); ok {
		t.Error("service MySQL57 is still there after cleaning")
	}
	if _, ok := sim.Service("Spooler"); !ok {
		t.Error("cleaning removed the unrelated Spooler service")
	}
	if v, ok := sim.GetEnv(platform.System, "JAVA_HOME"); ok {
		t.Errorf("JAVA_HOME still set to %q", v)
	}
	if p := strings.ToLower(systemPath(sim)); strings.Contains(p, "mysql") || strings.Contains(p, "nodejs") || strings.Contains(p, "java_home") {
		t.Errorf("PATH still has old entries: %s", p)
	}
	if p := systemPath(sim); !strings.Contains(strings.ToLower(p), "system32") {
		t.Errorf("cleaning damaged the Windows part of PATH: %s", p)
	}
	entries, _ := sim.UninstallEntries()
	for _, e := range entries {
		if strings.HasPrefix(e.DisplayName, "MySQL") {
			t.Errorf("%s is still installed", e.DisplayName)
		}
	}
	keep := false
	for _, e := range entries {
		keep = keep || strings.Contains(e.DisplayName, "7-Zip")
	}
	if !keep {
		t.Error("cleaning removed 7-Zip, which is not development software")
	}
	backups, _ := filepath.Glob(filepath.Join(m.BackupRoot(), "*"))
	if len(backups) == 0 {
		t.Error("cleaning left no backup folder")
	}

	again, err := m.Scan(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range again {
		if f.Kind == "service" || f.Kind == "uninstall" || f.Kind == "env" || f.Kind == "path" {
			t.Errorf("second scan still reports %s %s (%s)", f.PkgID, f.Kind, f.Title)
		}
	}
}

// A folder a Windows service still runs from is not a leftover, whatever the
// rules say about its name: the machines GitHub runs the tests on have Docker
// Engine in the folder the rules know as Docker Desktop's.
func TestAFolderAServiceRunsFromIsLeftAlone(t *testing.T) {
	m, sim := newSim(t)
	pf, _ := sim.GetEnv(platform.System, "ProgramFiles")
	dockerd := filepath.Join(pf, "Docker", "dockerd.exe")
	os.MkdirAll(filepath.Dir(dockerd), 0o755)
	os.WriteFile(dockerd, []byte("sim"), 0o644)
	sim.SimRegisterService("docker", `"`+dockerd+`" --run-service`)

	found, err := m.Scan([]string{"docker"})
	if err != nil {
		t.Fatal(err)
	}
	var dir *core.Finding
	for i := range found {
		if found[i].Kind == "dir" && found[i].Detail == filepath.Dir(dockerd) {
			dir = &found[i]
		}
	}
	if dir == nil {
		t.Fatalf("the scan did not list the folder at all: %+v", found)
	}
	if dir.Checked || !strings.Contains(dir.Note, "docker") {
		t.Errorf("the folder should be unticked and say which service uses it: %+v", *dir)
	}

	// Ticking it by hand does not get it deleted either.
	id, err := m.Clean([]string{dir.ID})
	if err != nil {
		t.Fatal(err)
	}
	m.WaitIdle()
	for _, task := range m.Tasks() {
		if task.ID == id && task.State == "done" {
			t.Errorf("cleaning reported success:\n%s", strings.Join(m.TaskLog(id), "\n"))
		}
	}
	if _, err := os.Stat(dockerd); err != nil {
		t.Errorf("the service's program was deleted: %v", err)
	}
}

// The machines GitHub runs the tests on have Pythons that only the registry
// knows about. Deleting the registration and leaving the files hides them
// from everything that looks there, and removes nothing.
func TestTheRegistrationOfAPythonThatStaysIsLeftAlone(t *testing.T) {
	const key = `HKLM\SOFTWARE\Python\PythonCore`
	m, sim := newSim(t)
	home := filepath.Join(t.TempDir(), "toolcache", "Python", "3.12.10", "x64")
	os.MkdirAll(home, 0o755)
	os.WriteFile(filepath.Join(home, "python.exe"), []byte("sim"), 0o644)
	sim.SimSetReg(key+`\3.12\InstallPath`, "", home+string(filepath.Separator))

	find := func() *core.Finding {
		found, err := m.Scan([]string{"python"})
		if err != nil {
			t.Fatal(err)
		}
		for i := range found {
			if found[i].Kind == "reg" && found[i].Detail == key {
				return &found[i]
			}
		}
		t.Fatalf("the scan did not list %s: %+v", key, found)
		return nil
	}
	if f := find(); f.Checked || !strings.Contains(f.Note, home) {
		t.Errorf("the key should be unticked and name the installation: %+v", *f)
	}

	// Once the files are gone the key is a leftover, and cleaning removes it
	// together with what is below it.
	os.RemoveAll(home)
	f := find()
	if !f.Checked {
		t.Fatalf("the key of a Python that is gone should be ticked: %+v", *f)
	}
	id, err := m.Clean([]string{f.ID})
	if err != nil {
		t.Fatal(err)
	}
	settle(t, m, "clean", id)
	if sim.RegKeyExists(key) || len(sim.RegSubKeys(key)) > 0 {
		t.Errorf("the key is still there after cleaning")
	}
}

// Machines set up by hand often have npm's global folder moved, with
// npm_config_prefix (C:\npm\prefix on the GitHub machines) or with `npm
// config set prefix` (as many tutorials say), and that folder on PATH.
// Cleaning Node.js takes the settings and their PATH entries, so global
// commands go where the new Node.js puts them; the folders are only listed.
func TestCleaningNodeTakesTheMovedNpmGlobalFolder(t *testing.T) {
	m, sim := newSim(t)
	machine := filepath.Join(filepath.Dir(sim.Dir("programfiles")), "npm", "prefix")
	tutorial := filepath.Join(sim.Drives()[1].Root, "nodejs", "node_global")
	for _, dir := range []string{machine, tutorial} {
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "yarn.cmd"), []byte("sim"), 0o644)
	}
	sim.SetEnv(platform.System, "npm_config_prefix", machine)
	sim.SetEnv(platform.System, "Path", systemPath(sim)+";"+machine)
	userPath, _ := sim.GetEnv(platform.User, "Path")
	sim.SetEnv(platform.User, "Path", userPath+";"+tutorial+string(filepath.Separator))
	npmrc := filepath.Join(sim.Dir("home"), ".npmrc")
	os.WriteFile(npmrc, []byte("registry=https://registry.npmmirror.com/\r\nprefix="+tutorial+"\r\ncache=D:\\nodejs\\node_cache\r\n"), 0o644)

	found, err := m.Scan([]string{"node"})
	if err != nil {
		t.Fatal(err)
	}
	find := func(kind, detail string) *core.Finding {
		for i := range found {
			if found[i].Kind == kind && platform.NormPath(found[i].Detail) == platform.NormPath(detail) {
				return &found[i]
			}
		}
		t.Errorf("the scan did not list %s %s", kind, detail)
		return nil
	}
	for _, want := range [][2]string{{"env", machine}, {"conf", "prefix=" + tutorial}, {"path", machine}, {"path", tutorial}} {
		if f := find(want[0], want[1]); f != nil && !f.Checked {
			t.Errorf("%s %s should be ticked", want[0], want[1])
		}
	}
	for _, dir := range []string{machine, tutorial} {
		if f := find("dir", dir); f != nil && (f.Checked || !strings.Contains(f.Note, "npm install -g")) {
			t.Errorf("the folder should be listed unticked, saying what is in it: %+v", *f)
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	var ids []string
	for _, f := range found {
		if f.Checked {
			ids = append(ids, f.ID)
		}
	}
	id, err := m.Clean(ids)
	if err != nil {
		t.Fatal(err)
	}
	settle(t, m, "clean", id)

	if v, ok := sim.GetEnv(platform.System, "npm_config_prefix"); ok {
		t.Errorf("npm_config_prefix still set to %q", v)
	}
	for _, scope := range []platform.Scope{platform.System, platform.User} {
		for _, e := range platform.PathEntries(sim, scope) {
			if n := platform.NormPath(e); n == platform.NormPath(machine) || n == platform.NormPath(tutorial) {
				t.Errorf("PATH still has %s", e)
			}
		}
	}
	rc, _ := os.ReadFile(npmrc)
	if core.GetKeyLine(string(rc), "prefix") != "" || !strings.Contains(string(rc), "registry=") || !strings.Contains(string(rc), "cache=") {
		t.Errorf("only the prefix line should be gone from .npmrc:\n%s", rc)
	}
	for _, dir := range []string{machine, tutorial} {
		if _, err := os.Stat(filepath.Join(dir, "yarn.cmd")); err != nil {
			t.Errorf("the unticked folder %s was deleted", dir)
		}
	}
}

// Wherever an old installation moved npm's global folder, pnpm and yarn go
// into the folder of the Node.js version being installed, which is on PATH.
func TestPnpmAndYarnGoIntoTheNodeVersionFolder(t *testing.T) {
	m, sim := newSim(t)
	var args []string
	sim.OnRun = func(exe string, a []string, _ platform.RunOpts) {
		if strings.EqualFold(filepath.Base(exe), "npm.cmd") {
			args = a
		}
	}
	ids, err := m.Install(core.InstallReq{ID: "node"})
	if err != nil {
		t.Fatal(err)
	}
	settle(t, m, "install node", ids...)
	dir := m.VersionsOf("node")[0].Dir
	if i := slices.Index(args, "--prefix"); i < 0 || i+1 == len(args) || args[i+1] != dir {
		t.Errorf("npm install -g should get the version folder %s as its prefix; ran npm %v", dir, args)
	}
	if !slices.Contains(args, "--allow-scripts=pnpm,yarn") {
		t.Errorf("npm install -g should allow the install scripts of pnpm and yarn; ran npm %v", args)
	}
}

func TestCleanRefusesIDsNotFromTheLastScan(t *testing.T) {
	m, _ := newSim(t)
	if _, err := m.Clean([]string{"made-up"}); err == nil {
		t.Fatal("Clean accepted an id that no scan produced")
	}
}

func TestInstallRefusesAPortThatIsTaken(t *testing.T) {
	m, _ := newSim(t)
	ids, err := m.Install(core.InstallReq{ID: "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	m.WaitIdle()
	failed := ""
	for _, task := range m.Tasks() {
		if task.ID == ids[len(ids)-1] {
			failed = task.State + ": " + task.Error
		}
	}
	if !strings.HasPrefix(failed, "failed") || !strings.Contains(failed, "3306") {
		t.Fatalf("installing MySQL on a port the old MySQL57 uses should fail and name the port; got %q", failed)
	}
	if len(m.VersionsOf("mysql")) != 0 {
		t.Error("a failed install was left recorded as installed")
	}
	if _, err := os.Stat(filepath.Join(m.AppsDir(), "mysql")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(m.AppsDir(), "mysql"))
		if len(entries) > 0 {
			t.Errorf("a failed install left files behind: %v", entries)
		}
	}
}

func TestEveryPackageInstallsAndUninstalls(t *testing.T) {
	m, sim := newSim(t)
	cleanSeed(t, m)
	pathBefore := systemPath(sim)
	servicesBefore, _ := sim.Services()

	override := map[string]map[string]string{"nacos": {"console": "8081"}}
	for _, p := range core.Packages() {
		if len(m.VersionsOf(p.ID)) > 0 {
			continue // came in as a dependency
		}
		ids, err := m.Install(core.InstallReq{ID: p.ID, Options: override[p.ID]})
		if err != nil {
			t.Fatalf("%s: %v", p.ID, err)
		}
		settle(t, m, "install "+p.ID, ids...)
		got := m.VersionsOf(p.ID)
		if len(got) != 1 {
			t.Fatalf("%s: %d versions recorded after install", p.ID, len(got))
		}
		for _, rel := range p.Probe {
			if _, err := os.Stat(filepath.Join(got[0].Dir, filepath.FromSlash(rel))); err != nil {
				t.Errorf("%s: expected file %s is missing", p.ID, rel)
			}
		}
		for _, d := range p.Requires {
			if _, ok := m.Find(d); !ok {
				t.Errorf("%s: dependency %s was not installed", p.ID, d)
			}
		}
	}

	cat := m.Catalog()
	if len(cat) != len(core.Packages()) {
		t.Fatalf("catalog has %d packages, want %d", len(cat), len(core.Packages()))
	}
	rows := m.Services()
	names := map[string]bool{}
	for _, r := range rows {
		names[r.Name] = true
		if r.State != "running" {
			t.Errorf("service %s is %s after install", r.Name, r.State)
		}
	}
	for _, want := range []string{"MySQL", "Redis", "Neo4j", "PostgreSQL", "MongoDB", "MariaDB", "Tomcat", "Nginx", "RabbitMQ", "Kafka", "Elasticsearch", "Nacos"} {
		if !names[want] {
			t.Errorf("service %s is not in the service panel", want)
		}
	}
	for _, v := range []string{"JAVA_HOME", "NODE_HOME", "PYTHON_HOME", "GO_HOME", "MAVEN_HOME"} {
		if got, ok := sim.GetEnv(platform.System, v); !ok || got == "" {
			t.Errorf("%s is not set after install", v)
		}
	}

	// A second install of something already there must be refused.
	if _, err := m.Install(core.InstallReq{ID: "redis"}); err == nil {
		t.Error("installing Redis twice was accepted")
	}
	// A JDK that Neo4j, Kafka and Nacos run on must not be removable.
	jdk := m.VersionsOf("jdk")[0]
	if _, err := m.Uninstall("jdk", jdk.Version, "keep"); err == nil {
		t.Error("uninstalling the JDK was accepted while services depend on it")
		m.WaitIdle()
	}

	for round := 0; len(m.Installed()) > 0; round++ {
		if round > 10 {
			t.Fatalf("cannot uninstall: %d packages left", len(m.Installed()))
		}
		for _, inst := range m.Installed() {
			id, err := m.Uninstall(inst.ID, inst.Version, "delete")
			if err != nil {
				continue // something still depends on it; next round
			}
			settle(t, m, "uninstall "+inst.ID, id)
		}
	}

	if got := systemPath(sim); got != pathBefore {
		t.Errorf("PATH differs after uninstalling everything:\n before %s\n after  %s", pathBefore, got)
	}
	servicesAfter, _ := sim.Services()
	if len(servicesAfter) != len(servicesBefore) {
		t.Errorf("%d services before, %d after uninstalling everything", len(servicesBefore), len(servicesAfter))
	}
	for _, v := range []string{"JAVA_HOME", "NODE_HOME", "PYTHON_HOME", "GO_HOME", "MAVEN_HOME", "ERLANG_HOME", "RABBITMQ_BASE"} {
		if got, ok := sim.GetEnv(platform.System, v); ok {
			t.Errorf("%s still set to %q after uninstalling everything", v, got)
		}
	}
	left, _ := os.ReadDir(m.AppsDir())
	for _, e := range left {
		sub, _ := os.ReadDir(filepath.Join(m.AppsDir(), e.Name()))
		if len(sub) > 0 {
			t.Errorf("files left under apps/%s after uninstall", e.Name())
		}
	}
}

func TestSwitchingBetweenTwoJDKs(t *testing.T) {
	m, sim := newSim(t)
	cleanSeed(t, m)
	install := func(version string) core.Installed {
		ids, err := m.Install(core.InstallReq{ID: "jdk", Version: version})
		if err != nil {
			t.Fatal(err)
		}
		settle(t, m, "install jdk "+version, ids...)
		for _, i := range m.VersionsOf("jdk") {
			if i.Version == version {
				return i
			}
		}
		t.Fatalf("jdk %s not recorded", version)
		return core.Installed{}
	}
	pkg := core.Lookup("jdk")
	v21, v17 := pkg.Releases[0].Version, ""
	for _, r := range pkg.Releases {
		if core.Major(r.Version) == 17 {
			v17 = r.Version
		}
	}
	if core.Major(v21) != 21 || v17 == "" {
		t.Fatalf("catalogue should offer JDK 21 first and a JDK 17; got %s and %q", v21, v17)
	}
	a := install(v21)
	b := install(v17)

	home := func() string { v, _ := sim.GetEnv(platform.System, "JAVA_HOME"); return v }
	if home() != b.Dir {
		t.Errorf("after installing 17 last, JAVA_HOME = %q, want %q", home(), b.Dir)
	}
	id, err := m.Activate("jdk", v21)
	if err != nil {
		t.Fatal(err)
	}
	settle(t, m, "activate", id)
	if home() != a.Dir {
		t.Errorf("after switching to 21, JAVA_HOME = %q, want %q", home(), a.Dir)
	}
	if n := strings.Count(strings.ToUpper(systemPath(sim)), "%JAVA_HOME%"); n != 1 {
		t.Errorf("PATH has %d %%JAVA_HOME%% entries, want exactly 1: %s", n, systemPath(sim))
	}

	id, err = m.Uninstall("jdk", v21, "keep")
	if err != nil {
		t.Fatal(err)
	}
	settle(t, m, "uninstall active jdk", id)
	if home() != b.Dir {
		t.Errorf("after removing the active JDK, JAVA_HOME = %q, want the remaining one %q", home(), b.Dir)
	}
}

func TestKeptDataIsReusedOnReinstall(t *testing.T) {
	m, _ := newSim(t)
	cleanSeed(t, m)
	ids, err := m.Install(core.InstallReq{ID: "redis"})
	if err != nil {
		t.Fatal(err)
	}
	settle(t, m, "install", ids...)
	inst := m.VersionsOf("redis")[0]
	marker := filepath.Join(inst.DataDir, "dump.rdb")
	if err := os.WriteFile(marker, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	id, err := m.Uninstall("redis", inst.Version, "keep")
	if err != nil {
		t.Fatal(err)
	}
	settle(t, m, "uninstall keep", id)
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("uninstalling with \"keep\" deleted the data")
	}

	ids, err = m.Install(core.InstallReq{ID: "redis"})
	if err != nil {
		t.Fatal(err)
	}
	settle(t, m, "reinstall", ids...)
	id, err = m.Uninstall("redis", inst.Version, "backup")
	if err != nil {
		t.Fatal(err)
	}
	settle(t, m, "uninstall backup", id)
	if _, err := os.Stat(marker); err == nil {
		t.Error("uninstalling with \"backup\" left the data in place")
	}
	moved, _ := filepath.Glob(filepath.Join(m.BackupRoot(), "*", "*", "dump.rdb"))
	if len(moved) != 1 {
		t.Errorf("expected the data in exactly one backup folder, found %v", moved)
	}
}

func TestMirrors(t *testing.T) {
	m, _ := newSim(t)
	if problems := m.SetAllMirrors(); len(problems) > 0 {
		t.Fatalf("SetAllMirrors: %v", problems)
	}
	for _, item := range m.Mirrors() {
		if len(item.Choices) > 0 && item.Current != item.Choices[0].Value {
			t.Errorf("%s: current = %q, want the recommended %q", item.ID, item.Current, item.Choices[0].Value)
		}
	}
	for _, item := range m.Mirrors() {
		if len(item.Choices) == 0 {
			continue
		}
		if err := m.SetMirror(item.ID, ""); err != nil {
			t.Errorf("%s: restore official source: %v", item.ID, err)
		}
	}
	for _, item := range m.Mirrors() {
		if item.Current != "" {
			t.Errorf("%s: current = %q after restoring the official source", item.ID, item.Current)
		}
	}
	if err := m.SetMirror("npm", "ftp://x"); err == nil {
		t.Error("a non-http mirror address was accepted")
	}
}

func TestPresetsOnlyNameRealPackages(t *testing.T) {
	m, _ := newSim(t)
	presets := m.Presets()
	if len(presets) < 6 {
		t.Fatalf("only %d presets are offered; some name a package that does not exist", len(presets))
	}
	for _, p := range presets {
		for _, item := range p.Items {
			id, ver, _ := strings.Cut(item, "@")
			pkg := core.Lookup(id)
			if pkg == nil {
				t.Errorf("preset %s: no package %s", p.ID, id)
				continue
			}
			if ver != "" {
				ok := false
				for _, r := range pkg.Releases {
					ok = ok || strings.HasPrefix(r.Version, ver)
				}
				if !ok {
					t.Errorf("preset %s: %s has no version starting with %s", p.ID, id, ver)
				}
			}
		}
	}
}

func TestCatalogueIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range core.Packages() {
		if seen[p.ID] {
			t.Errorf("duplicate package id %s", p.ID)
		}
		seen[p.ID] = true
		if p.Name == "" || p.Desc == "" || p.Install == nil || len(p.Releases) == 0 {
			t.Errorf("%s: missing name, description, install steps or releases", p.ID)
		}
		if len(p.Probe) == 0 && p.Category != "dep" {
			t.Errorf("%s: no probe files, so a broken install would go unnoticed", p.ID)
		}
		if !slices.Contains([]string{"lang", "db", "dbtool", "mw", "ide", "tool", "dep"}, p.Category) {
			t.Errorf("%s: unknown category %q", p.ID, p.Category)
		}
		for _, r := range p.Releases {
			if r.Version == "" || r.File == "" || len(r.URLs) == 0 {
				t.Errorf("%s %s: missing version, file name or download address", p.ID, r.Version)
			}
			for _, u := range r.URLs {
				if !strings.HasPrefix(u, "https://") {
					t.Errorf("%s %s: download address is not https: %s", p.ID, r.Version, u)
				}
			}
			if strings.ContainsAny(r.File, ` /\`) {
				t.Errorf("%s %s: file name %q has a space or a slash", p.ID, r.Version, r.File)
			}
		}
		for _, d := range p.Requires {
			dep := core.Lookup(d.ID)
			if dep == nil {
				t.Errorf("%s: requires unknown package %s", p.ID, d.ID)
				continue
			}
			ok := false
			for _, r := range dep.Releases {
				ok = ok || d.Matches(r.Version)
			}
			if !ok {
				t.Errorf("%s: no release of %s satisfies %s", p.ID, d.ID, d)
			}
		}
		if _, err := p.CheckOptions(nil); err != nil {
			t.Errorf("%s: its own default options are rejected: %v", p.ID, err)
		}
	}
}

func TestSetConf(t *testing.T) {
	in := "# port 1\r\nport 6379\r\nbind 0.0.0.0\r\nport 7000\r\n"
	got := setConf(in, "port", " ", "6380")
	if got != "# port 1\r\nport 6380\r\nbind 0.0.0.0\r\n" {
		t.Errorf("replace: got %q", got)
	}
	got = setConf("a=1\n", "b", "=", "2")
	if got != "a=1\nb=2\n" {
		t.Errorf("append: got %q", got)
	}
	got = setConf("portal 9\n", "port", " ", "1")
	if got != "portal 9\nport 1\n" {
		t.Errorf("a key that is only a prefix of another was replaced: got %q", got)
	}
}

func TestNginxListenRewritesOnlyTheFirstActiveLine(t *testing.T) {
	in := "http {\n    server {\n        listen       80;\n    }\n    #server {\n    #    listen       8000;\n    #}\n    server {\n        listen 443 ssl;\n    }\n}\n"
	done := false
	got := nginxListen.ReplaceAllStringFunc(in, func(m string) string {
		if done {
			return m
		}
		done = true
		return nginxListen.ReplaceAllString(m, "${1}listen       127.0.0.1:8088;")
	})
	want := strings.Replace(in, "listen       80;", "listen       127.0.0.1:8088;", 1)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The lines are the ones Nacos 3.2.0 ships in application.properties.
func TestNacosPropsFillsInTheEmptySecrets(t *testing.T) {
	shipped := strings.Join([]string{
		"nacos.server.main.port=8848",
		"nacos.console.port=8080",
		"### The two properties is the white list for auth and used by identity the request from other server.",
		"nacos.core.auth.server.identity.key=",
		"nacos.core.auth.server.identity.value=",
		"",
		"### The token expiration in seconds:",
		"nacos.core.auth.plugin.nacos.token.expire.seconds=18000",
		"### The default token (Base64 string):",
		"#nacos.core.auth.plugin.nacos.token.secret.key=VGhpc0lzTXlDdXN0b21TZWNyZXRLZXkwMTIzNDU2Nzg=",
		"nacos.core.auth.plugin.nacos.token.secret.key=",
		"",
	}, "\r\n")
	keys := []string{
		"nacos.core.auth.server.identity.key",
		"nacos.core.auth.server.identity.value",
		"nacos.core.auth.plugin.nacos.token.secret.key",
	}
	out := nacosProps(shipped, 8850, 8089)
	for _, key := range keys {
		v := core.GetKeyLine(out, key)
		if v == "" || strings.Contains(v, "nacos.") || strings.Contains(v, "#") {
			t.Errorf("%s was not filled in: %q", key, v)
		}
		if n := strings.Count(out, "\n"+key+"="); n != 1 {
			t.Errorf("%s appears %d times", key, n)
		}
	}
	if !strings.Contains(out, "nacos.server.main.port=8850\r\n") || !strings.Contains(out, "nacos.console.port=8089\r\n") {
		t.Errorf("ports were not set:\n%s", out)
	}
	if !strings.Contains(out, "token.expire.seconds=18000") {
		t.Errorf("an unrelated line was lost:\n%s", out)
	}
	// A second run keeps what is there: changing the secrets under existing
	// data would lock out everything that already has a token.
	again := nacosProps(out, 8850, 8089)
	for _, key := range keys {
		if core.GetKeyLine(again, key) != core.GetKeyLine(out, key) {
			t.Errorf("%s changed on the second run", key)
		}
	}
}

func TestKafkaUUID(t *testing.T) {
	for i := 0; i < 200; i++ {
		id := kafkaUUID()
		if len(id) != 22 || strings.HasPrefix(id, "-") || strings.ContainsAny(id, "+/=") {
			t.Fatalf("bad cluster id %q", id)
		}
	}
}
