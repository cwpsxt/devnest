<#
.SYNOPSIS
  Installs real software on a real Windows machine and checks that it works.

.DESCRIPTION
  The simulated tests prove the bookkeeping; only this proves the recipes.
  For every package in the chosen group the script installs it with
  devnestcli, then checks from the outside: the command resolves in a newly
  opened terminal, the Windows service runs, the port answers, the database
  accepts the right password and refuses a wrong one. Then it uninstalls and
  checks that the service, the folder and the port are gone.

  It changes the machine it runs on. It is meant for a throw-away CI runner.

.EXAMPLE
  pwsh scripts/ci/integration.ps1 -Cli build\bin\devnestcli.exe -Group mysql
#>
param(
  [Parameter(Mandatory = $true)][string]$Cli,
  [Parameter(Mandatory = $true)][string]$Group
)

$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$Cli = (Resolve-Path $Cli).Path
$Root = 'C:\DevNest'

# A throw-away password, handed to devnestcli through the environment so it
# never appears on a command line.
$Password = 'Dn' + [guid]::NewGuid().ToString('N').Substring(0, 14)
Write-Host "::add-mask::$Password"
$env:DN_PW = $Password

function Invoke-Cli {
  Write-Host "`n=== devnestcli $($args -join ' ')" -ForegroundColor Cyan
  & $Cli @args
  if ($LASTEXITCODE -ne 0) { throw "devnestcli $($args -join ' ') exited with $LASTEXITCODE" }
}

function Get-CliJson {
  $text = (& $Cli @args | Out-String)
  if ($LASTEXITCODE -ne 0) { throw "devnestcli $($args -join ' ') exited with $LASTEXITCODE" }
  return $text | ConvertFrom-Json
}

# Makes this process see what a newly opened terminal would: the variables
# stored in the registry, not the ones this process inherited.
function Update-ProcessEnv {
  $machine = [Microsoft.Win32.Registry]::LocalMachine.OpenSubKey('SYSTEM\CurrentControlSet\Control\Session Manager\Environment')
  $user = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment')
  $raw = [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames
  foreach ($key in @($machine, $user)) {
    foreach ($name in $key.GetValueNames()) {
      # USERNAME in the machine key is SYSTEM, and PowerShell builds its own
      # PSModulePath; a new terminal takes neither from here.
      if (-not $name -or $name -in 'Path', 'USERNAME', 'PSModulePath') { continue }
      $value = [Environment]::ExpandEnvironmentVariables([string]$key.GetValue($name, '', $raw))
      [Environment]::SetEnvironmentVariable($name, $value, 'Process')
    }
  }
  $parts = @([string]$machine.GetValue('Path', '', $raw), [string]$user.GetValue('Path', '', $raw)) | Where-Object { $_ }
  $env:Path = [Environment]::ExpandEnvironmentVariables($parts -join ';')
}

# Runs one command line through cmd.exe, the way a user would type it.
# The line must not contain a percent sign.
function Invoke-Shell([string]$Line) {
  Write-Host "> $Line" -ForegroundColor DarkGray
  $file = Join-Path $env:TEMP ('dn-' + [guid]::NewGuid().ToString('N') + '.cmd')
  Set-Content -Path $file -Value "@echo off`r`n$Line 2>&1`r`n" -Encoding ascii
  try {
    $out = (& cmd.exe /d /c $file | Out-String)
    $code = $LASTEXITCODE
  } finally {
    Remove-Item $file -ErrorAction SilentlyContinue
  }
  Write-Host $out
  return [pscustomobject]@{ Code = $code; Out = $out }
}

function Assert-Shell([string]$Line, [string]$Pattern = '') {
  $r = Invoke-Shell $Line
  if ($r.Code -ne 0) { throw "exit code $($r.Code) from: $Line" }
  if ($Pattern -and $r.Out -notmatch $Pattern) { throw "output of '$Line' does not match /$Pattern/" }
}

function Assert-ShellFails([string]$Line, [string]$Why) {
  $r = Invoke-Shell $Line
  if ($r.Code -eq 0) { throw "$Why (this should have failed: $Line)" }
}

# The command must resolve to the copy under the install folder, not to one
# the machine already had.
function Assert-Ours([string[]]$Commands) {
  foreach ($name in $Commands) {
    $found = Get-Command $name -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $found) { throw "'$name' is not on PATH" }
    if (-not $found.Source.StartsWith($Root, [StringComparison]::OrdinalIgnoreCase)) { throw "'$name' resolves to $($found.Source), not to the copy under $Root" }
    Write-Host "OK  $name -> $($found.Source)"
  }
}

function Test-Port([int]$Port) {
  $client = [Net.Sockets.TcpClient]::new()
  try {
    $client.Connect('127.0.0.1', $Port)
    return $client.Connected
  } catch {
    return $false
  } finally {
    $client.Dispose()
  }
}

function Assert-Http([string]$Url, [string]$Pattern = '', [int]$Seconds = 120) {
  $deadline = (Get-Date).AddSeconds($Seconds)
  $last = ''
  while ((Get-Date) -lt $deadline) {
    try {
      $resp = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 15
      if (-not $Pattern -or $resp.Content -match $Pattern) {
        Write-Host "OK  $Url  (HTTP $($resp.StatusCode))"
        return
      }
      $last = "HTTP $($resp.StatusCode), but the body does not match /$Pattern/"
    } catch {
      $last = $_.Exception.Message
    }
    Start-Sleep -Seconds 3
  }
  throw "$Url did not answer as expected within $Seconds s: $last"
}

# Checks, through Windows itself, that everything the package registered runs.
# A service that was just restarted gets $Seconds to open its ports.
function Assert-Healthy([string]$Id, [int]$Seconds = 120) {
  $rows = @(Get-CliJson services --json | Where-Object { $_.pkgId -eq $Id })
  if ($rows.Count -eq 0) { throw "$Id registered no service" }
  foreach ($row in $rows) {
    $svc = Get-Service -Name $row.name -ErrorAction SilentlyContinue
    if (-not $svc) { throw "service $($row.name) does not exist" }
    if ($svc.StartType -ne 'Automatic') { throw "service $($row.name) start type is $($svc.StartType)" }
    $started = Get-Date
    while ($true) {
      $svc.Refresh()
      $closed = @($row.ports | Where-Object { $_ -and -not (Test-Port $_.port) } | ForEach-Object { $_.port })
      if ($svc.Status -eq 'Running' -and $closed.Count -eq 0) { break }
      if (((Get-Date) - $started).TotalSeconds -ge $Seconds) {
        if ($svc.Status -ne 'Running') { throw "service $($row.name) is $($svc.Status)" }
        throw "service $($row.name) runs, but port $($closed -join ', ') does not answer after $Seconds s"
      }
      Start-Sleep -Seconds 2
    }
    $took = [int]((Get-Date) - $started).TotalSeconds
    Write-Host "OK  service $($row.name) is running, ports: $(($row.ports | ForEach-Object { $_.port }) -join ', ') (ready after $took s)"
  }
}

function Get-Install([string]$Ref) { return Get-CliJson info $Ref }

# Uninstalls and checks that nothing is left behind.
function Remove-Package([string]$Ref, [string]$Data = 'delete') {
  $info = Get-Install $Ref
  Invoke-Cli uninstall $Ref --data $Data
  if (Test-Path $info.dir) { throw "install folder still there after uninstall: $($info.dir)" }
  foreach ($name in $info.services) {
    if (Get-Service -Name $name -ErrorAction SilentlyContinue) { throw "service $name still there after uninstall" }
  }
  foreach ($p in $info.ports) {
    $tries = 0
    while ((Test-Port $p.port) -and $tries -lt 10) { Start-Sleep -Seconds 1; $tries++ }
    if (Test-Port $p.port) { throw "port $($p.port) still answers after uninstall" }
  }
  foreach ($name in $info.env) {
    if ([Environment]::GetEnvironmentVariable($name, 'Machine')) { throw "variable $name still set after uninstall" }
  }
  $machinePath = [string][Microsoft.Win32.Registry]::LocalMachine.OpenSubKey('SYSTEM\CurrentControlSet\Control\Session Manager\Environment').GetValue('Path', '', 'DoNotExpandEnvironmentNames')
  foreach ($entry in $info.paths) {
    if (($machinePath -split ';') -contains $entry) { throw "PATH still has $entry after uninstall" }
  }
  if ($info.dataDir) {
    $there = Test-Path $info.dataDir
    if ($Data -eq 'keep' -and -not $there) { throw "data folder should have been kept: $($info.dataDir)" }
    if ($Data -ne 'keep' -and $there -and (Get-ChildItem $info.dataDir -Force)) { throw "data folder still has files: $($info.dataDir)" }
  }
  Write-Host "OK  $Ref is gone"
}

# After the last version of a package is gone, its home variable and the
# PATH entries that follow it must be gone too.
function Assert-NoHome([string[]]$Names) {
  $machinePath = [string][Microsoft.Win32.Registry]::LocalMachine.OpenSubKey('SYSTEM\CurrentControlSet\Control\Session Manager\Environment').GetValue('Path', '', 'DoNotExpandEnvironmentNames')
  foreach ($name in $Names) {
    if ([Environment]::GetEnvironmentVariable($name, 'Machine')) { throw "variable $name is still set" }
    if ($machinePath -match [regex]::Escape("%$name%")) { throw "PATH still refers to %$name%" }
  }
  Write-Host "OK  no trace of: $($Names -join ', ')"
}

# Cleans what the machine already has of a package, the way a user would
# before installing, and checks that a second scan finds nothing left to do.
function Clear-Existing([string[]]$Ids) {
  Invoke-Cli clean --yes @Ids
  $left = @(Get-CliJson scan --json @Ids | Where-Object { $_.checked })
  if ($left.Count -gt 0) {
    $left | ForEach-Object { Write-Host "left: [$($_.pkgId)] $($_.kind) $($_.title) -- $($_.detail)" }
    throw "$($left.Count) item(s) are still there after cleaning"
  }
  Write-Host "OK  nothing left of: $($Ids -join ', ')"
}

# ---------------------------------------------------------------- groups

function Test-Scan {
  # Only looks. Shows what the detection rules make of a machine that has a
  # lot of software installed in the usual ways.
  Invoke-Cli scan
}

function Test-Clean {
  # The whole point of the tool: a machine full of software installed in the
  # usual ways, cleaned in one go. Everything the scan ticks by default must
  # be gone afterwards.
  Invoke-Cli scan
  Invoke-Cli clean --yes
  $left = @(Get-CliJson scan --json | Where-Object { $_.checked })
  if ($left.Count -gt 0) {
    $left | ForEach-Object { Write-Host "left: [$($_.pkgId)] $($_.kind) $($_.title) -- $($_.detail)" }
    throw "$($left.Count) item(s) are still there after cleaning"
  }
  Invoke-Cli scan
}

function Test-Java {
  Invoke-Cli install jdk@21
  Invoke-Cli install jdk@17
  Invoke-Cli install maven
  Invoke-Cli install gradle
  Invoke-Cli install tomcat port=8085

  Update-ProcessEnv
  Assert-Ours java, javac, mvn, gradle
  Assert-Shell 'java -version' 'version "17\.'       # the one installed last becomes the default
  Invoke-Cli use jdk@21
  Update-ProcessEnv
  Assert-Shell 'java -version' 'version "21\.'
  Assert-Shell 'javac -version' 'javac 21\.'
  Assert-Shell 'mvn -v' 'Apache Maven[\s\S]*Java version: 21\.'
  Assert-Shell 'gradle -v' 'Gradle \d'
  Assert-Healthy tomcat
  Assert-Http 'http://127.0.0.1:8085/' 'Apache Tomcat'

  Invoke-Cli service restart ((Get-Install tomcat).services[0])
  Assert-Healthy tomcat

  Remove-Package tomcat
  Remove-Package gradle
  Remove-Package maven
  Remove-Package jdk@21
  Update-ProcessEnv
  Assert-Shell 'java -version' 'version "17\.'       # the remaining version takes over
  Remove-Package jdk@17
  Assert-NoHome JAVA_HOME, MAVEN_HOME, GRADLE_HOME, CATALINA_HOME
}

function Test-Lang {
  # The machine has its own Node.js, PHP and MinGW, and several Pythons that
  # only the registry knows about (those stay, with their registration).
  Clear-Existing node, python, go, php, mingw
  Invoke-Cli install node
  Invoke-Cli install python
  Invoke-Cli install go
  Invoke-Cli install php
  Invoke-Cli install mingw

  Update-ProcessEnv
  Assert-Ours node, npm, pnpm, yarn, python, pip, go, php, composer, gcc, g++
  Assert-Shell 'node -v' '(?m)^v\d+\.'
  Assert-Shell 'npm -v' '(?m)^\d+\.'
  Assert-Shell 'pnpm -v' '(?m)^\d+\.'
  Assert-Shell 'yarn -v' '(?m)^\d+\.'
  Assert-Shell 'node -e "console.log(6*7)"' '(?m)^42'
  Assert-Shell 'python --version' '(?m)^Python 3\.'
  Assert-Shell 'pip --version' '(?m)^pip \d'
  Assert-Shell 'python -c "import ssl, sqlite3, ctypes, tkinter; print(6*7)"' '(?m)^42'
  $venv = Join-Path ([IO.Path]::GetTempPath()) 'dn-venv'
  Assert-Shell "python -m venv ""$venv"" && ""$venv\Scripts\python.exe"" -m pip --version" '(?m)^pip \d'
  Assert-Shell 'go version' '(?m)^go version go1\.'
  Assert-Shell 'php -v' '(?m)^PHP 8\.'
  Assert-Shell 'php -m' 'curl[\s\S]*mbstring[\s\S]*openssl[\s\S]*pdo_mysql'
  Assert-Shell 'composer --version' 'Composer version'
  Assert-Shell 'gcc --version' 'gcc'
  Assert-Shell 'g++ --version' 'g\+\+'

  foreach ($id in 'mingw', 'php', 'go', 'python', 'node') { Remove-Package $id }
  Assert-NoHome PYTHON_HOME, NODE_HOME, GO_HOME, PHP_HOME
}

# $Client is a command expected on PATH, or a path inside the install folder.
function Test-MySQLFamily([string]$Id, [string]$Ref, [int]$Port, [string]$VersionPattern, [string]$Client) {
  Invoke-Cli install $Ref "port=$Port" password=env:DN_PW
  Update-ProcessEnv
  Assert-Healthy $Id
  if ($Client.Contains('\')) {
    $Client = '"' + (Join-Path (Get-Install $Id).dir $Client) + '"'
  } else {
    Assert-Ours $Client
  }
  $base = "$Client --protocol=TCP -h127.0.0.1 -P$Port -uroot -N"

  $env:MYSQL_PWD = $Password
  Assert-Shell "$base -e ""select version()""" $VersionPattern
  Assert-Shell "$base -e ""show variables like 'character_set_server'""" 'utf8mb4'
  Assert-Shell "$base -e ""create database dn_test; create table dn_test.t (s varchar(20)); insert into dn_test.t values ('ok-42'); select s from dn_test.t""" 'ok-42'

  $env:MYSQL_PWD = 'wrong' + $Password
  Assert-ShellFails "$base -e ""select 1""" 'a wrong password was accepted'
  Remove-Item Env:MYSQL_PWD
  Assert-ShellFails "$base --skip-password -e ""select 1""" 'root can log in without a password'

  # The data must survive a restart of the service.
  Invoke-Cli service restart ((Get-Install $Id).services[0])
  Assert-Healthy $Id
  $env:MYSQL_PWD = $Password
  Assert-Shell "$base -e ""select s from dn_test.t""" 'ok-42'
  Remove-Item Env:MYSQL_PWD
}

function Test-MySQL {
  Clear-Existing mysql, mariadb
  Test-MySQLFamily mysql 'mysql@8.0' 3306 '(?m)^8\.0\.' 'mysql'

  # Kept data is picked up again by a reinstall of the same series.
  Remove-Package mysql 'keep'
  Invoke-Cli install 'mysql@8.0' password=env:DN_PW
  $env:MYSQL_PWD = $Password
  Assert-Shell 'mysql --protocol=TCP -h127.0.0.1 -P3306 -uroot -N -e "select s from dn_test.t"' 'ok-42'
  Remove-Item Env:MYSQL_PWD
  Remove-Package mysql 'backup'
  if (-not (Get-ChildItem (Join-Path $Root 'backup') -Directory -ErrorAction SilentlyContinue)) { throw 'the data was not moved to the backup folder' }

  Test-MySQLFamily mysql 'mysql@8.4' 3306 '(?m)^8\.4\.' 'mysql'
  Remove-Package mysql
}

function Test-MariaDB {
  Clear-Existing mysql, mariadb
  Test-MySQLFamily mariadb 'mariadb' 3307 'MariaDB' 'bin\mariadb.exe'
  Remove-Package mariadb
}

function Test-Postgres {
  Clear-Existing postgresql
  Invoke-Cli install postgresql password=env:DN_PW
  Assert-Healthy postgresql
  $psql = '"' + (Join-Path (Get-Install postgresql).dir 'bin\psql.exe') + '" -h 127.0.0.1 -p 5432 -U postgres -w -tA'

  $env:PGPASSWORD = $Password
  Assert-Shell "$psql -c ""select version()""" '(?m)^PostgreSQL \d+'
  Assert-Shell "$psql -c ""show server_encoding""" 'UTF8'
  Assert-Shell "$psql -c ""create table t (s text); insert into t values ('ok-42'); select s from t""" 'ok-42'
  $env:PGPASSWORD = 'wrong' + $Password
  Assert-ShellFails "$psql -c ""select 1""" 'a wrong password was accepted'

  Invoke-Cli service restart ((Get-Install postgresql).services[0])
  Assert-Healthy postgresql
  $env:PGPASSWORD = $Password
  Assert-Shell "$psql -c ""select s from t""" 'ok-42'
  Remove-Item Env:PGPASSWORD
  Remove-Package postgresql
}

function Test-NoSQL {
  Clear-Existing mongodb, redis
  Invoke-Cli install redis password=env:DN_PW
  Invoke-Cli install mongodb

  Update-ProcessEnv
  Assert-Healthy redis
  Assert-Ours redis-cli
  $anonymous = Invoke-Shell 'redis-cli -p 6379 ping'    # no password, no entry
  if ($anonymous.Out -notmatch 'NOAUTH') { throw 'Redis answered a client that gave no password' }
  $env:REDISCLI_AUTH = $Password
  Assert-Shell 'redis-cli -p 6379 ping' 'PONG'
  Assert-Shell 'redis-cli -p 6379 set dn ok-42' 'OK'
  Invoke-Cli service restart ((Get-Install redis).services[0])
  Assert-Healthy redis
  Assert-Shell 'redis-cli -p 6379 get dn' 'ok-42'      # persistence is on
  Remove-Item Env:REDISCLI_AUTH

  Assert-Healthy mongodb

  Remove-Package mongodb
  Remove-Package redis
}

function Test-Web {
  Clear-Existing nginx, rabbitmq, erlang     # the machine has an Nginx service of its own
  Invoke-Cli install nginx port=8088
  Invoke-Cli install rabbitmq

  Assert-Healthy nginx
  Assert-Http 'http://127.0.0.1:8088/' 'nginx'
  Invoke-Cli service restart ((Get-Install nginx).services[0])
  Assert-Healthy nginx
  Assert-Http 'http://127.0.0.1:8088/' 'nginx'

  Assert-Healthy rabbitmq
  Assert-Http 'http://127.0.0.1:15672/' 'RabbitMQ'

  Remove-Package rabbitmq
  Remove-Package erlang
  Remove-Package nginx
}

function Test-Kafka {
  Invoke-Cli install kafka
  Invoke-Cli install nacos console=8089

  Update-ProcessEnv
  Assert-Healthy kafka
  $topics = '"' + (Join-Path (Get-Install kafka).dir 'bin\windows\kafka-topics.bat') + '" --bootstrap-server 127.0.0.1:9092'
  Assert-Shell "$topics --create --topic dn-test --partitions 1 --replication-factor 1" 'Created topic'
  Assert-Shell "$topics --list" 'dn-test'
  Invoke-Cli service restart ((Get-Install kafka).services[0])
  Assert-Healthy kafka
  Assert-Shell "$topics --list" 'dn-test'

  Assert-Healthy nacos

  Remove-Package nacos
  Remove-Package kafka
  Remove-Package jdk
}

function Test-Search {
  Invoke-Cli install elasticsearch
  Invoke-Cli install neo4j password=env:DN_PW

  Assert-Healthy elasticsearch
  Assert-Http 'http://127.0.0.1:9200/' 'cluster_name'

  Assert-Healthy neo4j
  Assert-Http 'http://127.0.0.1:7474/' 'neo4j_version'
  $pair = [Convert]::ToBase64String([Text.Encoding]::ASCII.GetBytes("neo4j:$Password"))
  $body = '{"statements":[{"statement":"RETURN 6*7 AS n"}]}'
  $resp = Invoke-RestMethod -Uri 'http://127.0.0.1:7474/db/neo4j/tx/commit' -Method Post -Body $body -ContentType 'application/json' -Headers @{ Authorization = "Basic $pair" }
  if ($resp.results[0].data[0].row[0] -ne 42) { throw 'Neo4j did not run the query with the chosen password' }
  Write-Host 'OK  Neo4j accepts the password and answers a query'
  $bad = [Convert]::ToBase64String([Text.Encoding]::ASCII.GetBytes('neo4j:wrong' + $Password))
  try {
    Invoke-RestMethod -Uri 'http://127.0.0.1:7474/db/neo4j/tx/commit' -Method Post -Body $body -ContentType 'application/json' -Headers @{ Authorization = "Basic $bad" } | Out-Null
    $accepted = $true
  } catch {
    $accepted = $false
  }
  if ($accepted) { throw 'Neo4j accepted a wrong password' }

  Remove-Package neo4j
  Remove-Package elasticsearch
  Remove-Package jdk
}

function Test-Tools {
  Invoke-Cli install vscode
  Update-ProcessEnv
  Assert-Ours code
  Assert-Shell 'code --version' '(?m)^\d+\.\d+\.\d+'
  Remove-Package vscode
}

# The database GUI clients: the program is there, its Start menu shortcut
# points at it, and uninstalling takes both away.
function Test-DbTools {
  $exes = [ordered]@{
    'dbeaver'         = 'dbeaver.exe'
    'heidisql'        = 'heidisql.exe'
    'sqlitebrowser'   = 'DB Browser for SQLite.exe'
    'tinyrdm'         = 'Tiny RDM.exe'
    'mongodb-compass' = 'MongoDBCompass.exe'
    'elasticvue'      = 'elasticvue.exe'
  }
  $wsh = New-Object -ComObject WScript.Shell
  foreach ($id in $exes.Keys) {
    Invoke-Cli install $id
    $info = Get-Install $id
    $exe = Join-Path $info.dir $exes[$id]
    if (-not (Test-Path -LiteralPath $exe)) { throw "${id}: $($exes[$id]) is missing" }
    $links = @($info.shortcuts)
    if ($links.Count -eq 0) { throw "${id}: no Start menu shortcut" }
    foreach ($lnk in $links) {
      if (-not (Test-Path -LiteralPath $lnk)) { throw "${id}: shortcut $lnk is missing" }
      $target = $wsh.CreateShortcut($lnk).TargetPath
      if ($target -ne $exe) { throw "${id}: shortcut $lnk points at '$target', not $exe" }
    }
    Write-Host "OK  $id is in place: $exe"
  }
  foreach ($id in $exes.Keys) {
    $links = @((Get-Install $id).shortcuts)
    Remove-Package $id
    foreach ($lnk in $links) {
      if (Test-Path -LiteralPath $lnk) { throw "${id}: shortcut $lnk is still there after uninstall" }
    }
  }
}

function Test-JetBrains {
  Invoke-Cli install idea
  $dir = (Get-Install idea).dir
  if (-not (Test-Path (Join-Path $dir 'bin\idea64.exe'))) { throw 'idea64.exe is missing' }
  Remove-Package idea
}

function Test-Mirrors {
  Invoke-Cli mirrors all
  $npmrc = Join-Path $env:USERPROFILE '.npmrc'
  if ((Get-Content $npmrc -Raw) -notmatch 'registry\s*=\s*https://registry\.npmmirror\.com') { throw '.npmrc was not written' }
  $settings = Join-Path $env:USERPROFILE '.m2\settings.xml'
  [xml]$xml = Get-Content $settings -Raw
  if (-not ($xml.settings.mirrors.mirror | Where-Object { $_.url -match 'aliyun' })) { throw 'settings.xml has no aliyun mirror' }
  Assert-Shell 'npm config get registry' 'npmmirror'
  Assert-Shell 'pip config list' 'tsinghua'
  Assert-Shell 'go env GOPROXY' 'goproxy\.cn'
}

# ---------------------------------------------------------------- main

Invoke-Cli version
Invoke-Cli root $Root

$tests = @{
  scan      = { Test-Scan }
  clean     = { Test-Clean }
  java      = { Test-Java }
  lang      = { Test-Lang }
  mysql     = { Test-MySQL }
  mariadb   = { Test-MariaDB }
  postgres  = { Test-Postgres }
  nosql     = { Test-NoSQL }
  web       = { Test-Web }
  kafka     = { Test-Kafka }
  search    = { Test-Search }
  tools     = { Test-Tools }
  dbtools   = { Test-DbTools }
  jetbrains = { Test-JetBrains }
  mirrors   = { Test-Mirrors }
}
if (-not $tests.ContainsKey($Group)) { throw "unknown group '$Group'; known: $(($tests.Keys | Sort-Object) -join ', ')" }
& $tests[$Group]
Write-Host "`nALL CHECKS PASSED for group '$Group'" -ForegroundColor Green
