package httpapi

import (
	"net/http"
	"testing"
)

func TestHealth(t *testing.T) {
	h := newHarness(t)
	code, body := h.get("/health", "")
	expectCode(t, code, http.StatusOK, body)
	if body["status"] != "ok" {
		t.Fatalf("健康检查响应异常: %v", body)
	}
}

// setupTwoMaterials 建立两味药材（合格范围不同）与一个各需 100g 的方剂修订版。
func setupTwoMaterials(h *harness) {
	t := h.t
	materials := []map[string]any{
		{"code": "HQ", "name": "黄芪", "spec_min": 90.0, "spec_max": 110.0, "spec_unit": "%"},
		{"code": "DG", "name": "当归", "spec_min": 60.0, "spec_max": 80.0, "spec_unit": "%"},
	}
	for _, m := range materials {
		code, body := h.post("/v1/materials", h.users["qa"], m)
		expectCode(t, code, http.StatusCreated, body)
	}
	formula := map[string]any{
		"formula_ref": "F-BUXI", "revision": 1, "name": "补益方", "license_scope": "本机构使用",
		"items": []map[string]any{
			{"material_code": "HQ", "required_weight": 100.0},
			{"material_code": "DG", "required_weight": 100.0},
		},
	}
	code, body := h.post("/v1/formulas", h.users["lead"], formula)
	expectCode(t, code, http.StatusCreated, body)
}

// qualifyReceipt 登记来货、抽样、录入一轮合格读数并由药事放行。
func qualifyReceipt(h *harness, ref, material string, weight, measured float64) {
	t := h.t
	receipt := map[string]any{
		"receipt_ref": ref, "material_code": material,
		"supplier": "供应商-" + ref, "supplier_voucher": "V-" + ref,
		"origin": "产地-" + ref, "received_weight": weight,
	}
	code, body := h.post("/v1/receipts", h.users["warehouse"], receipt)
	expectCode(t, code, http.StatusCreated, body)

	code, body = h.post("/v1/receipts/"+ref+"/samples", h.users["inspector"],
		map[string]any{"sample_ref": "S-" + ref})
	expectCode(t, code, http.StatusCreated, body)

	code, body = h.post("/v1/samples/S-"+ref+"/inspections", h.users["inspector"],
		map[string]any{"measured": measured, "method": "液相"})
	expectCode(t, code, http.StatusCreated, body)
	if body["outcome"] != "pass" {
		t.Fatalf("来货 %s 首检应为 pass，实际 %v", ref, body["outcome"])
	}

	code, body = h.post("/v1/receipts/"+ref+"/decision", h.users["qa"],
		map[string]any{"target": "released", "note": "合格放行"})
	expectCode(t, code, http.StatusOK, body)
}

func dispense(h *harness, ref string, splits ...map[string]any) {
	h.t.Helper()
	code, body := h.post("/v1/receipts/"+ref+"/dispense", h.users["warehouse"],
		map[string]any{"splits": splits})
	expectCode(h.t, code, http.StatusCreated, body)
}

// buildReleasedBatch 投料两味药材各 100g、完成配制、成品检验合格并放行。
func buildReleasedBatch(h *harness, batchRef, hqContainer, dgContainer string) {
	t := h.t
	code, body := h.post("/v1/batches", h.users["lead"],
		map[string]any{"batch_ref": batchRef, "formula_ref": "F-BUXI", "revision": 1})
	expectCode(t, code, http.StatusCreated, body)

	code, body = h.post("/v1/batches/"+batchRef+"/weighings", h.users["lead"],
		map[string]any{"material_code": "HQ", "container_ref": hqContainer, "weight": 100.0})
	expectCode(t, code, http.StatusCreated, body)
	code, body = h.post("/v1/batches/"+batchRef+"/weighings", h.users["lead"],
		map[string]any{"material_code": "DG", "container_ref": dgContainer, "weight": 100.0})
	expectCode(t, code, http.StatusCreated, body)

	code, body = h.post("/v1/batches/"+batchRef+"/finish", h.users["lead"],
		map[string]any{"yield_weight": 190.0})
	expectCode(t, code, http.StatusOK, body)

	code, body = h.post("/v1/batches/"+batchRef+"/inspections", h.users["inspector"],
		map[string]any{"outcome": "pass", "measured": "性状、鉴别、含量均符合规定"})
	expectCode(t, code, http.StatusCreated, body)

	code, body = h.post("/v1/batches/"+batchRef+"/release", h.users["qa"],
		map[string]any{"decision": "released", "note": "同意放行"})
	expectCode(t, code, http.StatusOK, body)
}

// 全链路 happy path：合格原料配制的成品可放行，血缘、核对、外部查询均正确。
func TestHappyPathReleaseAndTrace(t *testing.T) {
	h := newHarness(t)
	setupTwoMaterials(h)
	qualifyReceipt(h, "R-HQ-1", "HQ", 500, 100)
	qualifyReceipt(h, "R-DG-1", "DG", 300, 70)
	dispense(h, "R-HQ-1",
		map[string]any{"container_ref": "C-HQ-1", "weight": 250.0},
		map[string]any{"container_ref": "C-HQ-2", "weight": 250.0})
	dispense(h, "R-DG-1",
		map[string]any{"container_ref": "C-DG-1", "weight": 300.0})

	buildReleasedBatch(h, "B-001", "C-HQ-1", "C-DG-1")

	// 批次详情：状态 released，放行签字人是药事负责人而非配制负责人。
	code, batch := h.get("/v1/batches/B-001", h.users["qa"])
	expectCode(t, code, http.StatusOK, batch)
	if batch["status"] != "released" {
		t.Fatalf("成品批应为 released，实际 %v", batch["status"])
	}
	rel := batch["release"].(map[string]any)
	if rel["decision"] != "released" || rel["decided_by"] != "孙药事" {
		t.Fatalf("放行记录异常: %v", rel)
	}
	checks := batch["finished_checks"].([]any)
	if len(checks) != 1 || checks[0].(map[string]any)["inspector"] != "李检验" {
		t.Fatalf("成品检验签字人应为检验人李检验: %v", checks)
	}

	// 血缘反查：节点含两味药材、两个来货批、三个分装容器；无阻断节点。
	code, lineage := h.get("/v1/batches/B-001/lineage", h.users["qa"])
	expectCode(t, code, http.StatusOK, lineage)
	kinds := map[string]int{}
	for _, n := range lineage["nodes"].([]any) {
		kinds[n.(map[string]any)["kind"].(string)]++
	}
	if kinds["receipt"] != 2 || kinds["material"] != 2 || kinds["container"] != 2 || kinds["batch"] != 1 {
		t.Fatalf("血缘节点数量异常: %v", kinds)
	}
	blocking := lineage["blocking_refs"].([]any)
	if len(blocking) != 0 || lineage["releaseable"] != true {
		t.Fatalf("合格批次不应有阻断节点: %v", lineage["blocking_refs"])
	}
	// 每次质量判断都在链上：原料检验、原料处置、成品检验、放行。
	stages := map[string]int{}
	for _, j := range lineage["judgements"].([]any) {
		stages[j.(map[string]any)["stage"].(string)]++
	}
	for _, stage := range []string{"raw_inspection", "raw_decision", "finished_inspection", "release"} {
		if stages[stage] < 1 {
			t.Fatalf("血缘缺少质量判断阶段 %s: %v", stage, stages)
		}
	}

	// 重量核对全部平衡。
	code, wc := h.get("/v1/weight-check", h.users["qa"])
	expectCode(t, code, http.StatusOK, wc)
	if wc["balanced"] != true {
		t.Fatalf("重量核对应平衡: %v", wc)
	}

	// 外部成品查询只给最小字段。
	code, pub := h.get("/public/v1/batches/B-001", "")
	expectCode(t, code, http.StatusOK, pub)
	if pub["status"] != "released" || pub["release"] != "released" || pub["batch_ref"] != "B-001" {
		t.Fatalf("外部成品状态异常: %v", pub)
	}
	if len(pub) != 3 {
		t.Fatalf("外部成品响应只允许 3 个字段，实际 %v", keys(pub))
	}
}

// 复检不得覆盖第一次失败读数：首败+复检合格仍不得放行，两轮读数并存。
func TestRetestCannotOverrideFirstFailure(t *testing.T) {
	h := newHarness(t)
	setupTwoMaterials(h)

	receipt := map[string]any{
		"receipt_ref": "R-BAD", "material_code": "HQ",
		"supplier": "S1", "supplier_voucher": "V1", "origin": "甘肃", "received_weight": 100.0,
	}
	code, body := h.post("/v1/receipts", h.users["warehouse"], receipt)
	expectCode(t, code, http.StatusCreated, body)
	code, body = h.post("/v1/receipts/R-BAD/samples", h.users["inspector"],
		map[string]any{"sample_ref": "S-BAD"})
	expectCode(t, code, http.StatusCreated, body)

	// 第一轮：读数 50，超出 90-110 合格范围 → fail。
	code, body = h.post("/v1/samples/S-BAD/inspections", h.users["inspector"],
		map[string]any{"measured": 50.0})
	expectCode(t, code, http.StatusCreated, body)
	if body["outcome"] != "fail" || body["round_no"].(float64) != 1 || body["is_retest"] != false {
		t.Fatalf("首检应失败且非复检: %v", body)
	}
	// 第二轮（复检）：读数 100 → pass，但不得覆盖第一轮。
	code, body = h.post("/v1/samples/S-BAD/inspections", h.users["inspector2"],
		map[string]any{"measured": 100.0})
	expectCode(t, code, http.StatusCreated, body)
	if body["outcome"] != "pass" || body["round_no"].(float64) != 2 || body["is_retest"] != true {
		t.Fatalf("复检应合格并标记为复检: %v", body)
	}

	// 试图放行：必须因首败读数存在而被拒绝。
	code, body = h.post("/v1/receipts/R-BAD/decision", h.users["qa"],
		map[string]any{"target": "released"})
	expectCode(t, code, http.StatusUnprocessableEntity, body)
	if body["code"] != "failed_reading_present" {
		t.Fatalf("应以 failed_reading_present 阻断，实际 %v", body["code"])
	}

	// 两轮读数仍然并存，签字人各自不同。
	code, detail := h.get("/v1/receipts/R-BAD", h.users["qa"])
	expectCode(t, code, http.StatusOK, detail)
	samples := detail["samples"].([]any)
	rounds := samples[0].(map[string]any)["inspections"].([]any)
	if len(rounds) != 2 {
		t.Fatalf("复检不得覆盖首败，应保留两轮读数，实际 %d 轮", len(rounds))
	}
	first := rounds[0].(map[string]any)
	second := rounds[1].(map[string]any)
	if first["outcome"] != "fail" || second["outcome"] != "pass" {
		t.Fatalf("读数顺序异常: %v %v", first, second)
	}
	if first["inspected_by"] != "李检验" || second["inspected_by"] != "张复验" {
		t.Fatalf("两轮检验签字人应各自保留: %v %v", first["inspected_by"], second["inspected_by"])
	}

	// 拒收处置可以做出。
	code, body = h.post("/v1/receipts/R-BAD/decision", h.users["qa"],
		map[string]any{"target": "rejected", "note": "首检不合格，不予放行"})
	expectCode(t, code, http.StatusOK, body)
}

// 不合格原料经分装、合批向下游传播：直接投料与合批投料都被阻断；
// 投料后被追判不合格的原料阻断其所有成品批放行。
func TestUnqualifiedMaterialBlocksDownstream(t *testing.T) {
	h := newHarness(t)
	setupTwoMaterials(h)
	qualifyReceipt(h, "R-HQ-G", "HQ", 500, 100)
	qualifyReceipt(h, "R-DG-G", "DG", 300, 70)
	dispense(h, "R-HQ-G", map[string]any{"container_ref": "C-HQG", "weight": 500.0})
	dispense(h, "R-DG-G", map[string]any{"container_ref": "C-DGG", "weight": 300.0})

	// 不合格来货：首检失败后拒收（拒收前完成分装，模拟已拆袋的情形）。
	bad := map[string]any{
		"receipt_ref": "R-HQ-B", "material_code": "HQ",
		"supplier": "S2", "supplier_voucher": "V2", "origin": "陕西", "received_weight": 200.0,
	}
	code, body := h.post("/v1/receipts", h.users["warehouse"], bad)
	expectCode(t, code, http.StatusCreated, body)
	code, body = h.post("/v1/receipts/R-HQ-B/samples", h.users["inspector"],
		map[string]any{"sample_ref": "S-HQB"})
	expectCode(t, code, http.StatusCreated, body)
	code, body = h.post("/v1/samples/S-HQB/inspections", h.users["inspector"],
		map[string]any{"measured": 40.0})
	expectCode(t, code, http.StatusCreated, body)
	if body["outcome"] != "fail" {
		t.Fatalf("不合格来货首检应 fail: %v", body)
	}
	dispense(h, "R-HQ-B", map[string]any{"container_ref": "C-HQB", "weight": 200.0})
	code, body = h.post("/v1/receipts/R-HQ-B/decision", h.users["qa"],
		map[string]any{"target": "rejected"})
	expectCode(t, code, http.StatusOK, body)

	// 1) 直接投用不合格容器 → 阻断。
	code, body = h.post("/v1/batches", h.users["lead"], map[string]any{
		"batch_ref": "B-DIRTY", "formula_ref": "F-BUXI", "revision": 1})
	expectCode(t, code, http.StatusCreated, body)
	code, body = h.post("/v1/batches/B-DIRTY/weighings", h.users["lead"],
		map[string]any{"material_code": "HQ", "container_ref": "C-HQB", "weight": 100.0})
	expectCode(t, code, http.StatusUnprocessableEntity, body)
	if body["code"] != "upstream_rejected" {
		t.Fatalf("不合格原料直接投料应被阻断，实际 %v", body["code"])
	}

	// 2) 不合格原料与合格原料合批后，整批合批不得投料。
	code, body = h.post("/v1/merges", h.users["warehouse"], map[string]any{
		"merge_ref": "MG-1", "material_code": "HQ", "output_weight": 200.0,
		"sources": []map[string]any{
			{"container_ref": "C-HQG", "input_weight": 100.0},
			{"container_ref": "C-HQB", "input_weight": 100.0},
		},
	})
	expectCode(t, code, http.StatusCreated, body)
	code, body = h.post("/v1/batches", h.users["lead"], map[string]any{
		"batch_ref": "B-MIX", "formula_ref": "F-BUXI", "revision": 1})
	expectCode(t, code, http.StatusCreated, body)
	code, body = h.post("/v1/batches/B-MIX/weighings", h.users["lead"],
		map[string]any{"material_code": "HQ", "merge_ref": "MG-1", "weight": 100.0})
	expectCode(t, code, http.StatusUnprocessableEntity, body)
	if body["code"] != "upstream_rejected" {
		t.Fatalf("含不合格来源的合批投料应被阻断，实际 %v", body["code"])
	}

	// 3) 用合格原料建成 B-OK，成品检验合格但尚未放行；
	//    此时其黄芪来货被追判不合格，放行必须被阻断，批次标记 blocked。
	buildInspectedBatch(h, "B-OK", "C-HQG", "C-DGG")
	code, body = h.post("/v1/receipts/R-HQ-G/decision", h.users["qa"],
		map[string]any{"target": "rejected", "note": "追加抽检不合格，召回处理"})
	expectCode(t, code, http.StatusOK, body)

	code, body = h.post("/v1/batches/B-OK/release", h.users["qa"],
		map[string]any{"decision": "released"})
	expectCode(t, code, http.StatusUnprocessableEntity, body)
	if body["code"] != "release_blocked" {
		t.Fatalf("全链复核应阻断放行，实际 %v", body["code"])
	}
	code, batch := h.get("/v1/batches/B-OK", h.users["qa"])
	expectCode(t, code, http.StatusOK, batch)
	if batch["status"] != "blocked" {
		t.Fatalf("被阻断批次状态应为 blocked，实际 %v", batch["status"])
	}

	// 影响面：从不合格来货反查到受影响的分装、合批、成品批。
	code, impact := h.get("/v1/receipts/R-HQ-G/impact", h.users["qa"])
	expectCode(t, code, http.StatusOK, impact)
	if !containsString(impact["containers"].([]any), "C-HQG") {
		t.Fatalf("影响面应包含分装 C-HQG: %v", impact)
	}
	if !containsString(impact["merges"].([]any), "MG-1") {
		t.Fatalf("影响面应包含合批 MG-1: %v", impact)
	}
	found := false
	for _, b := range impact["batches"].([]any) {
		bm := b.(map[string]any)
		if bm["batch_ref"] == "B-OK" && bm["blocked"] == true {
			found = true
		}
	}
	if !found {
		t.Fatalf("影响面应列出被阻断成品批 B-OK: %v", impact["batches"])
	}

	// 血缘图标记阻断来货节点。
	code, lineage := h.get("/v1/batches/B-OK/lineage", h.users["qa"])
	expectCode(t, code, http.StatusOK, lineage)
	if !containsString(lineage["blocking_refs"].([]any), "R-HQ-G") {
		t.Fatalf("血缘应标记 R-HQ-G 为阻断节点: %v", lineage["blocking_refs"])
	}
}

// buildInspectedBatch 与 buildReleasedBatch 相同但停在成品检验合格、等待放行。
func buildInspectedBatch(h *harness, batchRef, hqContainer, dgContainer string) {
	t := h.t
	code, body := h.post("/v1/batches", h.users["lead"],
		map[string]any{"batch_ref": batchRef, "formula_ref": "F-BUXI", "revision": 1})
	expectCode(t, code, http.StatusCreated, body)
	code, body = h.post("/v1/batches/"+batchRef+"/weighings", h.users["lead"],
		map[string]any{"material_code": "HQ", "container_ref": hqContainer, "weight": 100.0})
	expectCode(t, code, http.StatusCreated, body)
	code, body = h.post("/v1/batches/"+batchRef+"/weighings", h.users["lead"],
		map[string]any{"material_code": "DG", "container_ref": dgContainer, "weight": 100.0})
	expectCode(t, code, http.StatusCreated, body)
	code, body = h.post("/v1/batches/"+batchRef+"/finish", h.users["lead"],
		map[string]any{"yield_weight": 190.0})
	expectCode(t, code, http.StatusOK, body)
	code, body = h.post("/v1/batches/"+batchRef+"/inspections", h.users["inspector"],
		map[string]any{"outcome": "pass"})
	expectCode(t, code, http.StatusCreated, body)
}

// 职责分离：配制负责人不能代替检验人，也不能自行放行；跨岗位操作被拒。
func TestRoleSeparation(t *testing.T) {
	h := newHarness(t)
	setupTwoMaterials(h)
	qualifyReceipt(h, "R-HQ-1", "HQ", 500, 100)
	qualifyReceipt(h, "R-DG-1", "DG", 300, 70)
	dispense(h, "R-HQ-1", map[string]any{"container_ref": "C-HQ-1", "weight": 500.0})
	dispense(h, "R-DG-1", map[string]any{"container_ref": "C-DG-1", "weight": 300.0})

	// 药库人员不能抽样（检验人职责）。
	code, body := h.post("/v1/receipts/R-HQ-1/samples", h.users["warehouse"],
		map[string]any{"sample_ref": "S-X"})
	expectCode(t, code, http.StatusForbidden, body)

	// 检验人不能登记来货凭证。
	code, body = h.post("/v1/receipts", h.users["inspector"], map[string]any{
		"receipt_ref": "R-X", "material_code": "HQ",
		"supplier": "S", "supplier_voucher": "V", "received_weight": 10.0})
	expectCode(t, code, http.StatusForbidden, body)

	// 药事负责人不能执行分装。
	code, body = h.post("/v1/receipts/R-HQ-1/dispense", h.users["qa"], map[string]any{
		"splits": []map[string]any{{"container_ref": "C-X", "weight": 1.0}}})
	expectCode(t, code, http.StatusForbidden, body)

	buildInspectedBatch(h, "B-R", "C-HQ-1", "C-DG-1")

	// 配制负责人不能给自己的批次做成品检验签字（角色层即拒绝）。
	code, body = h.post("/v1/batches/B-R/inspections", h.users["lead"],
		map[string]any{"outcome": "pass"})
	expectCode(t, code, http.StatusForbidden, body)

	// 检验人角色不能做放行决定。
	code, body = h.post("/v1/batches/B-R/release", h.users["inspector"],
		map[string]any{"decision": "released"})
	expectCode(t, code, http.StatusForbidden, body)

	// 药事负责人即便有权放行，也不能放行自己任配制负责人的批次（本场景由角色隔离天然杜绝）。
	// 未带身份头的内部调用一律 401。
	code, body = h.get("/v1/batches/B-R", "")
	expectCode(t, code, http.StatusUnauthorized, body)
}

// 原料拆分及合批必须保持重量可核对。
func TestWeightConservation(t *testing.T) {
	h := newHarness(t)
	setupTwoMaterials(h)
	qualifyReceipt(h, "R-HQ-1", "HQ", 100, 100)

	// 分装总量超过来货重量 → 拒绝。
	code, body := h.post("/v1/receipts/R-HQ-1/dispense", h.users["warehouse"], map[string]any{
		"splits": []map[string]any{
			{"container_ref": "C-1", "weight": 60.0},
			{"container_ref": "C-2", "weight": 50.0},
		}})
	expectCode(t, code, http.StatusUnprocessableEntity, body)
	if body["code"] != "weight_exceeded" {
		t.Fatalf("超重分装应被拒绝，实际 %v", body["code"])
	}

	// 正确分装：60 + 40 = 100。
	dispense(h, "R-HQ-1",
		map[string]any{"container_ref": "C-1", "weight": 60.0},
		map[string]any{"container_ref": "C-2", "weight": 40.0})

	// 合批产出不等于来源之和 → 拒绝。
	code, body = h.post("/v1/merges", h.users["warehouse"], map[string]any{
		"merge_ref": "MG-BAD", "material_code": "HQ", "output_weight": 99.0,
		"sources": []map[string]any{
			{"container_ref": "C-1", "input_weight": 60.0},
			{"container_ref": "C-2", "input_weight": 40.0},
		}})
	expectCode(t, code, http.StatusUnprocessableEntity, body)
	if body["code"] != "weight_mismatch" {
		t.Fatalf("合批重量不守恒应被拒绝，实际 %v", body["code"])
	}

	// 正确合批：100 = 60 + 40。
	code, body = h.post("/v1/merges", h.users["warehouse"], map[string]any{
		"merge_ref": "MG-OK", "material_code": "HQ", "output_weight": 100.0,
		"sources": []map[string]any{
			{"container_ref": "C-1", "input_weight": 60.0},
			{"container_ref": "C-2", "input_weight": 40.0},
		}})
	expectCode(t, code, http.StatusCreated, body)

	// 已用尽的容器再次取用 → 拒绝。
	code, body = h.post("/v1/merges", h.users["warehouse"], map[string]any{
		"merge_ref": "MG-AGAIN", "material_code": "HQ", "output_weight": 10.0,
		"sources": []map[string]any{{"container_ref": "C-1", "input_weight": 10.0}}})
	expectCode(t, code, http.StatusUnprocessableEntity, body)
	if body["code"] != "weight_exceeded" {
		t.Fatalf("重复超额取用容器应被拒绝，实际 %v", body["code"])
	}

	// 跨药材合批 → 拒绝。
	qualifyReceipt(h, "R-DG-1", "DG", 100, 70)
	dispense(h, "R-DG-1", map[string]any{"container_ref": "C-DG", "weight": 100.0})
	code, body = h.post("/v1/merges", h.users["warehouse"], map[string]any{
		"merge_ref": "MG-X", "material_code": "HQ", "output_weight": 10.0,
		"sources": []map[string]any{{"container_ref": "C-DG", "input_weight": 10.0}}})
	expectCode(t, code, http.StatusUnprocessableEntity, body)
}

// 外部查询只返回合格范围与处置状态，不泄露供应商凭证、产地、读数与签字人。
func TestPublicViewMinimal(t *testing.T) {
	h := newHarness(t)
	setupTwoMaterials(h)
	qualifyReceipt(h, "R-HQ-1", "HQ", 500, 100)

	code, pub := h.get("/public/v1/receipts/R-HQ-1", "")
	expectCode(t, code, http.StatusOK, pub)
	want := map[string]bool{
		"material_code": true, "spec_min": true, "spec_max": true,
		"spec_unit": true, "disposition": true,
	}
	if len(pub) != len(want) {
		t.Fatalf("外部来货响应字段数异常: %v", keys(pub))
	}
	for k := range pub {
		if !want[k] {
			t.Fatalf("外部响应泄露内部字段 %q: %v", k, pub)
		}
	}
	if pub["disposition"] != "released" || pub["spec_min"] != 90.0 || pub["spec_max"] != 110.0 {
		t.Fatalf("外部来货响应内容异常: %v", pub)
	}

	// 外部查询不存在的记录 → 404 且不含任何内部线索。
	code, body := h.get("/public/v1/receipts/NOPE", "")
	expectCode(t, code, http.StatusNotFound, body)
}

// 检验记录在数据库层为追加式：直接尝试 UPDATE/DELETE 也被触发器拒绝。
func TestInspectionAppendOnly(t *testing.T) {
	h := newHarness(t)
	setupTwoMaterials(h)
	qualifyReceipt(h, "R-HQ-1", "HQ", 100, 100)

	if _, err := h.db.Exec(`UPDATE inspections SET measured = 999 WHERE measured = 100`); err == nil {
		t.Fatalf("修改检验读数必须被触发器拒绝")
	}
	if _, err := h.db.Exec(`DELETE FROM inspections`); err == nil {
		t.Fatalf("删除检验记录必须被触发器拒绝")
	}
}

// 数据库层强制：即便绕过应用角色，同一人也不能既当配制负责人又当成品检验签字人。
func TestFinishedInspectionTriggerSeparatesLead(t *testing.T) {
	h := newHarness(t)
	setupTwoMaterials(h)
	qualifyReceipt(h, "R-HQ-1", "HQ", 500, 100)
	qualifyReceipt(h, "R-DG-1", "DG", 300, 70)
	dispense(h, "R-HQ-1", map[string]any{"container_ref": "C-HQ-1", "weight": 500.0})
	dispense(h, "R-DG-1", map[string]any{"container_ref": "C-DG-1", "weight": 300.0})

	code, body := h.post("/v1/batches", h.users["lead"], map[string]any{
		"batch_ref": "B-T", "formula_ref": "F-BUXI", "revision": 1})
	expectCode(t, code, http.StatusCreated, body)

	// 直接以 lead_id 作为 inspector_id 插入，触发器必须拒绝。
	_, err := h.db.Exec(`
		INSERT INTO finished_inspections (id, batch_id, round_no, outcome, measured, inspector_id, inspected_at, note)
		SELECT 'x', b.id, 1, 'pass', '', b.lead_id, '2026-09-20T10:00:00+08:00', ''
		  FROM batches b WHERE b.batch_ref = 'B-T'`)
	if err == nil {
		t.Fatalf("配制负责人自检必须被触发器拒绝")
	}

	// 放行签字人同样不得是配制负责人。
	_, err = h.db.Exec(`
		INSERT INTO release_decisions (id, batch_id, round_no, decision, decided_by, decided_at, note)
		SELECT 'y', b.id, 1, 'released', b.lead_id, '2026-09-20T10:00:00+08:00', ''
		  FROM batches b WHERE b.batch_ref = 'B-T'`)
	if err == nil {
		t.Fatalf("配制负责人自行放行必须被触发器拒绝")
	}
}

// 成品检验任一轮不合格即阻断放行，批次进入 blocked。
func TestFinishedInspectionFailureBlocksRelease(t *testing.T) {
	h := newHarness(t)
	setupTwoMaterials(h)
	qualifyReceipt(h, "R-HQ-1", "HQ", 500, 100)
	qualifyReceipt(h, "R-DG-1", "DG", 300, 70)
	dispense(h, "R-HQ-1", map[string]any{"container_ref": "C-HQ-1", "weight": 500.0})
	dispense(h, "R-DG-1", map[string]any{"container_ref": "C-DG-1", "weight": 300.0})

	code, body := h.post("/v1/batches", h.users["lead"], map[string]any{
		"batch_ref": "B-FAIL", "formula_ref": "F-BUXI", "revision": 1})
	expectCode(t, code, http.StatusCreated, body)
	for _, w := range []map[string]any{
		{"material_code": "HQ", "container_ref": "C-HQ-1", "weight": 100.0},
		{"material_code": "DG", "container_ref": "C-DG-1", "weight": 100.0},
	} {
		code, body = h.post("/v1/batches/B-FAIL/weighings", h.users["lead"], w)
		expectCode(t, code, http.StatusCreated, body)
	}
	code, body = h.post("/v1/batches/B-FAIL/finish", h.users["lead"],
		map[string]any{"yield_weight": 180.0})
	expectCode(t, code, http.StatusOK, body)
	code, body = h.post("/v1/batches/B-FAIL/inspections", h.users["inspector"],
		map[string]any{"outcome": "fail", "measured": "含量测定低于规定"})
	expectCode(t, code, http.StatusCreated, body)

	code, body = h.post("/v1/batches/B-FAIL/release", h.users["qa"],
		map[string]any{"decision": "released"})
	expectCode(t, code, http.StatusUnprocessableEntity, body)
	if body["code"] != "release_blocked" {
		t.Fatalf("成品检验不合格应阻断放行，实际 %v", body["code"])
	}
	code, batch := h.get("/v1/batches/B-FAIL", h.users["qa"])
	expectCode(t, code, http.StatusOK, batch)
	if batch["status"] != "blocked" {
		t.Fatalf("批次应为 blocked，实际 %v", batch["status"])
	}
	// 阻断决定留痕，药事负责人可据此作拒收处置（追加式，不覆盖阻断记录）。
	code, body = h.post("/v1/batches/B-FAIL/release", h.users["qa"],
		map[string]any{"decision": "rejected", "note": "成品含量不合格，整批拒收"})
	expectCode(t, code, http.StatusOK, body)
}

// 投料时全部合格（含合批路径），事后某来货被追判不合格：
// 放行被阻断，影响面经合批递归找到成品批，血缘标记阻断节点。
func TestImpactThroughMergeAfterRetroactiveReject(t *testing.T) {
	h := newHarness(t)
	setupTwoMaterials(h)
	qualifyReceipt(h, "R-HQ-G", "HQ", 500, 100)
	qualifyReceipt(h, "R-DG-G", "DG", 300, 70)
	dispense(h, "R-HQ-G", map[string]any{"container_ref": "C-HQG", "weight": 500.0})
	dispense(h, "R-DG-G", map[string]any{"container_ref": "C-DGG", "weight": 300.0})

	// 合批 100g 黄芪，投料时来源全部放行。
	code, body := h.post("/v1/merges", h.users["warehouse"], map[string]any{
		"merge_ref": "MG-HQ", "material_code": "HQ", "output_weight": 100.0,
		"sources": []map[string]any{{"container_ref": "C-HQG", "input_weight": 100.0}},
	})
	expectCode(t, code, http.StatusCreated, body)

	code, body = h.post("/v1/batches", h.users["lead"], map[string]any{
		"batch_ref": "B-M", "formula_ref": "F-BUXI", "revision": 1})
	expectCode(t, code, http.StatusCreated, body)
	code, body = h.post("/v1/batches/B-M/weighings", h.users["lead"],
		map[string]any{"material_code": "HQ", "merge_ref": "MG-HQ", "weight": 100.0})
	expectCode(t, code, http.StatusCreated, body)
	code, body = h.post("/v1/batches/B-M/weighings", h.users["lead"],
		map[string]any{"material_code": "DG", "container_ref": "C-DGG", "weight": 100.0})
	expectCode(t, code, http.StatusCreated, body)
	code, body = h.post("/v1/batches/B-M/finish", h.users["lead"],
		map[string]any{"yield_weight": 190.0})
	expectCode(t, code, http.StatusOK, body)
	code, body = h.post("/v1/batches/B-M/inspections", h.users["inspector"],
		map[string]any{"outcome": "pass"})
	expectCode(t, code, http.StatusCreated, body)

	// 成品待放行期间，黄芪来货被追判不合格。
	code, body = h.post("/v1/receipts/R-HQ-G/decision", h.users["qa"],
		map[string]any{"target": "rejected", "note": "补充农残检测超标"})
	expectCode(t, code, http.StatusOK, body)

	code, body = h.post("/v1/batches/B-M/release", h.users["qa"],
		map[string]any{"decision": "released"})
	expectCode(t, code, http.StatusUnprocessableEntity, body)

	// 影响面必须沿 来货→分装→合批→称量→成品批 递归找到 B-M。
	code, impact := h.get("/v1/receipts/R-HQ-G/impact", h.users["qa"])
	expectCode(t, code, http.StatusOK, impact)
	if !containsString(impact["merges"].([]any), "MG-HQ") {
		t.Fatalf("影响面应递归包含合批 MG-HQ: %v", impact["merges"])
	}
	found := false
	for _, b := range impact["batches"].([]any) {
		bm := b.(map[string]any)
		if bm["batch_ref"] == "B-M" && bm["blocked"] == true {
			found = true
		}
	}
	if !found {
		t.Fatalf("影响面应经合批递归找到被阻断的 B-M: %v", impact["batches"])
	}

	// 血缘反查同样标出阻断来货。
	code, lineage := h.get("/v1/batches/B-M/lineage", h.users["qa"])
	expectCode(t, code, http.StatusOK, lineage)
	if !containsString(lineage["blocking_refs"].([]any), "R-HQ-G") {
		t.Fatalf("血缘应标记 R-HQ-G: %v", lineage["blocking_refs"])
	}
	kinds := map[string]int{}
	for _, n := range lineage["nodes"].([]any) {
		kinds[n.(map[string]any)["kind"].(string)]++
	}
	if kinds["merge"] != 1 || kinds["receipt"] != 2 {
		t.Fatalf("血缘应包含合批与两个来货节点: %v", kinds)
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func containsString(slice []any, want string) bool {
	for _, v := range slice {
		if s, ok := v.(string); ok && s == want {
			return true
		}
	}
	return false
}
