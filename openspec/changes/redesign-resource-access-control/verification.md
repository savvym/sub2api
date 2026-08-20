# Verification

状态词：`待实现`、`通过`、`失败`、`豁免（必须有批准链接）`。

## Requirement → Evidence

| Capability | Requirement | 主要证据 | 状态 |
| --- | --- | --- | --- |
| resource-authorization | 默认拒绝与资源隔离 | Policy 矩阵、跨租户 Repository/API 测试 | 待实现 |
| resource-authorization | 平台能力与资源动作分离 | Actor/Policy 单测、创建/分享 API 测试 | 待实现 |
| resource-authorization | SQL 范围过滤与不可枚举 | Repository integration、IDOR E2E、EXPLAIN | 待实现 |
| resource-authorization | 凭证与字段投影 | DTO/序列化 canary、日志/错误泄漏扫描 | 待实现 |
| self-service-resource-hosting | 有资格用户私有托管 | hoster/API/UI E2E、配额并发测试 | 待实现 |
| self-service-resource-hosting | 私有默认组与 group 0 隔离 | Scheduler repository/integration 测试 | 待实现 |
| self-service-resource-hosting | Backend/SIMPLE Mode 优先 | 模式组合测试 | 待实现 |
| resource-sharing | 分级、多主体分享 | Grant schema/Policy/API/UI 测试 | 待实现 |
| resource-sharing | 分享不级联 | DTO、关系查询、跨资源 E2E | 待实现 |
| resource-sharing | 帐号-分组受众闭包 | audience closure/TOCTOU/Scheduler 测试 | 待实现 |
| resource-sharing | 撤权与到期 | Grant 边界、协调器、多来源重算测试 | 待实现 |
| runtime-authorization-consistency | 运行时再次授权 | API Key、WS、异步任务、fallback 测试 | 待实现 |
| runtime-authorization-consistency | 两条 Outbox 与传播 SLA | 故障注入、lag 指标、多实例恢复测试 | 待实现 |
| runtime-authorization-consistency | 渐进迁移与回滚 | fresh/upgrade/reapply、shadow diff、模式测试 | 待实现 |

## Dark Schema Foundation 门禁

| 门禁 | 证据 | 状态 |
| --- | --- | --- |
| fresh、228 upgrade、重复 `ApplyMigrations` | 本机 PostgreSQL 18.6 两个临时库动态验证 | 通过 |
| 存量 owner/creator/public=NULL，version=1，group mode=legacy | migration integration 场景 + 动态查询 | 通过 |
| RBAC seed 唯一、兼容角色幂等并随旧角色收敛 | migration contract/integration 场景 + 双向动态验证 | 通过 |
| Grant XOR、访问级别、FK、部分唯一索引 | SQL contract + PostgreSQL 动态约束验证 | 通过 |
| authz event Owner/auth method 快照且 append-only | SQL/Ent contract + PostgreSQL 写拒绝验证 | 通过 |
| 5 个开关默认 false，role mode=legacy，缺失/读取错误 fail closed | service unit + admin API contract/partial payload 测试 | 通过 |
| 存量热表索引并发创建且 invalid index 可重试 | 231 contract、runner sqlmock、PostgreSQL `indisvalid=true` | 通过 |
| 不新增普通用户资源路由，现有行为等价 | 路由审查、后端完整单测、前端 1654 tests | 通过 |
| SQL/Ent 一致且生成代码最新 | Ent schema tests + `make -C backend generate` | 通过 |
| CI/Testcontainers repository integration | `CI=1 go test -tags=integration ./internal/repository` | 待实现（本机无 Docker） |

## 标准命令

```bash
openspec status --change redesign-resource-access-control
openspec validate redesign-resource-access-control --type change --strict --no-interactive
make -C backend generate
make -C backend test-unit
make test-frontend
make -C backend build
pnpm --dir frontend run build
```

CI/Docker 环境追加：

```bash
cd backend
CI=1 go test -tags=integration ./internal/repository
```

## 灰度与回滚门槛

- 任何开关开启前，所有实例必须已运行认识对应 Schema/模式的版本。
- Auth/Scheduler Outbox lag 超过阈值时禁止扩大分享；权威读取不得 fail open 回旧表。
- acl 资源仅在旧数据能表达完全相同集合时可切回 legacy；高级 Grant 存在时禁止盲目回切。
- 删除旧字段、旧表、旧入口必须作为独立版本，并在发布说明中声明旧版本回滚能力结束。

## 最终实施记录

- 待各阶段完成后填写 commit/PR、环境、命令、结果、指标窗口和批准人。
