package domain

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"
)

// GetReceiptQuality 返回来货批次的完整质量档案：供应商凭证、抽样、每轮检验与每次处置。
func (s *Service) GetReceiptQuality(ctx context.Context, callerID, receiptRef string) (*ReceiptQualityView, error) {
	var view *ReceiptQualityView
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.authenticate(ctx, tx, callerID,
			RoleInspector, RoleQARelease, RoleWarehouse, RoleCompoundingLead); err != nil {
			return err
		}
		var receiptID string
		v := &ReceiptQualityView{ReceiptRef: receiptRef, Samples: []SampleQualityView{}, Decisions: []DecisionView{}}
		err := tx.QueryRowContext(ctx, `
			SELECT r.id, m.code, m.name, r.supplier, r.supplier_voucher, r.origin,
			       r.received_weight, r.disposition
			  FROM receipts r JOIN materials m ON m.id = r.material_id
			 WHERE r.receipt_ref = ?`, receiptRef).
			Scan(&receiptID, &v.MaterialCode, &v.MaterialName, &v.Supplier,
				&v.SupplierVoucher, &v.Origin, &v.ReceivedWeight, &v.Disposition)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(404, "receipt_not_found", "来货批次不存在")
		}
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT s.id, s.sample_ref, s.taken_at, u.name
			   FROM samples s JOIN users u ON u.id = s.taken_by
			  WHERE s.receipt_id = ? ORDER BY s.taken_at`, receiptID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var sampleID, ref, takenAt, takenBy string
			if err := rows.Scan(&sampleID, &ref, &takenAt, &takenBy); err != nil {
				return err
			}
			rounds, err := loadInspectionRounds(ctx, tx, sampleID)
			if err != nil {
				return err
			}
			v.Samples = append(v.Samples, SampleQualityView{
				SampleRef: ref, TakenAt: takenAt, TakenBy: takenBy, Inspections: rounds})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		decisions, err := loadDecisions(ctx, tx, receiptID)
		if err != nil {
			return err
		}
		v.Decisions = decisions
		view = v
		return nil
	})
	return view, err
}

func loadInspectionRounds(ctx context.Context, tx *sql.Tx, sampleID string) ([]InspectionRoundView, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT i.round_no, i.measured, i.unit, i.outcome, i.is_retest, i.method,
		       u.name, i.inspected_at, i.note
		  FROM inspections i JOIN users u ON u.id = i.inspected_by
		 WHERE i.sample_id = ? ORDER BY i.round_no`, sampleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rounds []InspectionRoundView
	for rows.Next() {
		var rv InspectionRoundView
		var retest int
		if err := rows.Scan(&rv.RoundNo, &rv.Measured, &rv.Unit, &rv.Outcome,
			&retest, &rv.Method, &rv.InspectedBy, &rv.InspectedAt, &rv.Note); err != nil {
			return nil, err
		}
		rv.IsRetest = retest == 1
		rounds = append(rounds, rv)
	}
	return rounds, rows.Err()
}

func loadDecisions(ctx context.Context, tx *sql.Tx, receiptID string) ([]DecisionView, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT d.from_state, d.to_state, u.name, d.decided_at, d.note
		  FROM receipt_decisions d JOIN users u ON u.id = d.decided_by
		 WHERE d.receipt_id = ? ORDER BY d.decided_at`, receiptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var decisions []DecisionView
	for rows.Next() {
		var d DecisionView
		if err := rows.Scan(&d.FromState, &d.ToState, &d.DecidedBy, &d.DecidedAt, &d.Note); err != nil {
			return nil, err
		}
		decisions = append(decisions, d)
	}
	return decisions, rows.Err()
}

// GetBatch 返回成品批次详情，含每条称量、成品检验轮次与最新放行决定。
func (s *Service) GetBatch(ctx context.Context, batchRef string) (*BatchView, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var batchID, formulaRef, lead, status, blockedReason string
	var revision int
	var yield float64
	err = tx.QueryRowContext(ctx, `
		SELECT b.id, f.formula_ref, f.revision, u.name, b.status, b.blocked_reason, b.yield_weight
		  FROM batches b JOIN formulas f ON f.id = b.formula_id JOIN users u ON u.id = b.lead_id
		 WHERE b.batch_ref = ?`, batchRef).
		Scan(&batchID, &formulaRef, &revision, &lead, &status, &blockedReason, &yield)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fail(404, "batch_not_found", "配制批次不存在")
	}
	if err != nil {
		return nil, err
	}
	v := &BatchView{BatchRef: batchRef, FormulaRef: formulaRef, Revision: revision, Lead: lead,
		Status: status, BlockedReason: blockedReason, YieldWeight: yield,
		Weighings: []WeighingView{}, FinishedChecks: []CheckView{}}

	rows, err := tx.QueryContext(ctx, `
		SELECT m.code, w.weight, u.name, w.weighed_at,
		       COALESCE(c.container_ref, ''), COALESCE(mg.merge_ref, '')
		  FROM weighings w
		  JOIN materials m ON m.id = w.material_id
		  JOIN users u ON u.id = w.weighed_by
		  LEFT JOIN containers c ON c.id = w.container_id
		  LEFT JOIN merges mg ON mg.id = w.merge_id
		 WHERE w.batch_id = ? ORDER BY w.weighed_at`, batchID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var wv WeighingView
		if err := rows.Scan(&wv.MaterialCode, &wv.Weight, &wv.WeighedBy, &wv.WeighedAt,
			&wv.ContainerRef, &wv.MergeRef); err != nil {
			rows.Close()
			return nil, err
		}
		v.Weighings = append(v.Weighings, wv)
	}
	rows.Close()

	checks, err := loadFinishedChecks(ctx, tx, batchID)
	if err != nil {
		return nil, err
	}
	v.FinishedChecks = checks

	release, err := latestRelease(ctx, tx, batchID)
	if err != nil {
		return nil, err
	}
	v.Release = release
	return v, nil
}

func loadFinishedChecks(ctx context.Context, tx *sql.Tx, batchID string) ([]CheckView, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT fi.round_no, fi.outcome, fi.measured, u.name, fi.inspected_at, fi.note
		  FROM finished_inspections fi JOIN users u ON u.id = fi.inspector_id
		 WHERE fi.batch_id = ? ORDER BY fi.round_no`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var checks []CheckView
	for rows.Next() {
		var c CheckView
		if err := rows.Scan(&c.RoundNo, &c.Outcome, &c.Measured, &c.Inspector,
			&c.InspectedAt, &c.Note); err != nil {
			return nil, err
		}
		checks = append(checks, c)
	}
	return checks, rows.Err()
}

func latestRelease(ctx context.Context, tx *sql.Tx, batchID string) (*ReleaseView, error) {
	var rv ReleaseView
	err := tx.QueryRowContext(ctx, `
		SELECT rd.decision, u.name, rd.decided_at, rd.note
		  FROM release_decisions rd JOIN users u ON u.id = rd.decided_by
		 WHERE rd.batch_id = ? AND rd.round_no =
		       (SELECT MAX(round_no) FROM release_decisions WHERE batch_id = ?)`,
		batchID, batchID).Scan(&rv.Decision, &rv.DecidedBy, &rv.DecidedAt, &rv.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rv, nil
}

// TraceLineage 从任一成品批反查：所用合批/分装/来货/药材节点，以及链上每次质量判断。
func (s *Service) TraceLineage(ctx context.Context, callerID, batchRef string) (*LineageView, error) {
	var view *LineageView
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.authenticate(ctx, tx, callerID, RoleQARelease, RoleInspector); err != nil {
			return err
		}
		var batchID, formulaRef string
		var revision int
		var batchStatus string
		err := tx.QueryRowContext(ctx, `
			SELECT b.id, f.formula_ref, f.revision, b.status
			  FROM batches b JOIN formulas f ON f.id = b.formula_id
			 WHERE b.batch_ref = ?`, batchRef).
			Scan(&batchID, &formulaRef, &revision, &batchStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(404, "batch_not_found", "配制批次不存在")
		}
		if err != nil {
			return err
		}

		lg := &LineageView{BatchRef: batchRef, Nodes: []LineageNode{}, Edges: []LineageEdge{},
			BlockingRefs: []string{}, Judgements: []QualityJudgement{}}
		node := func(kind, ref string) string { return kind + ":" + ref }
		addNode := func(n LineageNode) {
			key := node(n.Kind, n.Ref)
			for _, existing := range lg.Nodes {
				if node(existing.Kind, existing.Ref) == key {
					return
				}
			}
			lg.Nodes = append(lg.Nodes, n)
		}
		addNode(LineageNode{Kind: "batch", Ref: batchRef, Label: formulaRef + " 修订" + strconv.Itoa(revision),
			Disposition: batchStatus})
		addEdge := func(fromKind, fromRef, toKind, toRef string, weight float64) {
			lg.Edges = append(lg.Edges, LineageEdge{
				From: node(fromKind, fromRef), To: node(toKind, toRef), Weight: weight})
		}

		// 已访问的合批，避免环/重复。
		visitedMerges := map[string]bool{}
		receiptSet := map[string]bool{}

		// walkContainer 与 walkMerge 互相递归，需先声明再赋值。
		var walkContainer func(containerRef string, weight float64, downstreamKind, downstreamRef string) error

		// 第一层：批次的称量投料行。
		rows, err := tx.QueryContext(ctx, `
			SELECT mat.code, w.weight,
			       COALESCE(c.container_ref, ''), COALESCE(mg.merge_ref, ''),
			       COALESCE(mg.id, '')
			  FROM weighings w
			  JOIN materials mat ON mat.id = w.material_id
			  LEFT JOIN containers c ON c.id = w.container_id
			  LEFT JOIN merges mg ON mg.id = w.merge_id
			 WHERE w.batch_id = ?`, batchID)
		if err != nil {
			return err
		}
		type firstHop struct {
			materialCode string
			weight       float64
			containerRef string
			mergeRef     string
			mergeID      string
		}
		var hops []firstHop
		for rows.Next() {
			var h firstHop
			if err := rows.Scan(&h.materialCode, &h.weight, &h.containerRef, &h.mergeRef, &h.mergeID); err != nil {
				rows.Close()
				return err
			}
			hops = append(hops, h)
		}
		rows.Close()

		var walkMerge func(mergeID, mergeRef string, weight float64, downstreamKind, downstreamRef string) error
		walkMerge = func(mergeID, mergeRef string, weight float64, downstreamKind, downstreamRef string) error {
			addNode(LineageNode{Kind: "merge", Ref: mergeRef, Label: "合批 " + mergeRef, Weight: weight})
			addEdge("merge", mergeRef, downstreamKind, downstreamRef, weight)
			if visitedMerges[mergeID] {
				return nil
			}
			visitedMerges[mergeID] = true
			srows, err := tx.QueryContext(ctx, `
				SELECT c.container_ref, ms.input_weight
				  FROM merge_sources ms JOIN containers c ON c.id = ms.container_id
				 WHERE ms.merge_id = ?`, mergeID)
			if err != nil {
				return err
			}
			defer srows.Close()
			for srows.Next() {
				var containerRef string
				var input float64
				if err := srows.Scan(&containerRef, &input); err != nil {
					return err
				}
				if err := walkContainer(containerRef, input, "merge", mergeRef); err != nil {
					return err
				}
			}
			return srows.Err()
		}

		walkContainer = func(containerRef string, weight float64, downstreamKind, downstreamRef string) error {
			var cWeight float64
			var receiptRef, disposition, materialCode, materialName string
			err := tx.QueryRowContext(ctx, `
				SELECT c.weight, r.receipt_ref, r.disposition, m.code, m.name
				  FROM containers c
				  JOIN receipts r ON r.id = c.receipt_id
				  JOIN materials m ON m.id = r.material_id
				 WHERE c.container_ref = ?`, containerRef).
				Scan(&cWeight, &receiptRef, &disposition, &materialCode, &materialName)
			if errors.Is(err, sql.ErrNoRows) {
				return fail(404, "container_not_found", "分装容器不存在: "+containerRef)
			}
			if err != nil {
				return err
			}
			addNode(LineageNode{Kind: "container", Ref: containerRef, Label: "分装 " + containerRef,
				Weight: cWeight})
			addEdge("container", containerRef, downstreamKind, downstreamRef, weight)
			if !receiptSet[receiptRef] {
				receiptSet[receiptRef] = true
				addNode(LineageNode{Kind: "receipt", Ref: receiptRef, Label: "来货 " + receiptRef,
					Disposition: disposition, Rejected: disposition == "rejected",
					Blocking: disposition != "released"})
				addNode(LineageNode{Kind: "material", Ref: materialCode, Label: materialName})
				addEdge("material", materialCode, "receipt", receiptRef, 0)
			}
			addEdge("receipt", receiptRef, "container", containerRef, weight)
			if disposition != "released" {
				lg.BlockingRefs = appendUnique(lg.BlockingRefs, receiptRef)
			}
			return nil
		}

		for _, h := range hops {
			if h.mergeRef != "" {
				if err := walkMerge(h.mergeID, h.mergeRef, h.weight, "batch", batchRef); err != nil {
					return err
				}
			} else {
				// walkContainer 内部会添加 container→batch 边。
				if err := walkContainer(h.containerRef, h.weight, "batch", batchRef); err != nil {
					return err
				}
			}
		}

		// 成品检验若有失败轮，同样阻断。
		var failRounds int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM finished_inspections WHERE batch_id = ? AND outcome = 'fail'`,
			batchID).Scan(&failRounds); err != nil {
			return err
		}
		if failRounds > 0 {
			lg.BlockingRefs = appendUnique(lg.BlockingRefs, "成品检验不合格("+batchRef+")")
		}
		// 汇总链上每次质量判断。
		judgements, err := loadLineageJudgements(ctx, tx, appendKeys(receiptSet), batchID)
		if err != nil {
			return err
		}
		lg.Judgements = judgements
		lg.Releaseable = len(lg.BlockingRefs) == 0 && batchStatus == "inspected"
		if batchStatus == "released" {
			lg.Releaseable = len(lg.BlockingRefs) == 0
		}
		sort.SliceStable(lg.Nodes, func(i, j int) bool {
			if lg.Nodes[i].Kind != lg.Nodes[j].Kind {
				return lg.Nodes[i].Kind < lg.Nodes[j].Kind
			}
			return lg.Nodes[i].Ref < lg.Nodes[j].Ref
		})
		view = lg
		return nil
	})
	return view, err
}

func appendUnique(slice []string, v string) []string {
	for _, existing := range slice {
		if existing == v {
			return slice
		}
	}
	return append(slice, v)
}

func appendKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// loadLineageJudgements 汇总血缘上的原料检验、处置、成品检验与放行决定。
func loadLineageJudgements(ctx context.Context, tx *sql.Tx, receiptRefs []string, batchID string) ([]QualityJudgement, error) {
	judgements := []QualityJudgement{}
	for _, ref := range receiptRefs {
		rows, err := tx.QueryContext(ctx, `
			SELECT i.round_no, i.outcome, i.measured, m.spec_min, m.spec_max,
			       u.name, i.inspected_at, i.note, s.sample_ref
			  FROM receipts r
			  JOIN materials m ON m.id = r.material_id
			  JOIN samples s ON s.receipt_id = r.id
			  JOIN inspections i ON i.sample_id = s.id
			  JOIN users u ON u.id = i.inspected_by
			 WHERE r.receipt_ref = ?
			 ORDER BY i.inspected_at`, ref)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var j QualityJudgement
			var sampleRef string
			if err := rows.Scan(&j.RoundNo, &j.Outcome, &j.Measured, &j.SpecMin, &j.SpecMax,
				&j.Actor, &j.OccurredAt, &j.Note, &sampleRef); err != nil {
				rows.Close()
				return nil, err
			}
			j.Stage, j.Ref = "raw_inspection", ref+"/"+sampleRef
			judgements = append(judgements, j)
		}
		rows.Close()

		drows, err := tx.QueryContext(ctx, `
			SELECT d.to_state, u.name, d.decided_at, d.note
			  FROM receipt_decisions d
			  JOIN receipts r ON r.id = d.receipt_id
			  JOIN users u ON u.id = d.decided_by
			 WHERE r.receipt_ref = ? ORDER BY d.decided_at`, ref)
		if err != nil {
			return nil, err
		}
		for drows.Next() {
			var j QualityJudgement
			if err := drows.Scan(&j.Outcome, &j.Actor, &j.OccurredAt, &j.Note); err != nil {
				drows.Close()
				return nil, err
			}
			j.Stage, j.Ref = "raw_decision", ref
			judgements = append(judgements, j)
		}
		drows.Close()
	}

	frows, err := tx.QueryContext(ctx, `
		SELECT fi.round_no, fi.outcome, u.name, fi.inspected_at, fi.note
		  FROM finished_inspections fi JOIN users u ON u.id = fi.inspector_id
		 WHERE fi.batch_id = ? ORDER BY fi.round_no`, batchID)
	if err != nil {
		return nil, err
	}
	for frows.Next() {
		var j QualityJudgement
		if err := frows.Scan(&j.RoundNo, &j.Outcome, &j.Actor, &j.OccurredAt, &j.Note); err != nil {
			frows.Close()
			return nil, err
		}
		j.Stage = "finished_inspection"
		judgements = append(judgements, j)
	}
	frows.Close()

	rrows, err := tx.QueryContext(ctx, `
		SELECT rd.round_no, rd.decision, u.name, rd.decided_at, rd.note
		  FROM release_decisions rd JOIN users u ON u.id = rd.decided_by
		 WHERE rd.batch_id = ? ORDER BY rd.round_no`, batchID)
	if err != nil {
		return nil, err
	}
	for rrows.Next() {
		var j QualityJudgement
		if err := rrows.Scan(&j.RoundNo, &j.Outcome, &j.Actor, &j.OccurredAt, &j.Note); err != nil {
			rrows.Close()
			return nil, err
		}
		j.Stage = "release"
		judgements = append(judgements, j)
	}
	rrows.Close()

	sort.SliceStable(judgements, func(i, j int) bool {
		if judgements[i].OccurredAt != judgements[j].OccurredAt {
			return judgements[i].OccurredAt < judgements[j].OccurredAt
		}
		return judgements[i].Stage < judgements[j].Stage
	})
	return judgements, nil
}

// Impact 查询一袋不合格原料的全部下游：分装、合批以及配制批次（含阻断状态）。
func (s *Service) Impact(ctx context.Context, callerID, receiptRef string) (*ImpactView, error) {
	var view *ImpactView
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.authenticate(ctx, tx, callerID, RoleQARelease, RoleInspector, RoleWarehouse); err != nil {
			return err
		}
		var receiptID, disposition string
		err := tx.QueryRowContext(ctx, `SELECT id, disposition FROM receipts WHERE receipt_ref = ?`, receiptRef).
			Scan(&receiptID, &disposition)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(404, "receipt_not_found", "来货批次不存在")
		}
		if err != nil {
			return err
		}
		v := &ImpactView{ReceiptRef: receiptRef, Disposition: disposition,
			Containers: []string{}, Merges: []string{}, Batches: []ImpactBatch{}}

		rows, err := tx.QueryContext(ctx,
			`SELECT container_ref FROM containers WHERE receipt_id = ? ORDER BY container_ref`, receiptID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var ref string
			if err := rows.Scan(&ref); err != nil {
				rows.Close()
				return err
			}
			v.Containers = append(v.Containers, ref)
		}
		rows.Close()

		mrows, err := tx.QueryContext(ctx, `
			SELECT DISTINCT m.merge_ref
			  FROM merge_sources ms
			  JOIN containers c ON c.id = ms.container_id
			  JOIN merges m ON m.id = ms.merge_id
			 WHERE c.receipt_id = ? ORDER BY m.merge_ref`, receiptID)
		if err != nil {
			return err
		}
		for mrows.Next() {
			var ref string
			if err := mrows.Scan(&ref); err != nil {
				mrows.Close()
				return err
			}
			v.Merges = append(v.Merges, ref)
		}
		mrows.Close()

		brows, err := tx.QueryContext(ctx, `
			WITH RECURSIVE reach(merge_id) AS (
				SELECT ms.merge_id FROM merge_sources ms
				  JOIN containers c ON c.id = ms.container_id
				 WHERE c.receipt_id = ?
			)
			SELECT DISTINCT b.batch_ref, b.status
			  FROM weighings w
			  JOIN batches b ON b.id = w.batch_id
			 WHERE w.container_id IN (SELECT id FROM containers WHERE receipt_id = ?)
			    OR w.merge_id IN (SELECT merge_id FROM reach)
			 ORDER BY b.batch_ref`, receiptID, receiptID)
		if err != nil {
			return err
		}
		for brows.Next() {
			var ib ImpactBatch
			if err := brows.Scan(&ib.BatchRef, &ib.Status); err != nil {
				brows.Close()
				return err
			}
			ib.Blocked = ib.Status == "blocked" || disposition == "rejected"
			v.Batches = append(v.Batches, ib)
		}
		brows.Close()
		view = v
		return nil
	})
	return view, err
}

// WeightCheck 全库重量核对：来货分装、合批守恒、成品批组方用量逐条核对。
func (s *Service) WeightCheck(ctx context.Context, callerID string) (*WeightCheckView, error) {
	var view *WeightCheckView
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.authenticate(ctx, tx, callerID, RoleQARelease, RoleWarehouse); err != nil {
			return err
		}
		out := &WeightCheckView{Receipts: []ReceiptWeightCheck{}, Merges: []MergeWeightCheck{},
			Batches: []BatchWeightCheck{}}
		balanced := true

		rrows, err := tx.QueryContext(ctx, `
			SELECT r.receipt_ref, r.received_weight,
			       COALESCE((SELECT SUM(c.weight) FROM containers c WHERE c.receipt_id = r.id), 0),
			       COALESCE((
			           SELECT SUM(w.weight) FROM weighings w
			            WHERE w.container_id IN (SELECT id FROM containers WHERE receipt_id = r.id)
			       ), 0) +
			       COALESCE((
			           SELECT SUM(ms.input_weight) FROM merge_sources ms
			            WHERE ms.container_id IN (SELECT id FROM containers WHERE receipt_id = r.id)
			       ), 0)
			  FROM receipts r ORDER BY r.receipt_ref`)
		if err != nil {
			return err
		}
		for rrows.Next() {
			var rc ReceiptWeightCheck
			if err := rrows.Scan(&rc.ReceiptRef, &rc.ReceivedWeight,
				&rc.DispensedWeight, &rc.ConsumedWeight); err != nil {
				rrows.Close()
				return err
			}
			rc.BookRemaining = rc.ReceivedWeight - rc.DispensedWeight
			rc.Balanced = rc.DispensedWeight <= rc.ReceivedWeight+weightEps &&
				rc.ConsumedWeight <= rc.DispensedWeight+weightEps
			balanced = balanced && rc.Balanced
			out.Receipts = append(out.Receipts, rc)
		}
		rrows.Close()

		mrows, err := tx.QueryContext(ctx, `
			SELECT m.merge_ref,
			       COALESCE((SELECT SUM(ms.input_weight) FROM merge_sources ms WHERE ms.merge_id = m.id), 0),
			       m.output_weight,
			       COALESCE((SELECT SUM(w.weight) FROM weighings w WHERE w.merge_id = m.id), 0)
			  FROM merges m ORDER BY m.merge_ref`)
		if err != nil {
			return err
		}
		for mrows.Next() {
			var mc MergeWeightCheck
			if err := mrows.Scan(&mc.MergeRef, &mc.InputWeight, &mc.OutputWeight, &mc.Consumed); err != nil {
				mrows.Close()
				return err
			}
			mc.Balanced = weightsEqual(mc.InputWeight, mc.OutputWeight) &&
				mc.Consumed <= mc.OutputWeight+weightEps
			balanced = balanced && mc.Balanced
			out.Merges = append(out.Merges, mc)
		}
		mrows.Close()

		brows, err := tx.QueryContext(ctx, `
			SELECT b.batch_ref, b.yield_weight, b.status, m.code, fi.required_weight,
			       COALESCE((SELECT SUM(w.weight) FROM weighings w
			                  WHERE w.batch_id = b.id AND w.material_id = m.id), 0)
			  FROM batches b
			  JOIN formula_items fi ON fi.formula_id = b.formula_id
			  JOIN materials m ON m.id = fi.material_id
			 ORDER BY b.batch_ref, fi.ordinal`)
		if err != nil {
			return err
		}
		currentRef := ""
		var bc *BatchWeightCheck
		flush := func() {
			if bc != nil {
				bc.Balanced = true
				// 已完成配制（及之后状态）的批次才要求实际投料与组方用量逐条一致；
				// 计划/投料中的批次只列事实，不把整张核对单判为不平衡。
				if bc.StagePastCompounding {
					for _, item := range bc.Items {
						if !weightsEqual(item.ActualWeight, item.RequiredWeight) {
							bc.Balanced = false
						}
					}
					balanced = balanced && bc.Balanced
				}
				out.Batches = append(out.Batches, *bc)
			}
		}
		for brows.Next() {
			var ref, code, status string
			var yield, required, actual float64
			if err := brows.Scan(&ref, &yield, &status, &code, &required, &actual); err != nil {
				brows.Close()
				return err
			}
			if ref != currentRef {
				flush()
				bc = &BatchWeightCheck{BatchRef: ref, YieldWeight: yield, Items: []BatchItemCheck{}}
				bc.StagePastCompounding = status == "compounded" || status == "inspected" ||
					status == "released" || status == "blocked"
				currentRef = ref
			}
			bc.Items = append(bc.Items, BatchItemCheck{
				MaterialCode: code, RequiredWeight: required, ActualWeight: actual})
		}
		brows.Close()
		flush()

		out.Balanced = balanced
		view = out
		return nil
	})
	return view, err
}

// PublicReceipt 外部查询：只返回合格范围与处置状态。
func (s *Service) PublicReceipt(ctx context.Context, receiptRef string) (*PublicReceiptView, error) {
	var v PublicReceiptView
	err := s.db.QueryRowContext(ctx, `
		SELECT m.code, m.spec_min, m.spec_max, m.spec_unit, r.disposition
		  FROM receipts r JOIN materials m ON m.id = r.material_id
		 WHERE r.receipt_ref = ?`, receiptRef).
		Scan(&v.MaterialCode, &v.SpecMin, &v.SpecMax, &v.SpecUnit, &v.Disposition)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fail(404, "not_found", "记录不存在")
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// PublicBatch 外部成品查询：只返回批号与最新处置状态。
func (s *Service) PublicBatch(ctx context.Context, batchRef string) (*PublicBatchView, error) {
	var v PublicBatchView
	err := s.db.QueryRowContext(ctx, `
		SELECT b.batch_ref, b.status,
		       COALESCE((SELECT rd.decision FROM release_decisions rd
		                  WHERE rd.batch_id = b.id
		                  ORDER BY rd.round_no DESC LIMIT 1), '')
		  FROM batches b WHERE b.batch_ref = ?`, batchRef).
		Scan(&v.BatchRef, &v.Status, &v.Release)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fail(404, "not_found", "记录不存在")
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// GetBatchForUser 带角色鉴权的批次详情查询，供内部接口使用。
func (s *Service) GetBatchForUser(ctx context.Context, callerID, batchRef string) (*BatchView, error) {
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := s.authenticate(ctx, tx, callerID,
			RoleQARelease, RoleInspector, RoleWarehouse, RoleCompoundingLead)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetBatch(ctx, batchRef)
}
