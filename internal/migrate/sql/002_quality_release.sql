-- 质量放行领域：入厂批次 → 抽样/检验 → 分装/合批 → 称量/投料 → 配制批次 → 放行
-- 所有重量以 INTEGER 毫克存储，避免浮点误差；拆分/合批在应用层按逐笔台账核对守恒。

CREATE TABLE IF NOT EXISTS staff (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    role       TEXT NOT NULL CHECK (role IN ('warehouse','inspector','production_manager','pharmacy_head')),
    active     INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS materials (
    id   TEXT PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS suppliers (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    origin        TEXT NOT NULL,
    license_scope TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    created_by    TEXT NOT NULL REFERENCES staff(id)
);

CREATE TABLE IF NOT EXISTS supplier_certificates (
    id            TEXT PRIMARY KEY,
    supplier_id   TEXT NOT NULL REFERENCES suppliers(id),
    cert_type     TEXT NOT NULL,
    cert_ref      TEXT NOT NULL,
    license_scope TEXT NOT NULL,
    issued_at     TEXT,
    expires_at    TEXT,
    recorded_at   TEXT NOT NULL,
    recorded_by   TEXT NOT NULL REFERENCES staff(id)
);

-- 入厂批次：一袋药材进厂时的凭证根节点
CREATE TABLE IF NOT EXISTS inbound_lots (
    id              TEXT PRIMARY KEY,
    lot_ref         TEXT NOT NULL UNIQUE,
    supplier_id     TEXT NOT NULL REFERENCES suppliers(id),
    material_id     TEXT NOT NULL REFERENCES materials(id),
    net_weight_mg   INTEGER NOT NULL CHECK (net_weight_mg > 0),
    received_at     TEXT NOT NULL,
    receiver_id     TEXT NOT NULL REFERENCES staff(id),
    created_at      TEXT NOT NULL
);

-- 物料批次节点：入厂批次本身是根节点；分装(拆分)和合批产生子节点
CREATE TABLE IF NOT EXISTS material_lots (
    id                    TEXT PRIMARY KEY,
    lot_ref               TEXT NOT NULL UNIQUE,
    material_id           TEXT NOT NULL REFERENCES materials(id),
    root_inbound_lot_id   TEXT REFERENCES inbound_lots(id),
    initial_weight_mg     INTEGER NOT NULL CHECK (initial_weight_mg >= 0),
    state                 TEXT NOT NULL CHECK (state IN
                              ('quarantined','held','available','rejected','disposed')),
    had_failure           INTEGER NOT NULL DEFAULT 0,
    created_at            TEXT NOT NULL,
    created_by            TEXT NOT NULL REFERENCES staff(id)
);

-- 重量台账：receipt（收货入账）/ split（分装）/ merge（合批）/ weigh（称量领出）
CREATE TABLE IF NOT EXISTS lot_movements (
    id            TEXT PRIMARY KEY,
    movement_type TEXT NOT NULL CHECK (movement_type IN ('receipt','sample','split','merge','weigh')),
    ref           TEXT,
    created_at    TEXT NOT NULL,
    created_by    TEXT NOT NULL REFERENCES staff(id),
    remark        TEXT
);

CREATE TABLE IF NOT EXISTS lot_movement_items (
    id              TEXT PRIMARY KEY,
    movement_id     TEXT NOT NULL REFERENCES lot_movements(id),
    material_lot_id TEXT NOT NULL REFERENCES material_lots(id),
    direction       TEXT NOT NULL CHECK (direction IN ('in','out')),
    weight_mg       INTEGER NOT NULL CHECK (weight_mg >= 0)
);

CREATE INDEX IF NOT EXISTS idx_movement_items_lot ON lot_movement_items(material_lot_id);

-- 院内制剂配制批次（成品批）
CREATE TABLE IF NOT EXISTS compound_batches (
    id                     TEXT PRIMARY KEY,
    batch_ref              TEXT NOT NULL UNIQUE,
    formula_ref            TEXT NOT NULL,
    formula_revision       INTEGER NOT NULL,
    product_name           TEXT NOT NULL,
    production_manager_id  TEXT NOT NULL REFERENCES staff(id),
    status                 TEXT NOT NULL CHECK (status IN
                             ('planned','charged','qc_passed','released','rejected','recalled')),
    quality_blocked        INTEGER NOT NULL DEFAULT 0,
    quality_blocked_reason TEXT NOT NULL DEFAULT '',
    created_at             TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS weighings (
    id                TEXT PRIMARY KEY,
    compound_batch_id TEXT NOT NULL REFERENCES compound_batches(id),
    material_lot_id   TEXT NOT NULL REFERENCES material_lots(id),
    movement_item_id  TEXT NOT NULL REFERENCES lot_movement_items(id),
    weight_mg         INTEGER NOT NULL CHECK (weight_mg > 0),
    weighed_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_weighings_lot ON weighings(material_lot_id);

CREATE TABLE IF NOT EXISTS chargings (
    id                TEXT PRIMARY KEY,
    compound_batch_id TEXT NOT NULL REFERENCES compound_batches(id),
    material_lot_id   TEXT NOT NULL REFERENCES material_lots(id),
    weighing_id       TEXT NOT NULL REFERENCES weighings(id),
    weight_mg         INTEGER NOT NULL CHECK (weight_mg > 0),
    charged_at        TEXT NOT NULL,
    charged_by        TEXT NOT NULL REFERENCES staff(id)
);

CREATE INDEX IF NOT EXISTS idx_chargings_batch ON chargings(compound_batch_id);
CREATE INDEX IF NOT EXISTS idx_chargings_lot ON chargings(material_lot_id);

-- 抽样：可对物料批次或配制批次抽样；retest（复检）显式回溯原抽样
CREATE TABLE IF NOT EXISTS samples (
    id                   TEXT PRIMARY KEY,
    sample_ref           TEXT NOT NULL UNIQUE,
    subject_type         TEXT NOT NULL CHECK (subject_type IN ('material_lot','compound_batch')),
    subject_id           TEXT NOT NULL,
    kind                 TEXT NOT NULL CHECK (kind IN ('initial','retest')),
    retest_of_sample_id  TEXT REFERENCES samples(id),
    qty_mg               INTEGER NOT NULL CHECK (qty_mg > 0),
    sampled_at           TEXT NOT NULL,
    sampler_id           TEXT NOT NULL REFERENCES staff(id)
);

CREATE INDEX IF NOT EXISTS idx_samples_subject ON samples(subject_type, subject_id);

-- 检验单：只能从 open → concluded 一次，结论不允许改写
CREATE TABLE IF NOT EXISTS inspections (
    id             TEXT PRIMARY KEY,
    inspection_ref TEXT NOT NULL UNIQUE,
    sample_id      TEXT NOT NULL REFERENCES samples(id),
    opened_at      TEXT NOT NULL,
    opened_by      TEXT NOT NULL REFERENCES staff(id),
    concluded_at   TEXT,
    verdict        TEXT CHECK (verdict IS NULL OR verdict IN ('qualified','unqualified')),
    concluded_by   TEXT REFERENCES staff(id),
    remark         TEXT
);

-- 检验读数：逐笔追加，数据库层禁止 UPDATE/DELETE，复检读数不能覆盖第一次失败读数
CREATE TABLE IF NOT EXISTS test_readings (
    id             TEXT PRIMARY KEY,
    inspection_id  TEXT NOT NULL REFERENCES inspections(id),
    seq            INTEGER NOT NULL,
    test_item      TEXT NOT NULL,
    spec_min       TEXT,
    spec_max       TEXT,
    unit           TEXT,
    observed_value TEXT NOT NULL,
    outcome        TEXT NOT NULL CHECK (outcome IN ('pass','fail')),
    measured_at    TEXT NOT NULL,
    inspector_id   TEXT NOT NULL REFERENCES staff(id),
    remark         TEXT,
    UNIQUE(inspection_id, seq)
);

CREATE TRIGGER trg_readings_immutable_update
BEFORE UPDATE ON test_readings
BEGIN
    SELECT RAISE(ABORT, 'test_readings 为追加只读记录，不得修改（失败读数必须保留）');
END;

CREATE TRIGGER trg_readings_immutable_delete
BEFORE DELETE ON test_readings
BEGIN
    SELECT RAISE(ABORT, 'test_readings 为追加只读记录，不得删除（失败读数必须保留）');
END;

CREATE TRIGGER trg_inspection_verdict_locked
BEFORE UPDATE OF verdict ON inspections
WHEN OLD.verdict IS NOT NULL AND NEW.verdict IS NOT OLD.verdict
BEGIN
    SELECT RAISE(ABORT, '检验结论一经作出不得更改，请通过复检流程记录');
END;

-- 放行决策：逐笔保留（hold/reject/release），以最新一笔为准；只有药事负责人可签
CREATE TABLE IF NOT EXISTS release_decisions (
    id                     TEXT PRIMARY KEY,
    compound_batch_id      TEXT NOT NULL REFERENCES compound_batches(id),
    decision               TEXT NOT NULL CHECK (decision IN ('released','held','rejected','recalled')),
    finished_inspection_id TEXT REFERENCES inspections(id),
    reason                 TEXT,
    decided_at             TEXT NOT NULL,
    decided_by             TEXT NOT NULL REFERENCES staff(id)
);

CREATE INDEX IF NOT EXISTS idx_release_batch ON release_decisions(compound_batch_id);

-- 不合格物料的处置记录
CREATE TABLE IF NOT EXISTS lot_disposals (
    id               TEXT PRIMARY KEY,
    material_lot_id  TEXT NOT NULL REFERENCES material_lots(id),
    disposition_type TEXT NOT NULL CHECK (disposition_type IN
                       ('destroy','return_to_supplier','deactivated')),
    disposed_at      TEXT NOT NULL,
    disposed_by      TEXT NOT NULL REFERENCES staff(id),
    remark           TEXT
);
