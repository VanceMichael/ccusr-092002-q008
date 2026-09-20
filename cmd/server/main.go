package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/httpapi"
	"github.com/vancemichael/092002-hospital-formula-lineage/internal/migrate"
	"github.com/vancemichael/092002-hospital-formula-lineage/internal/quality"
	"github.com/vancemichael/092002-hospital-formula-lineage/internal/store"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	dbPath := os.Getenv("DATABASE_PATH")
	if dbPath == "" {
		dbPath = "data/app.sqlite3"
	}
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("创建数据目录失败: %v", err)
		}
	}

	// 外键必须逐连接开启；busy_timeout 避免并发写立即报锁。
	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer db.Close()

	if err := migrate.Up(db); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	api := httpapi.NewAPI(quality.NewService(store.New(db)))
	log.Printf("质量放行服务启动，监听 :%s，数据库 %s", port, dbPath)
	log.Fatal(http.ListenAndServe(":"+port, api.Router()))
}
