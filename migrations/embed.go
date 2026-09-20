package migrations

import "embed"

// FS 内嵌全部 SQL 迁移，随二进制一起发布；容器内 sqlite CLI 仍可直接使用根目录 migrations。
//
//go:embed *.sql
var FS embed.FS
