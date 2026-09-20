# 院内制剂原料到成品质量放行

按入厂批次记录供应商凭证、抽样、复检、分装、称量和配制投料；原料拆分及合批保持重量可核对；不合格样品阻断其所有下游批次的放行；外部查询只暴露合格范围与处置状态；药事负责人可从任一成品反查所用药材及每一次质量判断。

本服务采用 HTTP 接口和 SQLite 本地文件（纯 Go 驱动 `modernc.org/sqlite`，无需 CGO 与 sqlite3 客户端）。

- `PORT`：监听端口，默认 8080
- `DATABASE_PATH`：数据文件路径，默认 `data/app.sqlite3`；迁移 SQL 内嵌在程序中，服务启动时自动应用
- `contracts/entities.json`：字段约定、角色权限矩阵与质量不变量
- `docs/domain.md`：领域规则说明
- `fixtures/example.json`：不含真实身份与真实读数的请求示例

## 本地开发

```sh
make migrate   # 应用内嵌迁移到 DATABASE_PATH
make test      # 运行全部自动化检查（含真实 SQLite 与触发器的端到端测试）
make run       # 启动服务
```

`docker compose up --build` 启动隔离容器，`APP_PORT` 调整宿主机端口。

## 接口一览

| 方法 | 路径 | 说明 | 签字/操作岗位 |
| --- | --- | --- | --- |
| POST | `/v1/staff` | 登记岗位人员 | 管理员 |
| POST | `/v1/materials` | 登记药材品种 | — |
| POST | `/v1/suppliers` | 登记供应商（含产地、许可范围） | 仓储/药事负责人 |
| POST | `/v1/suppliers/{id}/certificates` | 供应商凭证 | 仓储 |
| POST | `/v1/inbound-lots` | 入厂批次收货（开立谱系根批次、台账入账） | 仓储 |
| POST | `/v1/material-lots/{ref}/split` | 分装（重量守恒校验） | 仓储 |
| POST | `/v1/material-lots/merge` | 合批（同品种、重量守恒） | 仓储 |
| POST | `/v1/material-lots/{ref}/disposals` | 不合格物料处置（销毁/退回/灭活） | 仓储/药事负责人 |
| POST | `/v1/samples` | 抽样（复检用 `kind=retest` + `retest_of_ref`） | 仓储/检验人 |
| POST | `/v1/inspections` | 开启检验单 | 检验人 |
| POST | `/v1/inspections/{ref}/readings` | 追加读数（自动判定 pass/fail，不可改删） | 检验人 |
| POST | `/v1/inspections/{ref}/conclude` | 检验结论（一次定论） | 检验人 |
| POST | `/v1/compound-batches` | 开立配制批次 | 配制负责人 |
| POST | `/v1/compound-batches/{ref}/weighings` | 称量领出 | 仓储 |
| POST | `/v1/compound-batches/{ref}/charge` | 确认投料（投料瞬间复核失败标记） | 配制负责人 |
| POST | `/v1/compound-batches/{ref}/release` | 成品放行 | **药事负责人** |
| POST | `/v1/compound-batches/{ref}/hold` | 暂缓放行 | 药事负责人 |
| POST | `/v1/compound-batches/{ref}/reject` | 拒收 | 药事负责人 |
| GET | `/v1/internal/compound-batches/{ref}/lineage` | 成品反查完整证据链 | **药事负责人**（`viewer_id`） |
| GET | `/v1/internal/material-lots/{ref}/ledger` | 逐笔重量台账（拆分/合批/称量核对） | 内部岗位（`viewer_id`） |
| GET | `/v1/internal/material-lots/{ref}/affected-batches` | 一袋原料实际影响到的成品清单（direct/downstream） | 内部岗位（`viewer_id`） |
| GET | `/v1/public/compound-batches/{ref}` | 外部查询：合格范围 + 处置状态 | 公开 |

错误响应统一为 `{"error":{"code":..., "message":...}}`，状态码：`400 validation_error`、`403 role_forbidden`、`404 not_found`、`409 state_conflict/weight_mismatch/conflict`、`422 quality_blocked`。

## 快速试一遍

```sh
make run &
BASE=http://localhost:8080
curl -s -XPOST $BASE/v1/staff -d '{"id":"wh","name":"仓管","role":"warehouse"}'
# 收货 1kg → split 分装 → 检验 → 称量 → 投料 → 成品检验 → 放行
curl -s $BASE/v1/public/compound-batches/B1
# {"batch_ref":"B1","qualified_ranges":[...],"disposition_status":"released"}
```
