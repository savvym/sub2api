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

## Authorization Domain Contract 门禁

| 门禁 | 证据 | 状态 |
| --- | --- | --- |
| User、Service Principal 互斥且 System Actor 不可由 HTTP/业务包构造 | `internal/authz` Actor 构造边界与 JSON 伪造测试 | 通过 |
| 11 个 capability 与 migration 229 seed 完全一致 | `TestCapabilitiesMatchMigrationSeed` | 通过 |
| 13 个动作与四级访问映射准确，未知值 fail closed | action/access level table tests | 通过 |
| manager 不含 delete/transfer，public 仅 viewer/consumer | access level boundary tests | 通过 |
| legacy admin、capability、Owner、public、用户/角色 Grant 来源可审计 | typed provenance tests | 通过 |
| 404 隐匿、403、认证失败、依赖不可用与无效请求可区分 | denial reason/class tests | 通过 |
| 无路由、DI、Repository 或运行时 consumer，legacy 行为不变 | 路由/依赖审查 + 完整 backend unit/build | 通过 |

## Fresh Setup Compatibility Bootstrap 门禁

| 门禁 | 证据 | 状态 |
| --- | --- | --- |
| 管理员与 `system_bootstrap/admin` 兼容角色同事务提交 | setup sqlmock 成功/静默缺种子回滚测试 + PostgreSQL 18.6 fresh 动态验证 | 通过 |
| Web、CLI、AutoSetup 和多进程入口不会并发创建多个管理员 | 完整安装 advisory lock + `users` 事务锁 + PostgreSQL 双并发初始化验证 | 通过 |
| 并发安装不会把 loser 的 JWT/config 与 winner 的管理员混合 | PostgreSQL/Redis 双 `Install` 动态验证：单一成功、config/JWT/admin/password 同源 | 通过 |
| 重复 setup 不重复管理员或兼容 Grant | setup repeat 单测 + PostgreSQL repeat 动态验证 | 通过 |
| 已应用 229 后才创建的用户可由 232 修复 | 232 与 229 收敛 SQL 等价 contract + 本地开发库动态验证 | 通过 |
| 人工、其他 Service Principal 和其他角色 Grant 均保留 | 229/232 收敛边界 contract + migration integration 场景 | 通过 |
| `users.role` 仍为权威且 mode 保持 `legacy` | PostgreSQL fresh 查询 + 未写 settings contract | 通过 |
| setup 与正常启动对空密码/特殊字符/时区使用同一目标数据库 | 统一结构化 PostgreSQL URL 单测 + 独立临时数据库 fresh/重启验证 | 通过 |

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
