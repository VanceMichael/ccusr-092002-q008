package domain

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Bootstrap 自举首个用户。仅当用户表为空时允许（免角色），用于初始化四类岗位账号。
// 之后创建用户须由已存在的内部账号调用 CreateUser。
func (s *Service) Bootstrap(ctx context.Context, in CreateUserInput) (*Actor, error) {
	if err := validateUserInput(in); err != nil {
		return nil, err
	}
	created := &Actor{}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		n, err := s.countUsers(ctx, tx)
		if err != nil {
			return err
		}
		if n > 0 {
			return fail(409, "already_bootstrapped", "系统已初始化，新增用户请使用内部接口")
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO users (id, name, role, active, created_at) VALUES (?, ?, ?, 1, ?)`,
			in.ID, in.Name, in.Role, nowISO())
		if err != nil {
			return mapUnique(err, "用户编号已存在")
		}
		created.ID, created.Name, created.Role, created.Active = in.ID, in.Name, in.Role, true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// CreateUser 由已认证的内部人员创建新账号。
func (s *Service) CreateUser(ctx context.Context, callerID string, in CreateUserInput) (*Actor, error) {
	if err := validateUserInput(in); err != nil {
		return nil, err
	}
	created := &Actor{}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.authenticate(ctx, tx, callerID,
			RoleWarehouse, RoleInspector, RoleCompoundingLead, RoleQARelease); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO users (id, name, role, active, created_at) VALUES (?, ?, ?, 1, ?)`,
			in.ID, in.Name, in.Role, nowISO())
		if err != nil {
			return mapUnique(err, "用户编号已存在")
		}
		created.ID, created.Name, created.Role, created.Active = in.ID, in.Name, in.Role, true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func validateUserInput(in CreateUserInput) error {
	if in.ID == "" || in.Name == "" {
		return fail(400, "invalid_input", "id 与 name 不能为空")
	}
	switch in.Role {
	case RoleWarehouse, RoleInspector, RoleCompoundingLead, RoleQARelease:
		return nil
	default:
		return fail(400, "invalid_role", "未知角色: "+in.Role)
	}
}

// CreateMaterial 登记药材及合格范围（spec_min/spec_max 为放行判定区间）。
func (s *Service) CreateMaterial(ctx context.Context, callerID string, in CreateMaterialInput) (*MaterialView, error) {
	if in.Code == "" || in.Name == "" {
		return nil, fail(400, "invalid_input", "code 与 name 不能为空")
	}
	if in.SpecMin > in.SpecMax {
		return nil, fail(400, "invalid_spec", "spec_min 不能大于 spec_max")
	}
	view := &MaterialView{SpecUnit: in.SpecUnit}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.authenticate(ctx, tx, callerID,
			RoleWarehouse, RoleQARelease); err != nil {
			return err
		}
		id := newID()
		_, err := tx.ExecContext(ctx,
			`INSERT INTO materials (id, code, name, spec_min, spec_max, spec_unit, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, in.Code, in.Name, in.SpecMin, in.SpecMax, in.SpecUnit, nowISO())
		if err != nil {
			return mapUnique(err, "药材编码已存在")
		}
		view.ID, view.Code, view.Name, view.SpecMin, view.SpecMax = id, in.Code, in.Name, in.SpecMin, in.SpecMax
		return nil
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

// CreateFormula 登记方剂的某个组方版本。历史版本各自独立保存。
func (s *Service) CreateFormula(ctx context.Context, callerID string, in CreateFormulaInput) (*FormulaView, error) {
	if in.FormulaRef == "" || in.Name == "" || in.Revision < 1 {
		return nil, fail(400, "invalid_input", "formula_ref、name 必填，revision 须 ≥ 1")
	}
	if len(in.Items) == 0 {
		return nil, fail(400, "invalid_input", "组方至少包含一味药材")
	}
	for _, item := range in.Items {
		if item.MaterialCode == "" || item.RequiredWeight <= 0 {
			return nil, fail(400, "invalid_input", "组方明细的药材编码与正用量必填")
		}
	}
	view := &FormulaView{FormulaRef: in.FormulaRef, Revision: in.Revision, Name: in.Name,
		LicenseScope: in.LicenseScope}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.authenticate(ctx, tx, callerID,
			RoleCompoundingLead, RoleQARelease); err != nil {
			return err
		}
		formulaID := newID()
		_, err := tx.ExecContext(ctx,
			`INSERT INTO formulas (id, formula_ref, revision, name, license_scope, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			formulaID, in.FormulaRef, in.Revision, in.Name, in.LicenseScope, nowISO())
		if err != nil {
			return mapUnique(err, "该方剂版本已存在")
		}
		for ord, item := range in.Items {
			var materialID, materialName string
			err := tx.QueryRowContext(ctx,
				`SELECT id, name FROM materials WHERE code = ?`, item.MaterialCode).
				Scan(&materialID, &materialName)
			if errors.Is(err, sql.ErrNoRows) {
				return fail(400, "unknown_material", "未知药材编码: "+item.MaterialCode)
			}
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO formula_items (id, formula_id, material_id, required_weight, ordinal)
				 VALUES (?, ?, ?, ?, ?)`,
				newID(), formulaID, materialID, item.RequiredWeight, ord); err != nil {
				return err
			}
			view.Items = append(view.Items, FormulaItemView{
				MaterialCode:   item.MaterialCode,
				MaterialName:   materialName,
				RequiredWeight: item.RequiredWeight,
			})
		}
		view.ID = formulaID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

func mapUnique(err error, message string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// modernc.org/sqlite 对唯一约束返回包含 UNIQUE constraint failed 的错误。
	if strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "constraint failed") {
		return fail(409, "duplicate", message)
	}
	return err
}
