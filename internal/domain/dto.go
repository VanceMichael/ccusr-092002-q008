package domain

// 用户与主数据的请求/响应结构。字段顺序即接口示例顺序。

type CreateUserInput struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type CreateMaterialInput struct {
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	SpecMin  float64 `json:"spec_min"`
	SpecMax  float64 `json:"spec_max"`
	SpecUnit string  `json:"spec_unit"`
}

type MaterialView struct {
	ID       string  `json:"id"`
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	SpecMin  float64 `json:"spec_min"`
	SpecMax  float64 `json:"spec_max"`
	SpecUnit string  `json:"spec_unit"`
}

type CreateFormulaInput struct {
	FormulaRef   string             `json:"formula_ref"`
	Revision     int                `json:"revision"`
	Name         string             `json:"name"`
	LicenseScope string             `json:"license_scope"`
	Items        []FormulaItemInput `json:"items"`
}

type FormulaItemInput struct {
	MaterialCode   string  `json:"material_code"`
	RequiredWeight float64 `json:"required_weight"`
}

type FormulaView struct {
	ID           string            `json:"id"`
	FormulaRef   string            `json:"formula_ref"`
	Revision     int               `json:"revision"`
	Name         string            `json:"name"`
	LicenseScope string            `json:"license_scope"`
	Items        []FormulaItemView `json:"items"`
}

type FormulaItemView struct {
	MaterialCode   string  `json:"material_code"`
	MaterialName   string  `json:"material_name"`
	RequiredWeight float64 `json:"required_weight"`
}

// ReceiptInput 来货登记：供应商凭证、产地、来货重量。
type ReceiptInput struct {
	ReceiptRef      string  `json:"receipt_ref"`
	MaterialCode    string  `json:"material_code"`
	Supplier        string  `json:"supplier"`
	SupplierVoucher string  `json:"supplier_voucher"`
	Origin          string  `json:"origin"`
	ReceivedWeight  float64 `json:"received_weight"`
	ReceivedAt      string  `json:"received_at"`
}

type SampleInput struct {
	SampleRef string `json:"sample_ref"`
	Note      string `json:"note"`
}

type InspectionInput struct {
	Measured float64 `json:"measured"`
	Method   string  `json:"method"`
	Note     string  `json:"note"`
}

type DispenseInput struct {
	Splits []DispenseSplit `json:"splits"`
}

type DispenseSplit struct {
	ContainerRef string  `json:"container_ref"`
	Weight       float64 `json:"weight"`
}

type MergeInput struct {
	MergeRef     string             `json:"merge_ref"`
	MaterialCode string             `json:"material_code"`
	Sources      []MergeSourceInput `json:"sources"`
	OutputWeight float64            `json:"output_weight"`
	Note         string             `json:"note"`
}

type MergeSourceInput struct {
	ContainerRef string  `json:"container_ref"`
	InputWeight  float64 `json:"input_weight"`
}

type BatchInput struct {
	BatchRef   string `json:"batch_ref"`
	FormulaRef string `json:"formula_ref"`
	Revision   int    `json:"revision"`
}

type WeighingInput struct {
	MaterialCode string  `json:"material_code"`
	ContainerRef string  `json:"container_ref"` // 与 merge_ref 二选一
	MergeRef     string  `json:"merge_ref"`
	Weight       float64 `json:"weight"`
}

type FinishBatchInput struct {
	YieldWeight float64 `json:"yield_weight"`
}

type FinishedInspectionInput struct {
	Outcome  string `json:"outcome"` // pass / fail
	Measured string `json:"measured"`
	Note     string `json:"note"`
}

type ReleaseInput struct {
	Decision string `json:"decision"` // released / rejected / withheld
	Note     string `json:"note"`
}
