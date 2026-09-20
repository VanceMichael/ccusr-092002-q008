# 院内制剂出处与质控放行

龙华医院制剂中心"原料 → 成品"质量放行后端：按入厂批次记录供应商凭证、抽样、复检、分装、称量与配制投料；原料拆分与合批保持重量可核对；不合格样品阻断其全链下游成品的放行；药事负责人可从任一成品反查所用药材及每次质量判断。

本服务采用 HTTP 接口和 SQLite 本地文件（纯 Go 驱动，`CGO_ENABLED=0` 可构建）。运行参数 `PORT` 指定监听端口，`DATABASE_PATH` 指定数据文件；迁移在服务启动时自动执行（`migrations/*.sql` 同时内嵌进二进制）。

## 质量规则（系统强制）

1. **复检不覆盖首败**：检验记录按轮次追加，数据库触发器禁止 UPDATE/DELETE。只要来货批存在任一轮失败读数（即使复检合格），即不得放行。
2. **签字职责分离**：角色固定为药库 `warehouse`、检验人 `inspector`、配制负责人 `compounding_lead`、药事负责人 `qa_release`。配制负责人不能做本批成品检验签字、不能自行放行（应用层 + 数据库触发器双重强制）。
3. **不合格全链阻断**：放行时在 批次→称量→(合批→)分装→来货 的血缘图上做闭包复核，任一上游来货非 `released` 或成品检验有失败轮，放行被拒、批次置 `blocked` 并留痕。投料时同样拦截未放行原料，含合批的每个来源。
4. **重量可核对**：分装累计不得超过来货重量；合批产出必须等于各来源取用量之和；容器/合批剩余量逐笔扣减，超量拒绝；成品配制完成后实际投料须与组方版本用量逐条一致。`GET /v1/weight-check` 提供全库核对单。
5. **外部最小暴露**：`/public` 接口只返回合格范围与处置状态，不含供应商凭证、产地、检验读数、签字人。

## 角色与接口

内部接口均需请求头 `X-User-ID: <工号>`，角色不符返回 403，缺头返回 401。

| 方法与路径 | 允许角色 | 说明 |
| --- | --- | --- |
| `POST /v1/bootstrap` | 免认证（仅用户表为空时一次） | 自举首个账号 |
| `POST /v1/users` | 任一内部账号 | 创建岗位账号 |
| `POST /v1/materials` | qa / warehouse | 药材及合格范围 `spec_min`/`spec_max` |
| `POST /v1/formulas` | lead / qa | 方剂组方版本（历史版本各自保存） |
| `POST /v1/receipts` | warehouse | 来货登记：供应商、凭证、产地、来货重量 |
| `GET  /v1/receipts/{ref}` | 全部内部角色 | 来货质量档案：抽样、各轮检验、处置流水 |
| `POST /v1/receipts/{ref}/samples` | inspector | 抽样 |
| `POST /v1/samples/{ref}/inspections` | inspector | 录入一轮读数（第 2 轮起自动标记复检，合格性按范围系统判定） |
| `POST /v1/receipts/{ref}/decision` | qa | `released` / `rejected` / `quarantined` |
| `POST /v1/receipts/{ref}/dispense` | warehouse | 分装成容器，守恒校验 |
| `POST /v1/merges` | warehouse | 多容器合批，产出须等于来源之和，禁止跨药材 |
| `POST /v1/batches` | lead | 按方剂版本开批 |
| `POST /v1/batches/{ref}/weighings` | lead（本批） | 称量投料，来源为 `container_ref` 或 `merge_ref` 二选一 |
| `POST /v1/batches/{ref}/finish` | lead（本批） | 完成配制、登记收得重量 |
| `POST /v1/batches/{ref}/inspections` | inspector（非本批 lead） | 成品检验，可多轮追加 |
| `POST /v1/batches/{ref}/release` | qa（非本批 lead） | `released`/`rejected`/`withheld`；放行执行全链阻断复核 |
| `GET  /v1/batches/{ref}` | 全部内部角色 | 批次详情：投料、成品检验、最新放行 |
| `GET  /v1/batches/{ref}/lineage` | qa / inspector | 成品反查血缘图与每次质量判断 |
| `GET  /v1/receipts/{ref}/impact` | qa / inspector / warehouse | 原料正向影响面：分装、合批、受影响成品批 |
| `GET  /v1/weight-check` | qa / warehouse | 全库重量核对单 |
| `GET  /public/v1/receipts/{ref}` | 免认证 | 仅 `material_code/spec_min/spec_max/spec_unit/disposition` |
| `GET  /public/v1/batches/{ref}` | 免认证 | 仅 `batch_ref/status/release` |
| `GET  /health` | 免认证 | 健康检查 |

时间字段统一为带时区偏移的 ISO 8601 字符串；重量单位统一为克（数值）。

### 典型流程

```bash
# 1. 首次初始化（四个岗位各建账号）
curl -X POST localhost:8080/v1/bootstrap -d '{"id":"wh","name":"王库管","role":"warehouse"}'
curl -X POST localhost:8080/v1/users -H 'X-User-ID: wh' -d '{"id":"qa","name":"孙药事","role":"qa_release"}'

# 2. 药材与方剂
curl -X POST localhost:8080/v1/materials -H 'X-User-ID: qa' \
  -d '{"code":"HQ","name":"黄芪","spec_min":90,"spec_max":110,"spec_unit":"%"}'
curl -X POST localhost:8080/v1/formulas -H 'X-User-ID: lead' \
  -d '{"formula_ref":"F-BUXI","revision":1,"name":"补益方","license_scope":"本机构使用",
       "items":[{"material_code":"HQ","required_weight":100}]}'

# 3. 来货 → 抽样 → 检验 → 放行 → 分装
curl -X POST localhost:8080/v1/receipts -H 'X-User-ID: wh' \
  -d '{"receipt_ref":"R-1","material_code":"HQ","supplier":"S1","supplier_voucher":"V1",
       "origin":"甘肃","received_weight":500}'
curl -X POST localhost:8080/v1/receipts/R-1/samples -H 'X-User-ID: insp' -d '{"sample_ref":"S-1"}'
curl -X POST localhost:8080/v1/samples/S-1/inspections -H 'X-User-ID: insp' -d '{"measured":100}'
curl -X POST localhost:8080/v1/receipts/R-1/decision -H 'X-User-ID: qa' -d '{"target":"released"}'
curl -X POST localhost:8080/v1/receipts/R-1/dispense -H 'X-User-ID: wh' \
  -d '{"splits":[{"container_ref":"C-1","weight":500}]}'

# 4. 配制 → 投料 → 成品检验 → 放行
curl -X POST localhost:8080/v1/batches -H 'X-User-ID: lead' \
  -d '{"batch_ref":"B-1","formula_ref":"F-BUXI","revision":1}'
curl -X POST localhost:8080/v1/batches/B-1/weighings -H 'X-User-ID: lead' \
  -d '{"material_code":"HQ","container_ref":"C-1","weight":100}'
curl -X POST localhost:8080/v1/batches/B-1/finish -H 'X-User-ID: lead' -d '{"yield_weight":95}'
curl -X POST localhost:8080/v1/batches/B-1/inspections -H 'X-User-ID: insp' \
  -d '{"outcome":"pass","measured":"符合规定"}'
curl -X POST localhost:8080/v1/batches/B-1/release -H 'X-User-ID: qa' -d '{"decision":"released"}'

# 5. 追溯：成品反查药材血缘 / 原料影响面 / 重量核对
curl -H 'X-User-ID: qa' localhost:8080/v1/batches/B-1/lineage
curl -H 'X-User-ID: qa' localhost:8080/v1/receipts/R-1/impact
curl -H 'X-User-ID: qa' localhost:8080/v1/weight-check
```

## 本地开发

`make test` 运行全部自动化检查（含全链路场景测试），`make run` 启动服务，`make migrate` 可用 sqlite CLI 手动初始化数据文件。`docker compose up --build` 启动隔离容器，`APP_PORT` 可调整宿主机端口。

`fixtures/example.json` 保存不含真实身份的交换示例，`contracts/entities.json` 记录字段约定，`docs/domain.md` 介绍来源与范围。
