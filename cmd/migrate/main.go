// 命令 migrate 把内嵌 SQL 迁移应用到 DATABASE_PATH 指定的 SQLite 文件后退出。
package main

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/migrate"
)

func main() {
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "data/app.sqlite3"
	}
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("创建数据目录失败: %v", err)
		}
	}
	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()
	if err := migrate.Up(db); err != nil {
		log.Fatalf("迁移失败: %v", err)
	}
	log.Printf("迁移完成：%s", dbPath)
}
