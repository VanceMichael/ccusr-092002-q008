// Package quality 是原料到成品质量放行的领域服务：
// 记录入厂凭证、抽样复检、分装合批、称量投料与放行，并在一个事务内完成全部质量裁决。
package quality

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/store"
)

// 角色常量。
const (
	RoleWarehouse         = "warehouse"          // 仓储/分装/称量
	RoleInspector         = "inspector"          // 检验人
	RoleProductionManager = "production_manager" // 配制负责人
	RolePharmacyHead      = "pharmacy_head"      // 药事负责人
)

// 错误码，HTTP 层据此映射状态码。
const (
	CodeValidation     = "validation_error"
	CodeNotFound       = "not_found"
	CodeConflict       = "conflict"
	CodeRoleForbidden  = "role_forbidden"
	CodeStateConflict  = "state_conflict"
	CodeWeightMismatch = "weight_mismatch"
	CodeQualityBlocked = "quality_blocked"
	CodeSignature      = "signature_invalid"
)

// RuleError 表示违反质量规则的业务错误。
type RuleError struct {
	Code string
	Msg  string
}

func (e *RuleError) Error() string { return e.Msg }

func ruleError(code, format string, args ...any) error {
	return &RuleError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// AsRuleError 从错误中提取领域规则错误。
func AsRuleError(err error) (*RuleError, bool) {
	var ruleErr *RuleError
	if errors.As(err, &ruleErr) {
		return ruleErr, true
	}
	return nil, false
}

type Service struct {
	store *store.Store
	now   func() time.Time
}

func NewService(st *store.Store) *Service {
	return &Service{store: st, now: time.Now}
}

// SetClock 仅供测试注入固定时钟。
func (s *Service) SetClock(clock func() time.Time) {
	if clock != nil {
		s.now = clock
	}
}

func (s *Service) timeNow() time.Time { return s.now() }

func newID(prefix string) string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(raw)
}
