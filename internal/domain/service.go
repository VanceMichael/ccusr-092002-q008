// Package domain 实现院内制剂从原料来货到成品放行的质量业务规则。
package domain

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

// 角色常量：四类职责互不替代。
const (
	RoleWarehouse       = "warehouse"        // 药库：凭证登记、分装、合批
	RoleInspector       = "inspector"        // 检验人：抽样、首检、复检、成品检验
	RoleCompoundingLead = "compounding_lead" // 配制负责人：建批、称量投料
	RoleQARelease       = "qa_release"       // 药事负责人：成品放行、追溯
)

// weightEps 重量守恒核对的容差（克）。药材以克记账，1 微克容差足以吸收浮点误差。
const weightEps = 1e-6

// Error 携带 HTTP 状态码与业务错误码，便于接口层直接映射。
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

func fail(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// AsError 从任意错误中提取领域 *Error；找不到时返回 nil。
func AsError(err error) *Error {
	var target *Error
	if errors.As(err, &target) {
		return target
	}
	return nil
}

func nowISO() string { return time.Now().Format(time.RFC3339) }

func newID() string { return uuid.NewString() }

// Service 持有数据库连接。所有写操作都在单个事务内完成。
type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service { return &Service{db: db} }

func (s *Service) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Actor 是经 X-User-ID 解析出的签字/操作人员。
type Actor struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	Active bool   `json:"active"`
}

// authenticate 解析操作人员身份并要求其属于允许角色之一。
func (s *Service) authenticate(ctx context.Context, tx *sql.Tx, userID string, allowed ...string) (*Actor, error) {
	if userID == "" {
		return nil, fail(401, "unauthenticated", "缺少 X-User-ID 请求头")
	}
	actor := &Actor{}
	err := tx.QueryRowContext(ctx,
		`SELECT id, name, role, active FROM users WHERE id = ?`, userID).
		Scan(&actor.ID, &actor.Name, &actor.Role, &actor.Active)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fail(401, "unknown_user", "用户不存在")
	}
	if err != nil {
		return nil, err
	}
	if !actor.Active {
		return nil, fail(403, "user_inactive", "该用户已停用")
	}
	for _, role := range allowed {
		if actor.Role == role {
			return actor, nil
		}
	}
	return nil, fail(403, "role_forbidden", "该操作不允许角色 "+actor.Role+" 执行")
}

// countUsers 返回用户总数，用于首个用户的自举。
func (s *Service) countUsers(ctx context.Context, tx *sql.Tx) (int, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
