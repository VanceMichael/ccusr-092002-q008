package quality

import (
	"context"
	"errors"
	"time"

	"github.com/vancemichael/092002-hospital-formula-lineage/internal/store"
)

// withTx 在一个事务内执行领域动作，业务错误回滚，成功提交。
func (s *Service) withTx(ctx context.Context, fn func(q store.Tx) error) error {
	tx, err := s.store.BeginTx(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// requireStaff 读取操作人并校验角色；返回员工记录。
func requireStaff(ctx context.Context, q store.Tx, staffID string, roles ...string) (store.Staff, error) {
	staff, err := store.StaffIn(ctx, q, staffID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Staff{}, ruleError(CodeNotFound, "操作人不存在：%s", staffID)
		}
		return store.Staff{}, err
	}
	if !staff.Active {
		return store.Staff{}, ruleError(CodeRoleForbidden, "操作人 %s 已停用", staffID)
	}
	for _, role := range roles {
		if staff.Role == role {
			return staff, nil
		}
	}
	return store.Staff{}, ruleError(CodeRoleForbidden,
		"操作人 %s 的角色 %s 无权执行该操作", staff.Name, staff.Role)
}

func nonEmpty(field, value string) error {
	if value == "" {
		return ruleError(CodeValidation, "字段 %s 不能为空", field)
	}
	return nil
}

func positiveWeight(field string, value int64) error {
	if value <= 0 {
		return ruleError(CodeValidation, "字段 %s 必须为正整数毫克值，实际为 %d", field, value)
	}
	return nil
}

func atOrNow(value time.Time, now time.Time) time.Time {
	if value.IsZero() {
		return now
	}
	return value
}

// RegisterStaffInput 登记员工（内部账号由管理员建立）。
type RegisterStaffInput struct {
	ID   string
	Name string
	Role string
}

func (s *Service) RegisterStaff(ctx context.Context, in RegisterStaffInput) (store.Staff, error) {
	if err := nonEmpty("name", in.Name); err != nil {
		return store.Staff{}, err
	}
	if err := nonEmpty("id", in.ID); err != nil {
		return store.Staff{}, err
	}
	switch in.Role {
	case RoleWarehouse, RoleInspector, RoleProductionManager, RolePharmacyHead:
	default:
		return store.Staff{}, ruleError(CodeValidation, "未知角色：%s", in.Role)
	}
	staff := store.Staff{ID: in.ID, Name: in.Name, Role: in.Role, Active: true, CreatedAt: s.timeNow()}
	if err := s.store.CreateStaff(ctx, staff); err != nil {
		return store.Staff{}, mapStoreError(err)
	}
	return staff, nil
}

// RegisterMaterialInput 登记药材品种。
type RegisterMaterialInput struct {
	Code string
	Name string
}

func (s *Service) RegisterMaterial(ctx context.Context, in RegisterMaterialInput) (store.Material, error) {
	if err := nonEmpty("code", in.Code); err != nil {
		return store.Material{}, err
	}
	if err := nonEmpty("name", in.Name); err != nil {
		return store.Material{}, err
	}
	material := store.Material{ID: newID("mat"), Code: in.Code, Name: in.Name}
	if err := s.store.CreateMaterial(ctx, material); err != nil {
		return store.Material{}, mapStoreError(err)
	}
	return material, nil
}

// RegisterSupplierInput 登记供应商与产地。
type RegisterSupplierInput struct {
	ID           string
	Name         string
	Origin       string
	LicenseScope string
	OperatorID   string
}

func (s *Service) RegisterSupplier(ctx context.Context, in RegisterSupplierInput) (store.Supplier, error) {
	if err := nonEmpty("id", in.ID); err != nil {
		return store.Supplier{}, err
	}
	if err := nonEmpty("name", in.Name); err != nil {
		return store.Supplier{}, err
	}
	if err := nonEmpty("origin", in.Origin); err != nil {
		return store.Supplier{}, err
	}
	if err := nonEmpty("license_scope", in.LicenseScope); err != nil {
		return store.Supplier{}, err
	}
	supplier := store.Supplier{}
	err := s.withTx(ctx, func(q store.Tx) error {
		if _, err := requireStaff(ctx, q, in.OperatorID, RoleWarehouse, RolePharmacyHead); err != nil {
			return err
		}
		supplier = store.Supplier{
			ID: in.ID, Name: in.Name, Origin: in.Origin,
			LicenseScope: in.LicenseScope, CreatedAt: s.timeNow(), CreatedBy: in.OperatorID,
		}
		return mapStoreError(store.InsertSupplierIn(ctx, q, supplier))
	})
	if err != nil {
		return store.Supplier{}, err
	}
	return supplier, nil
}

// AddCertificateInput 登记供应商凭证（证照/检验报告等）。
type AddCertificateInput struct {
	SupplierID   string
	CertType     string
	CertRef      string
	LicenseScope string
	IssuedAt     *time.Time
	ExpiresAt    *time.Time
	OperatorID   string
}

func (s *Service) AddCertificate(ctx context.Context, in AddCertificateInput) (store.Certificate, error) {
	if err := nonEmpty("supplier_id", in.SupplierID); err != nil {
		return store.Certificate{}, err
	}
	if err := nonEmpty("cert_type", in.CertType); err != nil {
		return store.Certificate{}, err
	}
	if err := nonEmpty("cert_ref", in.CertRef); err != nil {
		return store.Certificate{}, err
	}
	if err := nonEmpty("license_scope", in.LicenseScope); err != nil {
		return store.Certificate{}, err
	}
	cert := store.Certificate{}
	err := s.withTx(ctx, func(q store.Tx) error {
		if _, err := requireStaff(ctx, q, in.OperatorID, RoleWarehouse, RolePharmacyHead); err != nil {
			return err
		}
		if _, err := store.SupplierIn(ctx, q, in.SupplierID); err != nil {
			return mapStoreError(err)
		}
		cert = store.Certificate{
			ID: newID("cert"), SupplierID: in.SupplierID, CertType: in.CertType,
			CertRef: in.CertRef, LicenseScope: in.LicenseScope,
			IssuedAt: in.IssuedAt, ExpiresAt: in.ExpiresAt,
			RecordedAt: s.timeNow(), RecordedBy: in.OperatorID,
		}
		return mapStoreError(store.InsertCertificateIn(ctx, q, cert))
	})
	if err != nil {
		return store.Certificate{}, err
	}
	return cert, nil
}

// ReceiveInboundLotInput 入厂批次收货：一袋药材进厂，登记净重并开立物料批次根节点。
type ReceiveInboundLotInput struct {
	LotRef       string
	SupplierID   string
	MaterialCode string
	NetWeightMg  int64
	ReceivedAt   time.Time
	ReceiverID   string
}

type InboundResult struct {
	Inbound  store.InboundLot
	Material store.Material
	Lot      store.MaterialLot
}

func (s *Service) ReceiveInboundLot(ctx context.Context, in ReceiveInboundLotInput) (InboundResult, error) {
	if err := nonEmpty("lot_ref", in.LotRef); err != nil {
		return InboundResult{}, err
	}
	if err := nonEmpty("supplier_id", in.SupplierID); err != nil {
		return InboundResult{}, err
	}
	if err := nonEmpty("material_code", in.MaterialCode); err != nil {
		return InboundResult{}, err
	}
	if err := positiveWeight("net_weight_mg", in.NetWeightMg); err != nil {
		return InboundResult{}, err
	}

	result := InboundResult{}
	err := s.withTx(ctx, func(q store.Tx) error {
		if _, err := requireStaff(ctx, q, in.ReceiverID, RoleWarehouse); err != nil {
			return err
		}
		supplier, err := store.SupplierIn(ctx, q, in.SupplierID)
		if err != nil {
			return mapStoreError(err)
		}
		material, err := store.MaterialByCodeIn(ctx, q, in.MaterialCode)
		if err != nil {
			return mapStoreError(err)
		}
		now := s.timeNow()
		receivedAt := atOrNow(in.ReceivedAt, now)

		inbound := store.InboundLot{
			ID: newID("inb"), LotRef: in.LotRef, SupplierID: supplier.ID,
			MaterialID: material.ID, NetWeightMg: in.NetWeightMg,
			ReceivedAt: receivedAt, ReceiverID: in.ReceiverID, CreatedAt: now,
		}
		if err := store.InsertInboundLotIn(ctx, q, inbound); err != nil {
			return mapStoreError(err)
		}
		// 入厂批次即物料批次谱系的根节点，收货后隔离待检。
		rootLot := store.MaterialLot{
			ID: newID("lot"), LotRef: in.LotRef, MaterialID: material.ID,
			RootInboundLotID: inbound.ID, InitialWeightMg: in.NetWeightMg,
			State: "quarantined", CreatedAt: now, CreatedBy: in.ReceiverID,
		}
		if err := store.InsertMaterialLotIn(ctx, q, rootLot); err != nil {
			return mapStoreError(err)
		}
		movement := store.Movement{
			ID: newID("mov"), MovementType: "receipt", Ref: inbound.ID,
			CreatedAt: now, CreatedBy: in.ReceiverID, Remark: "入厂收货入账",
		}
		item := store.MovementItem{
			ID: newID("itm"), MovementID: movement.ID, MaterialLotID: rootLot.ID,
			Direction: "in", WeightMg: in.NetWeightMg,
		}
		if err := store.InsertMovementIn(ctx, q, movement, []store.MovementItem{item}); err != nil {
			return mapStoreError(err)
		}
		result = InboundResult{Inbound: inbound, Material: material, Lot: rootLot}
		return nil
	})
	return result, err
}

func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return &RuleError{Code: CodeNotFound, Msg: err.Error()}
	}
	if errors.Is(err, store.ErrConflict) {
		return &RuleError{Code: CodeConflict, Msg: err.Error()}
	}
	return err
}
