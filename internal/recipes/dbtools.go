package recipes

import (
	"path/filepath"
	"strings"

	"devnest/internal/core"
)

// ---- Database GUI clients ----

// installPortable is the whole recipe for a desktop program that runs where
// it is unpacked: the download is a zip, or the program itself (an .exe).
func installPortable(name, exe string) func(c *core.Ctx) error {
	return func(c *core.Ctx) error {
		if strings.EqualFold(filepath.Ext(c.Rel.File), ".exe") {
			file, err := c.Fetch()
			if err != nil {
				return err
			}
			c.Step("复制")
			if err := c.CopyFile(file, c.J(exe)); err != nil {
				return err
			}
		} else if err := unzipInstall(c); err != nil {
			return err
		}
		c.Shortcut(name, c.J(exe), "")
		c.Info("启动", "开始菜单 → DevNest → "+name)
		return nil
	}
}

func init() {
	dbeaver := "dbeaver-ce-26.2.5-windows-x86_64.zip"
	core.Register(&core.Package{
		ID: "dbeaver", Name: "DBeaver", Category: "dbtool",
		Desc:     "免费的数据库图形客户端，能连 MySQL、MariaDB、PostgreSQL、SQLite 等关系型数据库；自带 Java 运行环境",
		Homepage: "https://dbeaver.io/",
		Releases: []core.Release{{Version: "26.2.5", File: dbeaver, Size: 126280946, URLs: []string{
			"https://dbeaver.io/files/26.2.5/" + dbeaver,
			gh("dbeaver/dbeaver", "26.2.5", dbeaver),
		}}},
		Notes: []string{"免费的社区版连不了 MongoDB 和 Redis，这两个可以用同一分类里的 MongoDB Compass 和 Tiny RDM。"},
		Probe: []string{"dbeaver.exe"},
		Steps: 1,
		Install: func(c *core.Ctx) error {
			if err := installPortable("DBeaver", "dbeaver.exe")(c); err != nil {
				return err
			}
			c.Info("第一次连数据库", "会提示下载对应的驱动，需要联网")
			return nil
		},
		Detect: core.Detect{
			Uninstall: []string{`^DBeaver`},
			Dirs:      []string{`%ProgramFiles%\DBeaver`, `%LOCALAPPDATA%\DBeaver`},
			UserDirs:  []string{`%APPDATA%\DBeaverData`},
		},
	})

	heidi := "HeidiSQL_12.22_64_Portable.zip"
	core.Register(&core.Package{
		ID: "heidisql", Name: "HeidiSQL", Category: "dbtool",
		Desc:     "小巧的数据库图形客户端，能连 MySQL、MariaDB、PostgreSQL、SQL Server、SQLite，启动快",
		Homepage: "https://www.heidisql.com/",
		Releases: []core.Release{{Version: "12.22", File: heidi, Size: 29903115,
			SHA256: "7b36821fa3e16cc37b511d41747efa4c8b980d1e3fa937e52c5931bcc4ee79b4",
			URLs:   []string{gh("HeidiSQL/HeidiSQL", "v12.22", heidi)}}},
		Probe:   []string{"heidisql.exe"},
		Steps:   1,
		Install: installPortable("HeidiSQL", "heidisql.exe"),
		Detect: core.Detect{
			Uninstall: []string{`^HeidiSQL`},
			Dirs:      []string{`%ProgramFiles%\HeidiSQL`},
			UserDirs:  []string{`%APPDATA%\HeidiSQL`},
		},
	})

	sqlite := "DB.Browser.for.SQLite-v3.13.1-win64.zip"
	core.Register(&core.Package{
		ID: "sqlitebrowser", Name: "DB Browser for SQLite", Category: "dbtool",
		Desc:     "打开、编辑 SQLite 数据库文件的图形工具",
		Homepage: "https://sqlitebrowser.org/",
		Releases: []core.Release{{Version: "3.13.1", File: sqlite, Size: 21720951,
			URLs: []string{gh("sqlitebrowser/sqlitebrowser", "v3.13.1", sqlite)}}},
		Notes:   []string{"发布方没有提供校验值，只能核对文件大小。"},
		Probe:   []string{"DB Browser for SQLite.exe"},
		Steps:   1,
		Install: installPortable("DB Browser for SQLite", "DB Browser for SQLite.exe"),
		Detect: core.Detect{
			Uninstall: []string{`^DB Browser for SQLite`},
			Dirs:      []string{`%ProgramFiles%\DB Browser for SQLite`},
			UserDirs:  []string{`%APPDATA%\sqlitebrowser`},
		},
	})

	rdm := "TinyRDM_Portable_1.2.7_windows_x64.zip"
	core.Register(&core.Package{
		ID: "tinyrdm", Name: "Tiny RDM", Category: "dbtool",
		Desc:     "Redis 图形客户端，界面简洁，支持中文",
		Homepage: "https://redis.tinycraft.cc/",
		Releases: []core.Release{{Version: "1.2.7", File: rdm, Size: 11855971,
			SHA256: "e4ec11abe5ffccb3277d60eba3980b9b23d254b5906c4980ee39562a8ea9beb3",
			URLs:   []string{gh("tiny-craft/tiny-rdm", "v1.2.7", rdm)}}},
		Probe:   []string{"Tiny RDM.exe"},
		Steps:   1,
		Install: installPortable("Tiny RDM", "Tiny RDM.exe"),
		Detect: core.Detect{
			Uninstall: []string{`^Tiny RDM`},
			UserDirs:  []string{`%APPDATA%\TinyRDM`},
		},
	})

	compass := "mongodb-compass-1.53.2-win32-x64.zip"
	core.Register(&core.Package{
		ID: "mongodb-compass", Name: "MongoDB Compass", Category: "dbtool",
		Desc:     "MongoDB 官方的图形客户端，可以浏览和编辑文档、分析查询",
		Homepage: "https://www.mongodb.com/products/tools/compass",
		Releases: []core.Release{{Version: "1.53.2", File: compass, Size: 226903302,
			SHA256: "b9706d0cf948227cf64be4484a6ebc402b6e260c610882eb68bb8e91e9e659b6",
			URLs: []string{
				"https://downloads.mongodb.com/compass/" + compass,
				gh("mongodb-js/compass", "v1.53.2", compass),
			}}},
		Notes:   []string{"免费使用；源码按 SSPL 协议公开，这不是通常意义上的开源协议。安装包约 220 MB。"},
		Probe:   []string{"MongoDBCompass.exe"},
		Steps:   1,
		Install: installPortable("MongoDB Compass", "MongoDBCompass.exe"),
		Detect: core.Detect{
			Uninstall: []string{`^MongoDB Compass`},
			Dirs:      []string{`%LOCALAPPDATA%\MongoDBCompass`},
			UserDirs:  []string{`%APPDATA%\MongoDB Compass`},
		},
	})

	core.Register(&core.Package{
		ID: "elasticvue", Name: "Elasticvue", Category: "dbtool",
		Desc:     "Elasticsearch 图形客户端，可以查看索引、搜索数据、发 REST 请求",
		Homepage: "https://elasticvue.com/",
		Releases: []core.Release{{Version: "1.17.0", File: "elasticvue-1.17.0.exe", Size: 10656256,
			SHA256: "844601944a5e03b6cd44f19d72ff684b233797efe1d996bb0c74cee900f77eb9",
			URLs:   []string{gh("cars10/elasticvue", "v1.17.0", "elasticvue.exe")}}},
		Probe:   []string{"elasticvue.exe"},
		Steps:   1,
		Install: installPortable("Elasticvue", "elasticvue.exe"),
		Detect: core.Detect{
			Uninstall: []string{`(?i)^elasticvue`},
			UserDirs:  []string{`%LOCALAPPDATA%\com.elasticvue.app`, `%APPDATA%\com.elasticvue.app`},
		},
	})
}
