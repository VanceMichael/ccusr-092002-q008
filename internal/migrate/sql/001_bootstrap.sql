
CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- 迁移登记由 internal/migrate 在应用脚本后统一写入（版本号为迁移文件名）。
