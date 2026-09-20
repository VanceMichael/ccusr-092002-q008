// Package store 负责打开 SQLite 数据库并按顺序执行内嵌迁移。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/vancemichael/092002-hospital-formula-lineage/migrations"
)

// Open 打开 SQLite 数据文件并打开外键约束。纯 Go 驱动，兼容 CGO_ENABLED=0。
func Open(path string) (*sql.DB, error) {
	dsn := path
	if !strings.Contains(dsn, "?") {
		query := url.Values{}
		query.Set("_pragma", "foreign_keys(1)")
		query.Add("_pragma", "busy_timeout(5000)")
		query.Add("_pragma", "journal_mode(WAL)")
		dsn = path + "?" + query.Encode()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// 单连接避免文件锁竞争，也保证 PRAGMA 设置与会话一致。
	db.SetMaxOpenConns(1)
	return db, nil
}

// Migrate 按文件名顺序应用尚未执行的迁移，每个迁移一个事务。
func Migrate(ctx context.Context, db *sql.DB) error {
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	applied := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		// 引导表尚不存在时，第一个迁移会负责创建。
		applied = map[string]bool{}
	} else {
		for rows.Next() {
			var version string
			if err := rows.Scan(&version); err != nil {
				rows.Close()
				return err
			}
			applied[version] = true
		}
		rows.Close()
	}

	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		if applied[version] {
			continue
		}
		script, err := migrations.FS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(script)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("执行迁移 %s 失败: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
