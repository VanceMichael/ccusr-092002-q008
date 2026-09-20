// Package store 是质量放行数据的 SQLite 持久化层，只做读写，不做业务裁决。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrNotFound = errors.New("记录不存在")

// ErrConflict 表示唯一约束等数据冲突。
var ErrConflict = errors.New("数据冲突")

type Staff struct {
	ID        string
	Name      string
	Role      string // warehouse / inspector / production_manager / pharmacy_head
	Active    bool
	CreatedAt time.Time
}

type Material struct {
	ID   string
	Code string
	Name string
}

type Supplier struct {
	ID           string
	Name         string
	Origin       string
	LicenseScope string
	CreatedAt    time.Time
	CreatedBy    string
}

type Certificate struct {
	ID           string
	SupplierID   string
	CertType     string
	CertRef      string
	LicenseScope string
	IssuedAt     *time.Time
	ExpiresAt    *time.Time
	RecordedAt   time.Time
	RecordedBy   string
}

type InboundLot struct {
	ID          string
	LotRef      string
	SupplierID  string
	MaterialID  string
	NetWeightMg int64
	ReceivedAt  time.Time
	ReceiverID  string
	CreatedAt   time.Time
}

type MaterialLot struct {
	ID               string
	LotRef           string
	MaterialID       string
	RootInboundLotID string
	InitialWeightMg  int64
	State            string // quarantined / held / available / rejected / disposed
	HadFailure       bool
	CreatedAt        time.Time
	CreatedBy        string
}

type CompoundBatch struct {
	ID                 string
	BatchRef           string
	FormulaRef         string
	FormulaRevision    int
	ProductName        string
	ProductionManager  string
	Status             string // planned / charged / qc_passed / released / rejected / recalled
	QualityBlocked     bool
	QualityBlockReason string
	CreatedAt          time.Time
}

type Sample struct {
	ID             string
	SampleRef      string
	SubjectType    string // material_lot / compound_batch
	SubjectID      string
	Kind           string // initial / retest
	RetestOfSample string
	QtyMg          int64
	SampledAt      time.Time
	SamplerID      string
}

type Reading struct {
	ID            string
	InspectionID  string
	Seq           int
	TestItem      string
	SpecMin       string
	SpecMax       string
	Unit          string
	ObservedValue string
	Outcome       string // pass / fail
	MeasuredAt    time.Time
	InspectorID   string
	Remark        string
}

type Inspection struct {
	ID            string
	InspectionRef string
	SampleID      string
	OpenedAt      time.Time
	OpenedBy      string
	ConcludedAt   *time.Time
	Verdict       string // "" / qualified / unqualified
	ConcludedBy   string
	Remark        string
	Readings      []Reading
}

type Weighing struct {
	ID              string
	CompoundBatchID string
	MaterialLotID   string
	MovementItemID  string
	WeightMg        int64
	WeighedAt       time.Time
}

type Charging struct {
	ID              string
	CompoundBatchID string
	MaterialLotID   string
	WeighingID      string
	WeightMg        int64
	ChargedAt       time.Time
	ChargedBy       string
}

type ReleaseDecision struct {
	ID                   string
	CompoundBatchID      string
	Decision             string // released / held / rejected
	FinishedInspectionID string
	Reason               string
	DecidedAt            time.Time
	DecidedBy            string
}

type Disposal struct {
	ID              string
	MaterialLotID   string
	DispositionType string // destroy / return_to_supplier / deactivated
	DisposedAt      time.Time
	DisposedBy      string
	Remark          string
}

// Tx 暴露给领域层的事务接口。
type Tx interface {
	Commit() error
	Rollback() error
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type Store struct {
	DB *sql.DB
}

func New(db *sql.DB) *Store { return &Store{DB: db} }

func (s *Store) BeginTx(ctx context.Context) (Tx, error) {
	return s.DB.BeginTx(ctx, nil)
}

// nowISO 返回带时区偏移的 ISO 8601 字符串（契约要求）。
func nowISO(ts time.Time) string { return ts.Format(time.RFC3339) }

func parseTime(value string) time.Time {
	ts, _ := time.Parse(time.RFC3339, value)
	return ts
}

func mapExecError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if msg := err.Error(); containsAny(msg, "UNIQUE constraint", "duplicate key") {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
