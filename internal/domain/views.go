package domain

// 各查询接口返回的读模型。

type InspectionRoundView struct {
	RoundNo     int     `json:"round_no"`
	Measured    float64 `json:"measured"`
	Unit        string  `json:"unit"`
	Outcome     string  `json:"outcome"`
	IsRetest    bool    `json:"is_retest"`
	Method      string  `json:"method"`
	InspectedBy string  `json:"inspected_by"`
	InspectedAt string  `json:"inspected_at"`
	Note        string  `json:"note"`
}

type ReceiptQualityView struct {
	ReceiptRef      string              `json:"receipt_ref"`
	MaterialCode    string              `json:"material_code"`
	MaterialName    string              `json:"material_name"`
	Supplier        string              `json:"supplier"`
	SupplierVoucher string              `json:"supplier_voucher"`
	Origin          string              `json:"origin"`
	ReceivedWeight  float64             `json:"received_weight"`
	Disposition     string              `json:"disposition"`
	Samples         []SampleQualityView `json:"samples"`
	Decisions       []DecisionView      `json:"decisions"`
}

type SampleQualityView struct {
	SampleRef   string                `json:"sample_ref"`
	TakenAt     string                `json:"taken_at"`
	TakenBy     string                `json:"taken_by"`
	Inspections []InspectionRoundView `json:"inspections"`
}

type DecisionView struct {
	FromState string `json:"from_state"`
	ToState   string `json:"to_state"`
	DecidedBy string `json:"decided_by"`
	DecidedAt string `json:"decided_at"`
	Note      string `json:"note"`
}

type ContainerView struct {
	ContainerRef string  `json:"container_ref"`
	Weight       float64 `json:"weight"`
}

type DispenseView struct {
	ReceiptRef     string          `json:"receipt_ref"`
	ReceivedWeight float64         `json:"received_weight"`
	DispensedTotal float64         `json:"dispensed_total"`
	Containers     []ContainerView `json:"containers"`
}

type MergeView struct {
	MergeRef     string            `json:"merge_ref"`
	MaterialCode string            `json:"material_code"`
	InputTotal   float64           `json:"input_total"`
	OutputWeight float64           `json:"output_weight"`
	Sources      []MergeSourceView `json:"sources"`
}

type MergeSourceView struct {
	ContainerRef string  `json:"container_ref"`
	InputWeight  float64 `json:"input_weight"`
}

type WeighingView struct {
	MaterialCode string  `json:"material_code"`
	ContainerRef string  `json:"container_ref,omitempty"`
	MergeRef     string  `json:"merge_ref,omitempty"`
	Weight       float64 `json:"weight"`
	WeighedBy    string  `json:"weighed_by"`
	WeighedAt    string  `json:"weighed_at"`
}

type BatchView struct {
	BatchRef       string         `json:"batch_ref"`
	FormulaRef     string         `json:"formula_ref"`
	Revision       int            `json:"revision"`
	Lead           string         `json:"lead"`
	Status         string         `json:"status"`
	BlockedReason  string         `json:"blocked_reason,omitempty"`
	YieldWeight    float64        `json:"yield_weight"`
	Weighings      []WeighingView `json:"weighings"`
	FinishedChecks []CheckView    `json:"finished_checks"`
	Release        *ReleaseView   `json:"release,omitempty"`
}

type CheckView struct {
	RoundNo     int    `json:"round_no"`
	Outcome     string `json:"outcome"`
	Measured    string `json:"measured"`
	Inspector   string `json:"inspector"`
	InspectedAt string `json:"inspected_at"`
	Note        string `json:"note"`
}

type ReleaseView struct {
	Decision  string `json:"decision"`
	DecidedBy string `json:"decided_by"`
	DecidedAt string `json:"decided_at"`
	Note      string `json:"note"`
}

// LineageNode 血缘图中的一个节点（成品批/合批/分装/来货/药材）。
type LineageNode struct {
	Kind        string  `json:"kind"` // batch / merge / container / receipt / material
	Ref         string  `json:"ref"`
	Label       string  `json:"label"`
	Disposition string  `json:"disposition,omitempty"`
	Rejected    bool    `json:"rejected"`
	Blocking    bool    `json:"blocking"` // 该节点在全链上导致阻断
	Weight      float64 `json:"weight,omitempty"`
}

type LineageEdge struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Weight float64 `json:"weight,omitempty"`
}

type LineageView struct {
	BatchRef string        `json:"batch_ref"`
	Nodes    []LineageNode `json:"nodes"`
	Edges    []LineageEdge `json:"edges"`
	// BlockingRefs 全链上所有被判定不合格（来货拒收或成品检验不合格）的节点编号。
	BlockingRefs []string `json:"blocking_refs"`
	Releaseable  bool     `json:"releaseable"`
	// Judgements 沿链发生过的每次质量判断，供药事负责人审阅。
	Judgements []QualityJudgement `json:"judgements"`
}

type QualityJudgement struct {
	Stage      string  `json:"stage"` // raw_inspection / raw_decision / finished_inspection / release
	Ref        string  `json:"ref"`
	RoundNo    int     `json:"round_no,omitempty"`
	Outcome    string  `json:"outcome"`
	Measured   float64 `json:"measured,omitempty"`
	SpecMin    float64 `json:"spec_min,omitempty"`
	SpecMax    float64 `json:"spec_max,omitempty"`
	Actor      string  `json:"actor"`
	OccurredAt string  `json:"occurred_at"`
	Note       string  `json:"note,omitempty"`
}

type ImpactView struct {
	ReceiptRef  string        `json:"receipt_ref"`
	Disposition string        `json:"disposition"`
	Containers  []string      `json:"containers"`
	Merges      []string      `json:"merges"`
	Batches     []ImpactBatch `json:"batches"`
}

type ImpactBatch struct {
	BatchRef string `json:"batch_ref"`
	Status   string `json:"status"`
	Blocked  bool   `json:"blocked"`
}

type WeightCheckView struct {
	Receipts []ReceiptWeightCheck `json:"receipts"`
	Merges   []MergeWeightCheck   `json:"merges"`
	Batches  []BatchWeightCheck   `json:"batches"`
	Balanced bool                 `json:"balanced"`
}

type ReceiptWeightCheck struct {
	ReceiptRef      string  `json:"receipt_ref"`
	ReceivedWeight  float64 `json:"received_weight"`
	DispensedWeight float64 `json:"dispensed_weight"`
	ConsumedWeight  float64 `json:"consumed_weight"`
	BookRemaining   float64 `json:"book_remaining"`
	Balanced        bool    `json:"balanced"`
}

type MergeWeightCheck struct {
	MergeRef     string  `json:"merge_ref"`
	InputWeight  float64 `json:"input_weight"`
	OutputWeight float64 `json:"output_weight"`
	Consumed     float64 `json:"consumed_weight"`
	Balanced     bool    `json:"balanced"`
}

type BatchWeightCheck struct {
	BatchRef    string  `json:"batch_ref"`
	YieldWeight float64 `json:"yield_weight"`
	// StagePastCompounding 表示批次已完成配制；此时实际投料必须与组方用量逐条相等。
	StagePastCompounding bool             `json:"stage_past_compounding"`
	Items                []BatchItemCheck `json:"items"`
	Balanced             bool             `json:"balanced"`
}

type BatchItemCheck struct {
	MaterialCode   string  `json:"material_code"`
	RequiredWeight float64 `json:"required_weight"`
	ActualWeight   float64 `json:"actual_weight"`
}

// PublicReceiptView 外部查询的最小响应：只含合格范围与处置状态，
// 不含供应商凭证、产地、检验读数、签字人等内部信息。
type PublicReceiptView struct {
	MaterialCode string  `json:"material_code"`
	SpecMin      float64 `json:"spec_min"`
	SpecMax      float64 `json:"spec_max"`
	SpecUnit     string  `json:"spec_unit"`
	Disposition  string  `json:"disposition"`
}

// PublicBatchView 外部成品查询的最小响应：只给放行处置状态。
type PublicBatchView struct {
	BatchRef string `json:"batch_ref"`
	Status   string `json:"status"`
	Release  string `json:"release"` // 最新放行决定；无则空
}
