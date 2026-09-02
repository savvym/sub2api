# Verification

验证状态词：`待实现`、`通过`、`失败`、`豁免（必须有批准链接）`。安全清单阶段状态使用 `Review Ready`、`Decision Accepted`、`Release Accepted`；`Decision Accepted` 只批准设计，`Release Accepted` 才批准实施/发布，两者不得简写为 `Accepted`。

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
| resource-sharing | 撤权与到期 | 1.11 已覆盖严格到期边界、四来源协调与 durable Scheduler 事件；完整 `account_groups` 关系重算仍待 4.2/4.4 | 待实现 |
| runtime-authorization-consistency | 运行时再次授权 | API Key、WS、异步任务、fallback 测试 | 待实现 |
| runtime-authorization-consistency | 两条 Outbox 与传播 SLA | 1.11 已覆盖 claim/lease/fencing、恢复、lag、5 秒目标和 30 秒扩大权限门；数据面 E2E 与发布窗口验证仍待实现 | 待实现 |
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
| CI/Testcontainers repository integration | GitHub Actions push [Run 32711471080](https://github.com/savvym/sub2api/actions/runs/32711471080) / [test job 97383587468](https://github.com/savvym/sub2api/actions/runs/32711471080/job/97383587468)：`make test-integration` → `go test -tags=integration ./...` | 通过；SHA `2d203b601c5d5b6578e91020bdbfbff4eb5bae6b`，repository 非缓存运行 41.430s |

## Authorization Domain Contract 门禁

| 门禁 | 证据 | 状态 |
| --- | --- | --- |
| User、Service Principal 互斥且 System Actor 不可由 HTTP/业务包构造 | `internal/authz` Actor 构造边界与 JSON 伪造测试 | 通过 |
| 12 个 capability 与 migration 229 + 243 seed 完全一致 | `TestCapabilitiesMatchMigrationSeed` | 通过 |
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

## Policy 与 SQL Scope Foundation 门禁

| 门禁 | 证据 | 状态 |
| --- | --- | --- |
| capability/create/resource Policy 矩阵默认拒绝且 provenance 稳定 | `internal/authz` 表驱动测试 | 通过 |
| PolicyStore 在数据库时间读取主体、角色、能力、开关、资源和有效 Grant | sqlmock contract + PostgreSQL 18.6 动态测试 | 通过 |
| 空/缺失/非法 snapshot fail closed | PolicyStore decode 与领域构造测试 | 通过 |
| legacy/shadow `group.use` 不被 ACL 结果混入 | PolicyService authorization-mode 测试 | 通过 |
| Scope 绑定精确资源/动作并重校验主体、角色、能力和开关 | Scope contract、SQL plan 与 stale-version 动态测试 | 通过 |
| Account/Group Ent predicate 在 PostgreSQL 正确编号参数 | 外层 filter + Scope 动态执行测试 | 通过 |
| Owner/public/direct/role Grant、严格到期边界、Count 与分页使用同一 predicate | PostgreSQL Account/Group Scope 动态测试 | 通过 |
| 真实 scoped reader 的筛选、排序、total、分页、聚合/hydration 禁用和窄 DTO | 1.6b repository/service/DTO 单测 + PostgreSQL 18.6 动态测试 | 通过 |

## RoleService 与 Mode Management 门禁（1.7a-1.7b）

| 门禁 | 证据 | 状态 |
| --- | --- | --- |
| legacy 用户角色变更只经 RoleService；管理员入口已接入且通用 UserRepository Update 不能写 Role | AdminService/RoleService unit contract、repository 写掩码审查、生产 Wire build | 通过 |
| legacy/shadow 下 `users.role`、bootstrap 兼容 `user_roles` 与 `users.authz_version` 原子提交，相邻用户字段失败时整体回滚 | RoleRepository transaction tests + 本机 PostgreSQL 18.6 动态场景 | 通过 |
| 纯 `authz_version` 变化也为每个有效 API Key 写入 hashed durable cache invalidation | migration 233 contract + PostgreSQL 18.6 reapply/触发器动态测试 | 通过 |
| active admin actor、expected-role CAS、自我降级、最后一个 active admin 和 disabled admin 不变量 fail closed | RoleService/AdminService 表驱动测试 + PostgreSQL 并发互降测试 | 通过 |
| 自动封禁与角色提升不能形成 disabled admin，mode readiness 与普通用户更新不会锁升级死锁 | ContentModeration/UserRepository race tests + PostgreSQL 18.6 真实并发测试 | 通过 |
| 所有生产用户创建路径事务内补 `system_bootstrap` 兼容角色 | UserRepository create integration 场景 | 通过 |
| legacy→shadow readiness 检查 229/232/233、系统角色/bootstrap、兼容角色与版本；shadow→legacy 检查不可映射 RBAC admin/Service Principal 角色 | RoleService contract + PostgreSQL 18.6 readiness/mode 动态测试 | 通过 |
| 通用 settings PUT 不能改变 mode，内部 transition 仅允许 legacy↔shadow，任何 RBAC transition 硬拒绝 | settings handler/service contract + RoleService transition tests | 通过 |
| 专用 GET 返回 current/next mode、数组 blockers 与 `can_transition`，使用 read-only repeatable-read snapshot 且不阻塞用户写 | RoleService unit + PostgreSQL 18.6 并发动态场景 + HTTP smoke | 通过 |
| 专用 POST 使用 strict payload 和 expected-mode CAS，无条件要求带 session ID 的 JWT TOTP step-up，拒绝 sid-less/未知 auth/Admin API Key，RBAC 仍硬拒绝 | AuthorizationHandler/step-up/RoleService unit + HTTP smoke | 通过 |
| mode 更新与固定成功 durable audit 使用同一 repository transaction；audit 失败必须回滚 mode，单次成功恰好一条 | RoleService contract + PostgreSQL 18.6 durable audit/trigger rollback 场景 | 通过 |
| 成功 transition 调用 `SkipAudit` 避免重复；失败不 skip，仅进入现有异步 middleware best-effort 尝试审计，不承诺 durable | AuthorizationHandler + AuditLog middleware 聚焦 unit | 通过 |
| 当前 1.7b 工作区完整后端 unit 与 OpenSpec strict validate | `make -C backend test-unit` + `openspec validate ... --strict` | 通过 |
| 本地 HTTP 验证 GET、JWT TOTP step-up、Admin API Key 拒绝、CAS、成功审计与清理恢复 | 本地开发环境 API/数据库烟测 | 通过；结束时为 legacy fallback，临时状态已清理 |
| CI/Testcontainers RoleRepository integration | GitHub Actions push Run `32711471080` 完整 integration suite；`make test-integration` → `go test -tags=integration ./...` | 通过；test job `97383587468`，repository 非缓存运行 41.430s |

## Resource Mutation Transaction 门禁（1.10）

| 门禁 | 证据 | 状态 |
| --- | --- | --- |
| `SERIALIZABLE`、已有未知隔离事务 fail closed、Actor/角色/资源固定锁序 | coordinator/repository unit + 聚焦 race | 通过 |
| 事务内重解析 Actor，并对主体/角色快照、Policy 与 expected `access_version` 重校验 | coordinator table tests | 通过 |
| 批量混入不可见、无权或版本变化目标时全拒绝且零副作用 | invisible batch、bulk rollback 与 snapshot tests | 通过 |
| 业务写、版本、durable resource event 与 Scheduler Outbox 同事务；audit/outbox 失败整体回滚 | repository contract、Scheduler/Auth Outbox 故障注入 integration 场景 | 通过；contract、本地标签编译及 CI/Testcontainers 动态套件均通过 |
| Group 授权字段变化产生 hashed Auth Outbox 并随事务回滚 | migration 237 contract + PostgreSQL 18.6 隔离库动态测试 | 通过；27 个子场景全部通过，临时库残留为 0 |
| no-op/replay 不写版本、event、durable marker 或 callback | coordinator + Account/Group duplicate tests | 通过 |
| 外部缓存/网络 callback 仅在 commit 后执行，panic 被逐个隔离 | coordinator tests | 通过 |
| JWT creator 与 Admin API Key Service Principal 归因互斥，不写凭据值 | service/repository event contract tests | 通过 |
| 生产 Wire 注入真实 Resolver、Policy 与 ResourceMutationRepository | 默认/integration 标签全仓编译 + backend build | 通过 |
| 公开 AdminService 构造缺少 coordinator 时 fail closed，不回退 legacy 直写 | constructor regression + API contract coordinator fixture | 通过 |
| 完整 backend unit、相关 vet 与聚焦 service/repository race | `make -C backend test-unit` + `go vet` + `go test -race` | 通过 |
| CI/Testcontainers ResourceMutationRepository integration | GitHub Actions push Run `32711471080` 完整 integration suite；`make test-integration` → `go test -tags=integration ./...` | 通过；Scheduler/Auth Outbox 故障注入场景包含在无 `-run` 过滤的 repository suite 中 |

## Authorization Expiry 与 Propagation 门禁（1.11）

| 门禁 | 证据 | 状态 |
| --- | --- | --- |
| User/Service Principal role 与 Account/Group Grant 在 `expires_at <= statement_timestamp()` 时同步失效 | PolicyStore/SQL Scope contract、严格边界 unit 与 PostgreSQL 动态测试 | 通过 |
| migration 238 为四类来源原子维护、回填和重臂 durable expiry job，重复应用不复活同 generation 已处理作业 | migration contract + PostgreSQL 18 migration/rearm 场景 | 通过 |
| `authorization_expiry_coordinator` 仅作 durable 审计身份，必须 active 且零角色；disabled、缺失或带任意角色均 fail closed | migration collision contract、repository readiness/锁测试、传播健康测试 | 通过 |
| 到期处理固定 `parent -> source -> job -> coordinator` 锁序；版本、audit/resource event、Scheduler enqueue 与 job 完成同事务 | expiry repository unit + PostgreSQL exact-once、audit failure rollback 和管理写并发无死锁场景 | 通过 |
| 租约过期可恢复，claim ownership、rearm generation、orphan source、retry/release 与停机 detached cleanup 不重复副作用 | expiry repository/worker race + PostgreSQL lease/rearm/orphan 场景 | 通过 |
| Scheduler Outbox 使用 PostgreSQL `SKIP LOCKED` claim、lease token fencing、ack/retry；durable bucket/lifecycle/full rebuild 锁忙或 fencing 必须 retry，不能 ACK | repository/service contract + PostgreSQL commit-order/lease recovery + strict rebuild tests | 通过 |
| migration 239 的新增 CHECK 使用 `NOT VALID`；Scheduler claim 索引由 migration 241 以 `_notx` 在线创建，invalid index 可清理重试 | migration 239/241 contract + migration runner tests | 通过 |
| Auth Outbox stage 0 固定优先，stage 0/1 指标分离，停机释放整批未结清 claim；second pass/retry 只传相对 delay，由数据库时间计算 `available_at`；并发索引可在线创建及恢复 | worker/repository race、migration 240 contract 与 migration runner tests | 通过 |
| API Key allow snapshot v22 丢弃 v21 及更早版本；首次正向 L1/L2 写入受 monotonic deadline 和 30 秒上限约束，rewrite 不续期，正向 L2 不提升 L1，无效 L2 只回源且不删除并发新值 | API Key auth cache version/TTL/clock-skew/interleaving + miniredis TTL/JSON round-trip tests | 通过 |
| 传播统计按数据库时间分别计算 Auth primary/safety、Scheduler、Expiry 的 pending/ready/oldest lag；延迟 safety pass 不计入 5 秒目标但受 30 秒安全线约束 | propagation repository/service tests | 通过 |
| 5 秒目标与 30 秒安全门边界精确；统计错误、必需 Worker 缺失/停止和 coordinator 不就绪均稳定禁止扩大权限 | propagation guard race + handler JSON contract | 通过 |
| Settings 仅在有效 Feature Flag 状态扩大时要求传播健康；关闭功能和撤权不受积压阻塞；ResourceMutation 扩权必须显式设置 `ExpandsAccess` | settings/resource mutation unit contracts | 通过 |
| Ops 健康入口暴露队列、Worker、target/safety、`expiry_coordinator_ready` 与稳定降级原因，且不因可选 Ops monitoring disabled 而隐藏安全状态 | `GET /api/v1/admin/ops/authorization/propagation/health` handler/route tests | 通过 |
| 完整 `account_groups` 授权来源、验证版本和撤权/到期/角色变化关系重算 | 任务 4.2/4.4；1.11 只产生 durable Scheduler 事件 | 待实现 |
| CI/Testcontainers 1.11 repository dynamic suite | GitHub Actions push Run `32711471080` 完整 integration suite；`make test-integration` → `go test -tags=integration ./...` | 通过；test job `97383587468`，SHA `2d203b601c5d5b6578e91020bdbfbff4eb5bae6b` |

## Phase 0 Exit Review（0.8）

Phase 0 退出是设计治理门禁，不是 Phase 2 发布验收。它确认安全边界、责任和验证计划已经冻结；不要求 `credential-inventory.md` 第 8.2 节、`outbound-security.md` 第 6/7/8.2 节的代码/目标环境证据已经完成，也不要求尚不存在的生产预检或 shadow 观察已经执行。这些证据转为首次部署/首次启用触发门禁，不能因勾选 0.8 被豁免。当前项目的单维护者、无部署范围决定记录在 [`phase-0-exit-record.md`](phase-0-exit-record.md)，并明确不声称存在独立三方评审。

| 门禁 | 必需证据 | 批准角色 | 当前状态 |
| --- | --- | --- | --- |
| 0.1-0.3 产品与权限基线 | `design.md` Accepted 决策、写入口/引用 ID 覆盖清单及对应 commit/PR | sole maintainer 在当前单人项目中承担范围 owner | 通过；已纳入版本化退出记录 |
| 0.4 凭据设计决策 | `credential-inventory.md` 第 8.1 节全部关闭，状态为 `Decision Accepted`；未知键/`header_overrides` fail closed、明文导出关闭、迁移约束冻结 | sole maintainer；首次部署/启用时应优先增加独立安全 reviewer | 通过；仅为设计接受，`Release Accepted` 仍未完成 |
| 0.5 出站设计决策 | `outbound-security.md` 第 8.1 节全部关闭；启用 allowlist 为空，全部 API Key 组合暂缓，OAuth 禁止 | sole maintainer；首次启用组合时重新评审 | 通过；仅为设计接受，`Release Accepted` 仍未完成 |
| 0.6 数据预检设计 | `data-preflight.sql` 的 `REPEATABLE READ READ ONLY`、输出最小化、异常分类、回填规模口径，以及两份 auto-reset inventory 的分类与处理路由 | sole maintainer；真实数据 owner 在首次导入/升级时加入 | 通过；脚本与 PostgreSQL 定向回归已通过，真实执行转为首次现有数据导入/升级门禁 |
| 0.7 兼容矩阵 | `compatibility-matrix.md` 的 Feature Flag x Backend/SIMPLE/authorization mode | sole maintainer | 通过；文档版本已纳入退出记录 |
| 未决实施项有 owner | 两份安全清单的所有 `Release Accepted` 待办登记 owner、触发时点和验证方法 | sole maintainer / future operator；生产场景建议独立安全 reviewer | 通过；详见退出记录的激活门禁表 |
| Phase 0 退出记录 | 版本化记录包含基线、日期、0.1-0.7 结论、空 allowlist、单维护者风险接受、未决项和自动重开条件 | sole maintainer；明确不声称独立三方批准 | 通过；仅适用于无部署、无真实数据、全部新能力默认关闭的范围 |

上表已由版本化单维护者记录关闭，因此可以勾选 0.8。该记录的限制和自动重开条件属于批准本身：创建部署环境、导入真实数据、启用任何自助/导出/shadow/RBAC/ACL 能力或发现新的设计级安全阻塞，都会重新打开 0.8。未来存在多角色团队或生产风险时，批准应升级为可追溯的独立平台、认证和安全评审。

## Phase 1 Exit Review（1.12）

本表区分 dark-foundation 工程验证、当前适用的阶段批准和未来环境激活门禁。1.12 只能在所有当前适用项通过，且任何不适用项都有明确事实、owner、触发时点和自动重开条件时勾选；该勾选不自动开始 Phase 2，也不把未来激活门禁视为豁免。

| 门禁 | 证据 | 状态 |
| --- | --- | --- |
| User/Service Principal 跨租户 Account/Group 全链隔离 | 本机 PostgreSQL 18.6 真实 `Resolver -> Policy -> Scope -> reader` 矩阵；覆盖搜索、排序、分页、total 与详情 IDOR 404 | 通过 |
| 旧 Scope 重校验与固定 Admin API Key fail closed | 本机 PostgreSQL 18.6 动态测试；覆盖开关、角色版本、主体停用，以及 `admin_api_key` code/status 变化 | 通过 |
| SIMPLE Mode 发布护栏与模式组合 | SettingService、生产 PolicyStore、Scope 三层 fail closed；Standard/SIMPLE x 5 个 Feature Flag 的 64 组测试，并验证数据库 raw flags 仍可为 true | 通过 |
| SQL Scope 生产规模查询计划 | PostgreSQL 18.6、20,000 行 Account/Group 与大规模无关 Grant fixture；Owner、public、direct-user、role Grant 稀疏索引均命中，主表无 Seq Scan；Account/Group 的 legacy admin/platform capability 全局计划只访问资源关系一次 | 通过 |
| migration 242 在线 public scope 索引 | SQL contract、`_notx` runner invalid-index retry 与本机 PostgreSQL 索引有效性/查询计划测试 | 通过 |
| main/authz 同编号 migration 双线收敛 | `TestSharedMigrationNumberLineagesConverge` 覆盖 main-first、authz-first、完整文件名记账、两次完整 apply 与双方 schema/data 保留；当前代码 SHA `aeb967ebe` 的 push/PR CI 均无 `-run` 过滤地非缓存执行 integration-tag repository 包 | 通过；Run `33608505225`/`33608510880` 的 repository 分别运行 `50.632s`/`49.677s`，并覆盖 migration 243 |
| main quota handler trusted Actor 合并 | OpenAI 手动 reset 的 quota/recover/load adapters 保留同一 User/SP Actor；Grok reset/reconcile 使用 `Admin*` facade；聚焦 handler/service 测试 | 通过 |
| OpenAI quota auto-reset 后台 Actor | 固定 roleless Service Principal/direct worker permission；missing/disabled/角色或权限形态错误时零副作用。普通执行先做三次 mutable eligibility reload，第四次在每个 POST 前的 proxy/account 锁内同时复核 eligibility 与完整 upstream identity；独立 recovery keyset pager 不依赖 enabled/status/schedulable，malformed candidate 也可靠、可取消地阻塞入队。managed state strict 拒绝未知字段/类型/状态、非 canonical hash 对和超界 count。evaluate 前半程持 advisory-only lease，POST 前才按 proxy `FOR SHARE` → account `FOR UPDATE` 升级，POST 返回立即释放 advisory+row transaction，agent retry 取得新 lease。新操作逐次重授权、Actor-qualified idempotency 与稳定 redeem request ID；上游效果后撤权时仅允许不重新授权的原子 finalizer 提交 `recovery_deferred` SP audit + succeeded 事实，恢复仍拒绝且恢复授权后不二次 reset | 通过；focused service/repository、race、本机 PostgreSQL finalizer，以及当前代码 SHA 的 push/PR CI、完整 Testcontainers 与 Security Scan 均成功。生产启用门禁仍单独受阻 |
| migration 243 protected attempt 升级与协调 | 五列 marker 固化无 account FK 的正 `account_id`；一次性 `completed=TRUE` sentinel；account scope 解析与 raw/旧 SP 稳定 key 唯一映射，0/多匹配 fail migration；全部 account replay 与 protected raw/旧 SP → reserved SP；success/no-effect 两个非 PUBLIC、SECURITY INVOKER owner 函数均校验 marker provenance 与 SP audit，分别强制 `reconcile-success:<record_id>` / `reconcile-no-effect:<record_id>` decision ID，不复用 redeem request ID。success 原子 audit+tombstone+succeeded，数据库固定 8 天 retention，到期后若同 stable-key 的 `resetting|failed` managed state 仍在则继续保留；no-effect 的 drained 参数只是调用方断言，批准前必须归档 fleet shutdown/drain 与 upstream no-effect 证据，audit extra 带不含凭据的 evidence ref/decision owner，原子 409 audit+删除 marker/parent并支持 exact retry；仅 stable-key 精确匹配时清当前 managed state，更新/不同 state 保留；unknown 禁止猜；迟到旧 UPDATE、未协调 cleanup、RESTRICT 后备、raw fence/malformed/post-snapshot current retryable 与旧 account scope 写围栏 | 通过；本地与 Testcontainers migration/fresh/upgrade/reapply 已验证。当前没有部署、旧 Worker 或历史数据，生产 inventory/drain 不适用；首次导入或升级现有数据库时自动恢复为硬门禁 |
| migration 243 协调输入/签名加固 | 两函数 SQL 强制 audit extra 精确绑定 account/record/fingerprint，必须含 trim 后长度合法的 string `evidence_ref`/`decision_owner`，缺失或错配整事务回滚；confirmed-success 唯一入口为八参数 `reconcile_openai_quota_auto_reset_protected_attempt(bigint,text,text,bigint,timestamptz,text,integer,jsonb)`，raw reapply 显式删除两个废弃 overload | 通过；actor migration 定向 PG、完整 integration-tag migrations 包，以及当前代码 SHA 的无过滤 push/PR Testcontainers suite 均成功。生产协调证据仅在未来出现旧数据/marker 时触发 |
| 当前 Phase 1 管理员写面 TOCTOU | 本机 PostgreSQL 18.6 两个不同管理员 Actor 双事务同版本并发；恰一提交/一冲突，SERIALIZABLE mutation closure 可执行 1 或 2 次且 loser 尝试完整回滚，最终业务状态、版本、durable event 与 Scheduler Outbox 恰好一次；真实 `AdminService.ClearAccountError` 测试证明 production after-commit callback 仅在提交后执行且恰好一次，通用语义另由 coordinator 单测覆盖 | 通过 |
| 228 到当前 migration 243 持久升级、重复 apply | 当前代码 SHA `aeb967ebe` 的 GitHub Actions push/PR CI 均运行无 `-run` 过滤的完整 integration suite；包含 `TestResourceAccessControlUpgradeFrom228ThroughCurrent` | 通过；PostgreSQL `18.1-alpine3.23` Testcontainers 动态升级/reapply 成功，覆盖 migration 243 |
| 完整 backend unit、聚焦 race/vet、默认与 integration 标签编译、build | 当前代码 SHA 的本地验证与 GitHub Actions | 通过；本地命令结果见 `implementation-evidence.md` 最新小节，远端完整 unit/integration、lint 亦成功 |
| CI repository Testcontainers 动态套件（当前代码 SHA） | [push Run 33608505225](https://github.com/savvym/sub2api/actions/runs/33608505225) / [test job 100177927975](https://github.com/savvym/sub2api/actions/runs/33608505225/job/100177927975) 与 [PR Run 33608510880](https://github.com/savvym/sub2api/actions/runs/33608510880) / [test job 100177945050](https://github.com/savvym/sub2api/actions/runs/33608510880/job/100177945050)：`make test-integration` → `go test -tags=integration ./...`；Ubuntu、Go 1.27.0、PostgreSQL `18.1-alpine3.23`、Redis `8.4-alpine` | 通过；attempt 1、SHA `aeb967ebe0d9ed9aa5b43f0f9e60dc030f3839e6`，integration step 均为 `3m38s`，repository 分别非缓存运行 `50.632s`/`49.677s` |
| production role shadow 差异记录能力 | `PolicyService` 四个入口并行计算 legacy/RBAC 且保持 legacy 响应；管理员 JWT/Admin API Key 生产入口接线；低基数、无 ID 的结构化 INFO/WARN 日志与 observer panic 隔离测试 | 通过（日志可由外部系统聚合；未新增独立进程内指标） |
| Phase 0 安全决策与退出批准 | 0.4/0.5 达到设计级 `Decision Accepted`，0.8 记录责任、范围、未决项与自动重开条件 | 通过；由 sole maintainer 接受无部署、空 allowlist 范围，明确不声称独立三方评审 |
| 生产只读数据预检与凭据键名统计 | 对批准的只读副本运行 `data-preflight.sql` 与 `credential-key-preflight.sql`，归档异常/回填规模及受限结果 | 当前不适用；不存在 production/staging 或真实数据。首次导入/升级现有数据库或处理真实帐号数据时自动成为阻塞门禁，异常分类和处理要求保持不变 |
| 目标环境 shadow readiness 与观察 | 专用 role-mode readiness、legacy→shadow 执行、具体差异指标、日志量与 sink `dropped_count`、观察窗口和回滚证据 | 当前不适用；不存在目标环境且 mode 不切换。首次进入 `shadow` 前自动成为阻塞门禁 |
| PR URL（当前代码 SHA 快照） | 采证快照（2026-09-02）：[Draft PR #1](https://github.com/savvym/sub2api/pull/1) 的 base 为 `main`、head 为 `codex/resource-access-control-foundation`、base OID `efb46db0a960fdad94502b1c3a982a0051cf5245`、代码证据 OID `aeb967ebe0d9ed9aa5b43f0f9e60dc030f3839e6` | 通过；代码 SHA 为 `MERGEABLE/CLEAN`。Phase 1 无部署范围退出后仍按维护者决定保持 Draft，不自动转 Ready 或合并 |
| GitHub Security Scan（当前代码 SHA） | [push Run 33608505242](https://github.com/savvym/sub2api/actions/runs/33608505242) 与 [PR Run 33608511032](https://github.com/savvym/sub2api/actions/runs/33608511032)，均覆盖 backend `govulncheck` 与 frontend production audit exception check | 通过；SHA `aeb967ebe0d9ed9aa5b43f0f9e60dc030f3839e6`，两个 backend job 均报告代码受影响漏洞 0 个，两个 frontend audit exception check 均成功 |
| 平台/认证/安全批准人 | 多角色生产场景应有独立可追溯批准；当前单维护者项目必须透明记录角色合并和风险范围 | 通过；sole maintainer 接受当前 dark-foundation 范围，首次部署/启用时重新评审 |
| Phase 1 正式退出结论 | 工程门禁完成，环境门禁对当前不存在的部署不适用但保留为自动触发条件 | 通过；1.12 已勾选，进度 24/49。该结论不批准部署、Phase 2、自助、shadow/RBAC 或 ACL enforcement |

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
