# DevNest 开发环境管家

Windows 上的开发环境一键安装与清理工具：选好软件点一下，下载、解压、写配置、注册服务、设环境变量全部做完；机器上已有的旧安装可以先扫描、确认后一键清掉。

一个 exe，不用安装，双击运行（需要管理员权限）。

> **当前状态：0.1.0，还没有发布过。**
> 安装流程目前只在模拟环境里跑通，真实 Windows 上的安装由仓库里的真机安装测试（`integration` 工作流）验证，该工作流全部通过之前，请不要在重要的机器上使用。

## 能做什么

- **一键安装**：31 个常用开发软件，装完即可用（服务已启动、密码已设置、命令已加入 PATH）。
- **套装**：Java 后端、前端、Python、Go、数据库、微服务中间件、图数据库，一次装一组。
- **一键清理**：扫描机器上已有的安装（注册表卸载项、Windows 服务、环境变量、PATH、残留目录，以及被改到别处的 npm 全局目录设置），列出来由你勾选，确认后才动手。数据库的数据目录默认不勾选，删除前先备份。
- **多版本切换**：JDK、Python、Node.js、Go、PHP 可以同时装多个版本，点一下切换默认版本。
- **服务面板**：查看每个服务的状态和端口占用，启动、停止、重启，看日志。
- **国内镜像**：npm / pnpm / yarn、pip、Maven、Gradle、Go 模块代理、Rust（cargo / rustup）、Docker 镜像加速，一键全部切到国内源，也可以单独改回官方源。
- **卸载干净**：卸载时停服务、删服务、删目录、还原环境变量和 PATH；数据可选保留、备份后删除或直接删除。

## 支持的软件

| 分类 | 软件 |
|---|---|
| 语言与运行环境 | JDK（8 / 11 / 17 / 21 / 25）、Python、Node.js（可带 pnpm、yarn）、Go、PHP（可带 Composer）、.NET SDK、Rust、MinGW-w64（GCC） |
| 构建工具 | Maven、Gradle、Git |
| 数据库 | MySQL（8.0 / 8.4）、MariaDB、PostgreSQL、MongoDB、Redis、Neo4j |
| 中间件与服务器 | Nginx、Tomcat、RabbitMQ（自动带 Erlang）、Kafka、Elasticsearch、Nacos |
| 开发工具 | IntelliJ IDEA、PyCharm、VS Code、DBeaver、Apifox、Docker Desktop |
| 运行库 | VC++ 运行库（需要它的软件会自动带上） |

依赖会自动处理：装 Maven、Tomcat、Kafka、Neo4j 等会先装好合适版本的 JDK，装 RabbitMQ 会先装 Erlang。

每个软件具体有哪些版本，以程序里「软件库」页显示的为准。

## 下载与使用

1. 在仓库的 Releases 页下载 `DevNest.exe`（没有正式版时，到 Actions 页最近一次 `build` 运行的页面底部下载 `DevNest-windows-amd64`）。
2. 双击运行，在系统弹出的权限提示里点「是」。
3. 第一次打开先到「设置」页确认安装目录。默认选剩余空间最大的非系统盘（比如 `D:\DevNest`），只有一个盘时是 `C:\DevNest`；路径里不能有空格和中文，装了软件之后不能再改。
4. 机器上有旧的安装时，先到「一键清理」页扫描并清理，再到「软件库」或首页的套装安装。

系统要求：Windows 10 / 11，64 位。程序自带 WebView2 安装器，系统里没有时会自动装上。

### 文件放在哪里

```
D:\DevNest\                安装目录（可在设置里改）
  apps\<软件>\<版本>\      程序文件
  data\<软件>\             数据（数据库文件、配置等），卸载时可选择保留
  backup\                  卸载或清理前做的备份
  cache\                   下载的安装包，可在设置里清空
C:\ProgramData\DevNest\
  state.json               已装软件的记录
  settings.json            设置
  logs\                    每个任务的日志
  env-backup\              改动环境变量之前的备份
```

### 安全方面的默认做法

- 数据库和中间件默认只监听 `127.0.0.1`，局域网里的其他机器连不上。
- 勾选「允许局域网访问」时，必须把密码改成非默认值且不少于 8 位，否则不让装。
- 密码不会写进已装记录，日志里会打码。
- 下载的安装包会校验：发布方提供校验值的按校验值核对，其余按文件大小核对，安装程序类的还会检查数字签名。
- 一键清理只动你勾选的项；没有静默卸载方式的软件只提示、不代为卸载。

## 已知局限

- 真实 Windows 上的安装尚未全部验证（见文首的状态说明）。
- Git、Docker Desktop、.NET SDK、Rust、Apifox、PyCharm 不在自动化的真机测试范围内。
- Redis、RabbitMQ、Erlang、MinGW-w64 只有 GitHub 一个下载来源，国内网络慢或连不上时，到「设置」里填一个 GitHub 加速前缀。
- Docker Desktop 需要系统开启 WSL2 或 Hyper-V，安装后可能要重启。
- 只支持 64 位 x86 的 Windows，不支持 ARM 版 Windows。

## 命令行版

`devnestcli.exe` 和桌面程序用的是同一套安装逻辑，适合写脚本批量装机，自动化测试用的也是它。需要在管理员终端里运行。

```
devnestcli list                               列出可装的软件和版本
devnestcli install jdk@21                     版本可以只写前缀
devnestcli install mysql@8.0 port=3306 password=env:DB_PW
devnestcli use jdk@17                         切换默认版本
devnestcli uninstall mysql --data backup      数据：keep 保留 / backup 备份后删除 / delete 删除
devnestcli scan                               扫描机器上已有的安装
devnestcli clean --yes mysql                  清理扫描到的项（不带 --yes 只列出）
devnestcli services                           服务状态
devnestcli service restart <服务名>
devnestcli mirrors all                        全部切到国内镜像
devnestcli root D:\DevNest                    查看或设置安装目录
```

密码不要直接写在命令行上，写成 `password=env:变量名`，程序会从环境变量里读。

## 在 GitHub 上打包

仓库里有三个工作流（`.github/workflows/`）：

| 工作流 | 什么时候跑 | 做什么 |
|---|---|---|
| `build`（打包） | 推送到 main、提 PR、打 `v*` 标签、手动 | 跑测试，在 Windows 机器上编译出 `DevNest.exe` 和 `devnestcli.exe`，可在该次运行页面底部下载；打标签时发布到 Releases |
| `integration`（真机安装测试） | 推送到 main 且改了安装逻辑、手动 | 在 Windows 机器上分组真装真卸，检查命令、服务、端口、密码和卸载后的残留 |
| `catalog`（下载地址体检） | 每周一、手动 | 检查所有下载地址还能不能用 |

### 发一个版本

1. 改 `internal/core/manager.go` 里的 `AppVersion` 和 `wails.json` 里的 `productVersion`。
2. 提交后打标签并推送：
   ```
   git tag v0.1.0
   git push origin v0.1.0
   ```
3. `build` 工作流会校验标签和 `AppVersion` 是否一致，一致才发布。

### 只跑某几组真机测试

在 Actions 页手动运行 `integration`，`groups` 里填要跑的组，比如 `["mysql","java"]`。全部的组：
`scan` `clean` `mirrors` `java` `lang` `mysql` `mariadb` `postgres` `nosql` `web` `kafka` `search` `tools` `jetbrains`。

每组跑完会把任务日志打包上传，失败时下载 `logs-<组名>` 查看。

## 开发

需要 Go 1.26、Node.js 22、[Wails v2](https://wails.io)：

```
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.1
wails dev          # 开发模式，改代码自动刷新
wails build -platform windows/amd64 -webview2 embed
go test ./internal/...
```

### 演示模式

在 macOS 或 Linux 上运行时，程序自动进入演示模式：注册表、服务、环境变量都是模拟的，安装包不会真的下载，用来开发界面和跑测试。模拟数据放在 `DEVNEST_SIM_DIR` 指定的目录（默认是临时目录下的 `devnest-sim`）。在 Windows 上设置环境变量 `DEVNEST_SIM=1` 也能进入演示模式。

演示模式只能证明流程和界面是通的，不能证明软件在 Windows 上真的装得上。

### 目录

```
main.go, app.go          桌面程序入口，把引擎的方法暴露给界面
frontend/                界面（Vue 3 + Vite + TypeScript）
internal/core/           引擎：任务队列、下载、安装与回滚、扫描清理、服务、镜像
internal/recipes/        每个软件的安装配方（版本、下载地址、怎么装、怎么卸）
internal/platform/       对 Windows 的操作（注册表、服务、环境变量）和模拟实现
internal/svchost/        把普通程序包成 Windows 服务的宿主
cmd/devnestcli/          命令行版
cmd/catalogcheck/        下载地址体检
scripts/ci/              真机安装测试脚本
```

### 更新某个软件的版本

版本号和下载地址都在 `internal/recipes/` 里。改完后运行：

```
go run ./cmd/catalogcheck       # 检查新地址能不能下载
go test ./internal/...
```

下载地址的顺序是国内镜像在前、官方地址在后。在海外机器上设置环境变量 `DEVNEST_SOURCES=official` 可以反过来，先试官方地址。
