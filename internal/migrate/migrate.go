// Package migrate 在服务启动时把内嵌的 SQL 迁移按文件名顺序应用到 SQLite。
package migrate

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed sql/*.sql
var migrationFS embed.FS

// Up 应用全部未执行的迁移。每个迁移文件在一个事务内执行并登记到 schema_migrations。
func Up(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
	    version TEXT PRIMARY KEY,
	    applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("初始化迁移登记表: %w", err)
	}

	entries, err := migrationFS.ReadDir("sql")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var exists int
		if err := db.QueryRow(
			`SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, name,
		).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		script, err := migrationFS.ReadFile("sql/" + name)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(script)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("迁移 %s 执行失败: %w", name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations(version) VALUES (?)`, strings.TrimSuffix(name, ".sql"),
		); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
