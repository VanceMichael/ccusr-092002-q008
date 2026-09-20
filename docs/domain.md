# 领域资料

院内方剂有历史组方版本；临床资料、配制批次及质量检验由不同部门管理，原始患者资料不能混入公开资料。

本服务覆盖"原料到成品的质量放行"链路：入厂批次（供应商凭证）→ 抽样与检验（含复检）→ 分装/合批 → 称量 → 配制投料 → 成品检验 → 药事负责人放行，以及从任一成品反查全部所用药材与每一次质量判断。

## 角色与签字分离

| 角色 | 职责 | 明确禁止 |
| --- | --- | --- |
| `warehouse` 仓储 | 收货、凭证登记、分装、合批、称量、抽样、不合格处置 | 不得录入检验读数、不得签检验结论、不得放行 |
| `inspector` 检验人 | 开检验单、录入读数、签字结论、复检 | 不得放行成品 |
| `production_manager` 配制负责人 | 开立配制批次、确认投料 | 不得代替检验人开单/读数/签结论，不得放行 |
| `pharmacy_head` 药事负责人 | 放行/暂缓/拒收、查看完整谱系、不合格处置 | 不得签检验结论 |

角色由每个请求体中的操作人 ID 携带（`receiver_id`/`inspector_id`/`manager_id`/`head_id` 等），服务端按员工表角色校验，越权返回 `403 role_forbidden`。

## 重量可核对

所有重量以 **整数毫克** 存储，杜绝浮点误差。每个物料批次有逐笔台账（`lot_movements` + `lot_movement_items`），动作类型：

- `receipt` 入厂收货入账
- `sample` 抽样耗料
- `split` 分装（一个出、多个入）
- `merge` 合批（多个出、一个入）
- `weigh` 称量领出

分装时各子批次重量之和必须等于领出重量；合批时新批次重量必须等于各来源领出之和；任何动作后"入量合计 − 出量合计"不得为负。违反返回 `409 weight_mismatch`，事务整体回滚。

## 检验读数与复检

- 读数逐笔追加，序号递增。`test_readings` 上有数据库触发器，**直接 UPDATE/DELETE 也会失败**。
- 录入读数时按 `spec_min`/`spec_max`（含边界）自动判定 pass/fail，实测值与范围都留痕。
- 出现 fail 读数时，对应物料批次及其分装/合批后代**立即**挂起，无需等待结论。
- 检验单只能结论一次；有任一 fail 读数只能判 `unqualified`。结论不可重开或改写。
- 复检是**新抽样（`kind=retest` 回溯原抽样）+ 新检验单**，原单和第一次失败读数原样保留。
- 批次 `had_failure` 是永久事实：即使复检合格也不会自动解锁放行；需要药事负责人按处置流程处理。

## 不合格阻断

任一物料批次被判不合格（或其谱系上游被判不合格）：

1. 该批次及经 split/merge 派生出的全部物料批次置 `had_failure=1` 并挂起；
2. 已使用这些物料的配制批次置 `quality_blocked=1`；
3. 已放行的成品批次状态转 `recalled`，且不能重新放行；
4. 未投料的称量在投料瞬间再次校验，命中失败标记即 `422 quality_blocked`；
5. 挂失败标记的物料不能再被称量领用或合批。

谱系沿 `lot_movements` 的 split/merge 边用递归 CTE 双向遍历（`internal/store/tx.go`）。

## 放行

放行只能由药事负责人发起（`POST /v1/compound-batches/{ref}/release|hold|reject`），放行前三道闸：

1. 成品必须有最新一份**合格**检验结论；
2. 批次未被不合格原料阻断、未召回；
3. 每张配方的投料物料在放行此刻再次核对，均无失败标记。

放行决策逐笔留痕（可 hold/reject/release 多次），以最新一笔为当前处置状态。

## 外部查询

`GET /v1/public/compound-batches/{batch_ref}` 是对外唯一查询，**只**返回：

- `qualified_ranges`：各检验项目的合格范围（不含实测值、不含结论）；
- `disposition_status`：`released` / `held` / `rejected` / `recalled` / `pending`。

不返回供应商、产地、凭证、抽样、读数、检验人或任何患者/内部资料。完整证据链只在内部谱系接口对药事负责人开放。

## 内部核对接口

- `GET /v1/internal/material-lots/{lot_ref}/ledger`：逐笔重量台账（receipt/sample/split/merge/weigh）与入、出、余量，供任何内部岗位核对重量。
- `GET /v1/internal/material-lots/{lot_ref}/affected-batches`：一袋原料判不合格时，沿谱系向下列出实际使用过它（直接投料 `direct`，或经分装/合批后代投料 `downstream`）的成品批次及阻断状态，精确定位受影响成品，无需停用整个系列。

## 数据约定

`contracts/entities.json` 保存外部数据交换字段与不变量。来源编号（`*_ref`）仅表示提交方对同一记录的识别符；运行时内部使用独立主键。时间字段一律使用带时区偏移的 ISO 8601 字符串。持久化文件由 `DATABASE_PATH` 决定。
