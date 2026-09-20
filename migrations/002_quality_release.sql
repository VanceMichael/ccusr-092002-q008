-- 院内制剂质量放行：来货凭证 → 抽样检验 → 分装/合批 → 称量配制 → 成品检验 → 放行
-- 全部重量以克为单位，使用 INTEGER 毫毫克？否：直接使用 REAL 克，并在应用层做守恒核对。
-- 时间一律 TEXT，ISO 8601 带偏移。

INSERT OR IGNORE INTO schema_migrations(version) VALUES ('002_quality_release');

-- 用户与角色。角色在应用层固定为四类，数据库不做枚举扩展。
CREATE TABLE IF NOT EXISTS users (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    role        TEXT NOT NULL CHECK (role IN (
                    'warehouse',          -- 药库/供应商凭证登记、分装合批
                    'inspector',          -- 检验人（实验室复检、成品检验）
                    'compounding_lead',   -- 配制负责人（投料、配制）
                    'qa_release'          -- 药事负责人（成品放行、血缘反查）
                )),
    active      INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL
);

-- 物料（药材）主数据与合格范围
CREATE TABLE IF NOT EXISTS materials (
    id              TEXT PRIMARY KEY,
    code            TEXT NOT NULL UNIQUE,         -- 院内药材编码
    name            TEXT NOT NULL,
    spec_min        REAL NOT NULL,                -- 合格判定下界（指标值）
    spec_max        REAL NOT NULL,                -- 合格判定上界
    spec_unit       TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    CHECK (spec_min <= spec_max)
);

-- 院内方剂及历史组方版本（version 为递增修订号）
CREATE TABLE IF NOT EXISTS formulas (
    id              TEXT PRIMARY KEY,
    formula_ref     TEXT NOT NULL,
    revision        INTEGER NOT NULL,
    name            TEXT NOT NULL,
    license_scope   TEXT NOT NULL DEFAULT '',     -- 配制许可范围
    created_at      TEXT NOT NULL,
    UNIQUE (formula_ref, revision)
);

-- 组方明细：每个方剂修订版所需药材及理论用量（克）
CREATE TABLE IF NOT EXISTS formula_items (
    id              TEXT PRIMARY KEY,
    formula_id      TEXT NOT NULL REFERENCES formulas(id),
    material_id     TEXT NOT NULL REFERENCES materials(id),
    required_weight REAL NOT NULL CHECK (required_weight > 0),
    ordinal         INTEGER NOT NULL DEFAULT 0,
    UNIQUE (formula_id, material_id)
);

-- 来货批次：供应商凭证、产地、来货重量（入厂批次）
CREATE TABLE IF NOT EXISTS receipts (
    id                TEXT PRIMARY KEY,
    receipt_ref       TEXT NOT NULL UNIQUE,       -- 来货批号
    material_id       TEXT NOT NULL REFERENCES materials(id),
    supplier          TEXT NOT NULL,
    supplier_voucher  TEXT NOT NULL,              -- 供应商凭证编号
    origin            TEXT NOT NULL DEFAULT '',   -- 产地
    received_weight   REAL NOT NULL CHECK (received_weight > 0),
    received_at       TEXT NOT NULL,
    recorded_by       TEXT NOT NULL REFERENCES users(id),
    -- disposition: pending 待判定 / released 合格放行 / rejected 不合格拒收 / quarantined 隔离待定
    disposition       TEXT NOT NULL DEFAULT 'pending'
                      CHECK (disposition IN ('pending','released','rejected','quarantined')),
    disposition_note  TEXT NOT NULL DEFAULT ''
);

-- 抽样记录：一次来货可多次抽样
CREATE TABLE IF NOT EXISTS samples (
    id            TEXT PRIMARY KEY,
    receipt_id    TEXT NOT NULL REFERENCES receipts(id),
    sample_ref    TEXT NOT NULL UNIQUE,
    taken_at      TEXT NOT NULL,
    taken_by      TEXT NOT NULL REFERENCES users(id),
    note          TEXT NOT NULL DEFAULT ''
);

-- 检验轮次（append-only）。
-- 第一次失败的读数必须保留：复检新增一行，绝不更新旧行。
-- result 只允许 inspector 在取样后录入；released/rejected 判定在服务层综合所有轮次。
CREATE TABLE IF NOT EXISTS inspections (
    id            TEXT PRIMARY KEY,
    sample_id     TEXT NOT NULL REFERENCES samples(id),
    round_no      INTEGER NOT NULL CHECK (round_no >= 1),
    measured      REAL NOT NULL,
    unit          TEXT NOT NULL DEFAULT '',
    outcome       TEXT NOT NULL CHECK (outcome IN ('pass','fail')),
    is_retest     INTEGER NOT NULL DEFAULT 0,
    method        TEXT NOT NULL DEFAULT '',
    inspected_by  TEXT NOT NULL REFERENCES users(id),
    inspected_at  TEXT NOT NULL,
    note          TEXT NOT NULL DEFAULT '',
    UNIQUE (sample_id, round_no)
);

-- 数据库层兜底：检验记录只许插入，不许修改/删除，首败读数物理上不可覆盖。
CREATE TRIGGER IF NOT EXISTS trg_inspections_no_update
BEFORE UPDATE ON inspections
BEGIN
    SELECT RAISE(ABORT, '检验记录为追加式，禁止修改（复检必须新增一轮）');
END;
CREATE TRIGGER IF NOT EXISTS trg_inspections_no_delete
BEFORE DELETE ON inspections
BEGIN
    SELECT RAISE(ABORT, '检验记录为追加式，禁止删除');
END;

-- 质量判定流水：对来货批次的每次处置都留痕（pending→quarantined/rejected/released）
CREATE TABLE IF NOT EXISTS receipt_decisions (
    id            TEXT PRIMARY KEY,
    receipt_id    TEXT NOT NULL REFERENCES receipts(id),
    from_state    TEXT NOT NULL,
    to_state      TEXT NOT NULL,
    decided_by    TEXT NOT NULL REFERENCES users(id),
    decided_at    TEXT NOT NULL,
    note          TEXT NOT NULL DEFAULT ''
);

-- 分装：从来货批次拆出的小包装/分装桶，重量可逐袋核对。
CREATE TABLE IF NOT EXISTS containers (
    id            TEXT PRIMARY KEY,
    receipt_id    TEXT NOT NULL REFERENCES receipts(id),
    container_ref TEXT NOT NULL UNIQUE,
    weight        REAL NOT NULL CHECK (weight > 0),  -- 分装净重（克）
    created_at    TEXT NOT NULL,
    created_by    TEXT NOT NULL REFERENCES users(id)
);

-- 合批：多个分装容器（可跨来货批次）合并为一个混合批；总量守恒。
CREATE TABLE IF NOT EXISTS merges (
    id            TEXT PRIMARY KEY,
    merge_ref     TEXT NOT NULL UNIQUE,
    material_id   TEXT NOT NULL REFERENCES materials(id),
    output_weight REAL NOT NULL CHECK (output_weight > 0),  -- 实际产出重量
    created_at    TEXT NOT NULL,
    created_by    TEXT NOT NULL REFERENCES users(id),
    note          TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS merge_sources (
    id            TEXT PRIMARY KEY,
    merge_id      TEXT NOT NULL REFERENCES merges(id),
    container_id  TEXT NOT NULL REFERENCES containers(id),
    input_weight  REAL NOT NULL CHECK (input_weight > 0),   -- 从该容器取用重量
    UNIQUE (merge_id, container_id)
);

-- 配制批次（成品批）
CREATE TABLE IF NOT EXISTS batches (
    id               TEXT PRIMARY KEY,
    batch_ref        TEXT NOT NULL UNIQUE,
    formula_id       TEXT NOT NULL REFERENCES formulas(id),
    lead_id          TEXT NOT NULL REFERENCES users(id),  -- 配制负责人
    planned_at       TEXT NOT NULL,
    started_at       TEXT NOT NULL DEFAULT '',
    -- planned 已计划 / in_progress 投料中 / compounded 配制完成待检 / inspected 成品已检 / released 放行 / blocked 阻断
    status           TEXT NOT NULL DEFAULT 'planned'
                     CHECK (status IN ('planned','in_progress','compounded','inspected','released','blocked')),
    blocked_reason   TEXT NOT NULL DEFAULT '',
    yield_weight     REAL NOT NULL DEFAULT 0
);

-- 称量投料：成品批次的每条投料行，来源可以是分装容器或合批批，重量逐条记录。
CREATE TABLE IF NOT EXISTS weighings (
    id             TEXT PRIMARY KEY,
    batch_id       TEXT NOT NULL REFERENCES batches(id),
    material_id    TEXT NOT NULL REFERENCES materials(id),
    container_id   TEXT REFERENCES containers(id),
    merge_id       TEXT REFERENCES merges(id),
    weight         REAL NOT NULL CHECK (weight > 0),
    weighed_at     TEXT NOT NULL,
    weighed_by     TEXT NOT NULL REFERENCES users(id),
    CHECK ((container_id IS NOT NULL AND merge_id IS NULL)
        OR (merge_id IS NOT NULL AND container_id IS NULL))
);

-- 成品检验：检验人不得是该批次配制负责人（服务层 + CHECK 约束双重保证）。
CREATE TABLE IF NOT EXISTS finished_inspections (
    id             TEXT PRIMARY KEY,
    batch_id       TEXT NOT NULL REFERENCES batches(id),
    round_no       INTEGER NOT NULL DEFAULT 1,
    outcome        TEXT NOT NULL CHECK (outcome IN ('pass','fail')),
    measured       TEXT NOT NULL DEFAULT '',   -- 多项成品指标以文本/JSON 记录，不参与原料判定
    inspector_id   TEXT NOT NULL REFERENCES users(id),
    inspected_at   TEXT NOT NULL,
    note           TEXT NOT NULL DEFAULT '',
    UNIQUE (batch_id, round_no)
);

-- SQLite 的 CHECK 不允许子查询，用触发器强制：检验人不得是本批配制负责人。
CREATE TRIGGER IF NOT EXISTS trg_finished_inspection_not_self
BEFORE INSERT ON finished_inspections
BEGIN
    SELECT RAISE(ABORT, '成品检验人不得是该批次的配制负责人')
    WHERE NEW.inspector_id = (SELECT lead_id FROM batches WHERE id = NEW.batch_id);
END;
CREATE TRIGGER IF NOT EXISTS trg_finished_inspections_no_update
BEFORE UPDATE ON finished_inspections
BEGIN
    SELECT RAISE(ABORT, '成品检验记录为追加式，禁止修改');
END;
CREATE TRIGGER IF NOT EXISTS trg_finished_inspections_no_delete
BEFORE DELETE ON finished_inspections
BEGIN
    SELECT RAISE(ABORT, '成品检验记录为追加式，禁止删除');
END;

-- 成品放行决定（药事负责人签字，追加式）；放行时再做一次全链阻断校验。
-- 放行后若追查出上游不合格，可再追加一条 withheld 召回/扣留，原签字仍保留可查。
CREATE TABLE IF NOT EXISTS release_decisions (
    id             TEXT PRIMARY KEY,
    batch_id       TEXT NOT NULL REFERENCES batches(id),
    round_no       INTEGER NOT NULL,
    decision       TEXT NOT NULL CHECK (decision IN ('released','rejected','blocked','withheld')),
    decided_by     TEXT NOT NULL REFERENCES users(id),
    decided_at     TEXT NOT NULL,
    note           TEXT NOT NULL DEFAULT '',
    UNIQUE (batch_id, round_no)
);

-- 放行签字人不得是配制负责人（职责分离）。
CREATE TRIGGER IF NOT EXISTS trg_release_not_lead
BEFORE INSERT ON release_decisions
BEGIN
    SELECT RAISE(ABORT, '药事放行签字人不得是该批次的配制负责人')
    WHERE NEW.decided_by = (SELECT lead_id FROM batches WHERE id = NEW.batch_id);
END;

CREATE TRIGGER IF NOT EXISTS trg_release_decisions_no_update
BEFORE UPDATE ON release_decisions
BEGIN
    SELECT RAISE(ABORT, '放行决定为追加式，禁止修改');
END;
CREATE TRIGGER IF NOT EXISTS trg_release_decisions_no_delete
BEFORE DELETE ON release_decisions
BEGIN
    SELECT RAISE(ABORT, '放行决定为追加式，禁止删除');
END;

-- 常用反查索引
CREATE INDEX IF NOT EXISTS idx_samples_receipt ON samples(receipt_id);
CREATE INDEX IF NOT EXISTS idx_inspections_sample ON inspections(sample_id);
CREATE INDEX IF NOT EXISTS idx_containers_receipt ON containers(receipt_id);
CREATE INDEX IF NOT EXISTS idx_merge_sources_merge ON merge_sources(merge_id);
CREATE INDEX IF NOT EXISTS idx_merge_sources_container ON merge_sources(container_id);
CREATE INDEX IF NOT EXISTS idx_weighings_batch ON weighings(batch_id);
CREATE INDEX IF NOT EXISTS idx_weighings_container ON weighings(container_id);
CREATE INDEX IF NOT EXISTS idx_weighings_merge ON weighings(merge_id);
CREATE INDEX IF NOT EXISTS idx_receipt_decisions ON receipt_decisions(receipt_id);
