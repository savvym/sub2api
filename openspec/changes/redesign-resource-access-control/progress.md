# 当前进度

更新时间：2026-08-24

## 当前状态

- 当前阶段：Phase 0 安全评审 + Phase 1 权限基础设施。
- 当前分支：`codex/resource-access-control-foundation`。
- 基线提交：`58de21e70`（总体设计文档）。
- Foundation 提交：`215536582`，已推送 `origin/codex/resource-access-control-foundation`。
- Authorization Domain Contract 提交：`0d66334c9`，已推送同一远程分支。
- Fresh Setup Compatibility Bootstrap 提交：`4505b0301`，已推送同一远程分支。
- Policy 与 SQL Scope Foundation 提交：`75b6582c2`，已推送同一远程分支。
- Scoped Resource Reader 提交：`af49971aa`，已推送同一远程分支。
- RoleService Core 提交：`4a1617e8d`，Guarded Role Mode API 提交：`0201ae488`，均已推送同一远程分支。
- Trusted ActorResolver 提交：`34e43155c`，已推送同一远程分支。
- Trusted Runtime Actor Integration 提交：`631761288`，Admin Resource Actor Propagation 提交：`b55155000`，均已推送同一远程分支。
- Transactional Resource Mutation Coordination 提交：`5b9060048`，已推送同一远程分支。
- Bounded Authorization Propagation 提交：`1035f73a4`，已推送同一远程分支。
- Phase 1 CI Stabilization 提交：`2d203b601`，已推送同一远程分支；GitHub Actions [CI Run 32711471080](https://github.com/savvym/sub2api/actions/runs/32711471080) 与 [test job 97383587468](https://github.com/savvym/sub2api/actions/runs/32711471080/job/97383587468) 在完整 SHA `2d203b601c5d5b6578e91020bdbfbff4eb5bae6b` 上通过。
- Phase 1 CI Evidence 提交：`10e20f841`，已推送同一远程分支；[Draft PR #1](https://github.com/savvym/sub2api/pull/1) 已建立，base 为 `main`，保持 Draft 且不得在退出门禁完成前合并。
- 任务进度：20/49。1.12 当前已实现的工程验证项已通过：跨租户全链、当前管理员写面 TOCTOU、SQL Scope 生产规模 EXPLAIN、64 组 Standard/SIMPLE 配置、Admin API Key fail-closed、production role shadow 脱敏结构化日志，以及 CI/Testcontainers 的 228→current 升级/reapply、完整 repository integration 与 GitHub Security Scan 均已有自动化证据。正式 Phase 1 退出仍未批准，因此 1.12 保持未勾选；Phase 0 的 0.4/0.5/0.8、生产预检与凭据键名统计、目标环境 shadow 观察和批准人证据仍缺失。
- 当前权威行为仍为旧 `users.role` 与旧分组资格；核心管理员写虽已调用 Policy，但 legacy/shadow 下只保持现有 JWT Admin 与固定 Admin API Key Service Principal 行为，不得解释为 ACL/RBAC 已启用。

## 已完成

- 总体设计文档已完成并推送到远程设计分支。
- 本地 PostgreSQL、Redis、前后端依赖与开发连接已准备。
- 已决定用 OpenSpec 目录追踪长任务，按 Phase 独立分支/PR。
- 已冻结 V1 Owner、分享、撤权、计费、停用、group `0` 和迁移模式默认决策。
- 已建立帐号/分组写入口与引用 ID 覆盖清单、只读数据预检和模式兼容矩阵。
- 已新增 RBAC、Service Principal、用户/机器角色与权限种子；`platform.secret.export` 未授予任何内置角色。
- 已为帐号/分组增加 Owner、creator、public level、access version/mode，并新增 typed Grant 与 append-only authz event。
- 已同步 9 个 Ent Schema 和生成代码；存量表索引通过独立 `_notx` migration 并发创建。
- 已新增 5 个默认关闭开关和 `role_authorization_mode=legacy`；缺失、读取失败或非法值均 fail closed。
- 已将 credentials/extra 与自助出站安全清单补全为 Review Ready；仍需生产键名统计和安全/平台负责人批准。
- 已在本地初始化库只读运行 `data-preflight.sql`，所有异常查询为 0；该空实例结果不能替代真实服务器数据预检。
- 已新增独立 `internal/authz` 领域契约：可信 Actor、11 个能力、13 个动作、四级访问映射、typed provenance 和稳定拒绝类别。
- fresh setup 现在于单一数据库事务内串行完成管理员创建、`system_bootstrap` 兼容角色写入与校验；失败整体回滚，重复或并发执行不会创建第二个管理员。
- 新增 migration 232，为已经在 229 之后由旧 setup 创建的用户补齐/收敛兼容角色；只修改 `system_bootstrap` 拥有的 `admin/user` Grant，保留人工和其他 Service Principal Grant。
- setup 的 PostgreSQL 连接统一改为结构化 URL，修复空密码会吞掉后续 `dbname`、从而迁移到默认数据库的问题。
- Web、CLI 与 AutoSetup 已统一经过完整安装 advisory lock；同数据库的并发安装只有锁持有者可以创建管理员和写入配置，AutoSetup 不再维护重复流程。
- setup 与正常启动共用结构化 PostgreSQL DSN，实现空密码、特殊字符密码和时区参数的一致解析。
- 已新增 PolicyService 的 capability/create/resource 判定矩阵、稳定 provenance、Feature Flag 紧急关闭语义，以及 legacy/shadow group.use 单一权威保护。
- 已新增 PostgreSQL PolicyStore 与不可伪造 AccessibleScope；Account/Group view predicate 在 SQL 内重校验主体版本、角色版本、能力快照和当前开关，并覆盖 Owner、public、直接用户 Grant、角色 Grant与严格到期边界。
- Ent predicate 已在 PostgreSQL 18.6 动态验证外层参数编号、筛选、Count、分页和 stale-version fail closed；没有把未受 Scope 约束的资源加载进 Go 再过滤。
- 已新增独立 `ResourceReadService` 与 Account/Group scoped reader；SQL Scope 始终先于业务筛选、Count、白名单排序和分页，详情查询对不存在与不可见资源统一返回 not found。
- 普通资源读取使用专用窄投影与 DTO；帐号 credentials/extra/proxy/错误/额度/调度状态/关系字段、分组帐号拓扑/计数/价格/路由/利润字段均不会查询或序列化，`account_count` 排序显式拒绝。
- PostgreSQL 18.6 动态测试已覆盖 Owner、public、直接用户 Grant、角色 Grant、私有/过期 Grant、筛选后 total、稳定分页、窄 SELECT 和 stale subject fail closed，临时数据库残留为 0。
- 已新增 RoleService/RoleRepository 作为 legacy 用户角色变更的唯一 command path；管理员角色修改已接入，通用 `UserRepository.Update` 不再接受 Role 写入，所有生产用户创建路径都会补齐 `system_bootstrap` 兼容角色。
- legacy/shadow 角色变更在同一 PostgreSQL 事务中维护 `users.role`、兼容 `user_roles`、`users.authz_version` 与 API Key 缓存失效 Outbox；migration 233 补齐了纯授权版本变化的 durable invalidation。
- 角色约束覆盖 active admin actor、expected-role CAS、自我降级、最后一个 active admin、disabled 用户提升和角色/状态竞态；事务 advisory lock、稳定行锁顺序及 readiness 表锁顺序已通过真实并发验证。
- 内部 readiness/transition 状态机已检查 migration 229/232/233、系统角色、bootstrap principal、兼容角色一致性和版本；shadow 回 legacy 额外拒绝不可映射的 RBAC admin 与 Service Principal 角色，任何 RBAC transition 仍硬拒绝。
- 已新增专用 `GET /api/v1/admin/authorization/role-mode`，返回当前 mode、唯一允许的下一跳、稳定数组形式的 readiness blockers 与 `can_transition`；该只读入口使用 PostgreSQL read-only repeatable-read snapshot，不取得角色 command advisory/表锁，不要求 step-up，也不修改 mode。
- 已新增严格 payload 的 `POST /api/v1/admin/authorization/role-mode/transitions`，以认证上下文中的管理员和 `expected_mode` 执行 CAS；入口无条件要求近期、带非空 session ID 的 JWT TOTP step-up，不受全局 step-up 开关影响，并拒绝 Admin API Key、未知认证方式和 sid-less legacy JWT。
- 成功 transition 在 mode 更新同一 PostgreSQL 事务内写固定 action/method/path、JWT actor 快照和 previous/current mode durable audit；审计写入失败会回滚 mode。成功后 `SkipAudit` 避免中间件重复记录；CAS/readiness/请求/依赖失败不 skip，交由现有异步 AuditLog middleware 做 best-effort 尝试审计，不宣称失败记录 durable。
- 通用 settings PUT 继续禁止修改 `role_authorization_mode`；新 GET/POST 路由、AuthorizationHandler 和依赖已接入生产 Wire。只允许 legacy↔shadow，任何涉及 RBAC 的 status/transition 仍硬拒绝。
- 没有新增普通用户资源路由/UI，也没有将任何运行时授权切换到 ACL/RBAC。
- 1.7b 已通过完整 `make -C backend test-unit`、相关包 vet、backend build、Wire 编译、OpenSpec strict validate 与 diff check；本机 PostgreSQL 18.6 动态覆盖 GET 非阻塞 snapshot、成功 durable audit 恰好一条及 audit insert 失败原子回滚。
- 本地 HTTP 已验证未认证拒绝、GET readiness 的 `blockers: []`、未启用 TOTP 拒绝、Admin API Key 拒绝、成功 legacy→shadow→legacy、expected-mode 409 conflict 和两条成功 durable audit；烟测结束后 mode setting、TOTP、临时 Admin API Key、审计记录与临时测试库均恢复/删除。
- JWT、Optional JWT、管理员 JWT/WebSocket 现在都从数据库最新单快照解析 User Actor；Admin API Key 映射为固定 `admin_api_key` Service Principal Actor，首管理员仅保留为旧 Handler 的兼容 shim。
- `admin_api_key` 是零角色审计身份锚：migration 234 会清理同 code 的历史角色碰撞但不重新启用 disabled 主体，Resolver 对非零角色/能力快照继续 fail closed；该认证方式不能解析任意其他 Service Principal code。
- 管理面审计改为 Actor-first，并为 `audit_logs` 增加互斥的 Service Principal 主体、restrict FK、查询过滤和并发部分索引；机器请求不再继承首管理员 email/role。`actor_user_id` 保留既有无 FK 的历史兼容语义。
- 幂等数据库作用域按 durable Actor 隔离，用户与同数值 ID 的 Service Principal 不再冲突；升级前 raw scope 仅按当前/兼容完整 fingerprint 回放或回收，升级 fence 阻止旧、新实例同时取得同一副作用所有权。缺失或不一致 Actor 稳定返回 `503 AUTHORIZATION_UNAVAILABLE`，不再使用 `admin:0/user:0`。
- 幂等终态写回已与 HTTP request cancellation 解耦并保留 5 秒上限；兼容矩阵覆盖 raw legacy actor/payload、qualified canonical actor/legacy payload 和当前 canonical 记录，且 qualified-only 兼容不会放宽到 raw scope。migration 236 防止旧 cleanup snapshot 删除已续期 fence。
- restart 只在外层幂等成功落库、响应写出后且非 replay 时调度；前端明确 HTTP 失败会停止并显示错误，模糊断线才进入重启等待且消费 pending key。update/rollback/restart 的 system mutation 均携带 session-scoped 幂等 key。
- 已补真实 `ActorResolver -> PolicyService -> AccessibleScope -> ResourceReadService` 贯通测试；主体版本陈旧时在 scoped reader 前 fail closed。本切片没有注册普通用户帐号/分组路由，也没有启用 ACL/RBAC。
- 1.8 已通过完整 backend unit/vet/build、前端 lint/typecheck/build 与 237 files/1661 tests、OpenSpec strict validate、diff check；本机 PostgreSQL 18.6 动态覆盖 migration 234/235/236、续期/清理锁竞态和 AuthzPolicyStore snapshot，临时数据库残留为 0。
- 帐号、分组及 Claude/OpenAI/Grok/Gemini/Antigravity OAuth、CN Provider 管理入口现在都在解析 path/query/body、检查依赖或执行副作用前取得可信 Actor，并显式传给 Actor-aware service facade；静态 capabilities、runtime sanity 和默认模型映射保持明确排除。
- 为避免经关联入口绕过，User/API Key 的 Group 关系、Channel CRUD、Payment Plan、Redeem、Settings 默认订阅、Content Moderation 配置/API Key 测试、Channel Monitor CRUD/模板与 Ops alert rule/silence 也已接线。缺失或 JWT subject 不一致稳定返回 `503 AUTHORIZATION_UNAVAILABLE`，仓储与外部副作用为零。
- service facade 首语句只验证当前管理员 Actor 是 JWT User 或 Admin API Key Service Principal，随后委托既有业务方法；旧管理员成功响应和全局资源行为保持不变，没有启用 Policy/ACL/RBAC，也没有新增普通用户路由。
- 1.9 已通过完整 `make -C backend test-unit`、定向 race、相关包 vet、默认标签全仓编译、backend build、OpenSpec strict validate 与 diff check；注册路由 AST 门禁和 malformed-input 运行时测试共同锁定入口 fail closed 与 Actor 向后传递。
- 已新增生产 `ResourceMutationCoordinator`：使用 `SERIALIZABLE` 事务，按固定顺序锁定 Actor、角色与 Account/Group；事务内重新解析 JWT User 或固定 `admin_api_key` Service Principal，并比较主体版本、角色版本、能力快照和认证方式。已有 Ent 事务无法证明隔离级别时 fail closed。
- 公开 `NewAdminService` 缺少 coordinator 时使用不可用哨兵，核心写 facade 不会回退 legacy 直写；API contract 已显式注入可工作的 Resolver、Policy 与事务 repository。
- Account/Group 核心创建、复制、更新、批量、删除、关系和运行态命令，以及 Composite Route，已接入事务内 `CanCreate`/`Authorize` 与 expected `access_version` 校验。Batch delete 与 batch credentials 现在各使用单一原子 service 命令；批量目标任一不可见或版本变化时整体拒绝。重复复制 replay 与空操作不递增版本、不写事件、不设置 durable marker，也不执行提交后回调。
- 业务写、每个实际变更资源的 `access_version`、append-only `resource_authorization_events` 和适用 Scheduler Outbox 共用外层事务；Group/API Key 相关 Auth Cache Outbox 由数据库 trigger 在同一事务产生。audit/outbox 任一失败会整体回滚。
- durable resource event 仅记录资源、Owner 快照、User/Service Principal Actor、auth method、event type、版本、request ID、字段类别和固定 `result=success`，不记录凭据值或 request body。通用 `audit_logs` 仍由 middleware 异步记录，不能用 resource event 取代。
- migration 237 扩展 Group 授权缓存失效字段覆盖，Outbox 只持久化 API Key SHA-256，不保存明文；静态 contract 与本机 PostgreSQL 18.6 隔离库动态测试均已通过，覆盖 25 类授权快照字段、cosmetic silent 和事务 rollback，临时数据库残留为 0。
- 本地 Redis/缓存/网络动作延迟到数据库提交后；单个 callback panic 被隔离，不会把已提交写伪装成 HTTP 失败，也不会阻止后续 callback。
- 平台资源由 JWT 管理员创建时记录 `created_by_user_id`；Admin API Key 创建时 creator 保持为空，通过 Service Principal durable event 归因，不伪造自然人。
- 1.10 已通过完整 `make -C backend test-unit`、聚焦 authz/service/repository/handler race、相关 vet、默认标签全仓编译、integration 标签全仓编译、backend build 和 migration 237 PostgreSQL 18.6 动态测试；该阶段本机因无 Docker 未运行 repository Testcontainers 动态套件，后续已由 CI Run `32711471080` 补齐。
- migration 238 新增 `authorization_expiry_jobs`、四类来源 trigger/backfill 和零角色 `authorization_expiry_coordinator` Service Principal；数据库 Policy/Scope 使用 `statement_timestamp()` 严格拒绝已到期角色与 Grant。协调器以可恢复租约处理到期，在同一 `SERIALIZABLE` 事务中递增主体/资源版本、写 durable audit/resource event，并为 Account/Group Grant 到期产生 Scheduler 事件。
- Scheduler Outbox 已改为 PostgreSQL `SKIP LOCKED` claim、lease token fencing、ack/retry 和过期租约恢复，不再以 Redis watermark 决定消费所有权；durable delivery 对 bucket/lifecycle/full rebuild 使用 strict 语义，锁忙或 fencing 会 retry 而不是 ACK。migration 239 的新增 CHECK 使用 `NOT VALID`，claim 索引拆到 migration 241 以 `_notx` `CREATE INDEX CONCURRENTLY` 在线创建，runner 会清理同名 invalid index 后重试。
- Auth Outbox 固定优先处理 stage 0，并分别观测 primary 与延迟 safety pass，停机时以 detached 2 秒 context 释放未结清 claim；service 只传相对 delay，second pass/retry 的 `available_at` 由数据库 `statement_timestamp() + interval` 生成。
- API Key allow snapshot 提升到 v22 并拒绝 v21 及更早快照；首次正向 L1/L2 写入同时受 jitter 后 30 秒上限和不可序列化的进程内 monotonic deadline 约束。正向 L2 命中不提升到 L1，Redis 相对 TTL 保持跨实例权威；缺失、过期或未来时间戳只视为 miss 并回源，不删除或广播可能并发刷新的值。传播统计使用数据库时间分别报告 Auth primary/safety、Scheduler 与 Expiry 的 pending、ready 和 oldest lag。
- `AuthorizationPropagationGuard` 将 5 秒作为健康目标、30 秒作为扩大权限安全门；统计不可用、必需 Worker 缺失/停止、到期 coordinator disabled/缺失/带任意角色或相关 lag 达到安全线时稳定 fail closed。Settings 只在有效状态扩大时调用该门，关闭功能和撤权不被积压阻塞；ResourceMutation 已提供显式 `ExpandsAccess` 契约，当前尚无 Grant 管理生产命令。
- 新增 `GET /api/v1/admin/ops/authorization/propagation/health`，暴露数据库时间、队列、Worker、`expiry_coordinator_ready`、5 秒/30 秒判断与稳定降级原因；该安全入口不受可选 Ops monitoring 开关限制，Ops disabled 时仍返回 fail-closed 健康状态。1.11 聚焦 unit/race/vet、默认与 integration 标签编译，以及本机 PostgreSQL 18 的到期 exact-once、租约恢复、事务回滚、锁序和 Scheduler commit-order 场景已通过；该阶段本机未运行的 Docker/Testcontainers repository 动态套件后续已由 CI Run `32711471080` 补齐。
- 1.12 新增真实 Resolver→Policy→Scope→reader 的跨租户 PostgreSQL 矩阵，覆盖 User/Service Principal、Account/Group、搜索、排序、分页、total、404 IDOR、开关/角色版本/停用重校验、固定 Admin API Key legacy/shadow 旁路及 code/status 失效。
- 稀疏 SQL Scope 已从逐行关联 OR 改为同一语句内的可索引候选 ID `UNION ALL`；20,000 行资源和大规模无关 Grant 的 PG18.6 EXPLAIN 门禁证明 Account/Group 的 Owner、public、direct-user、role-grant 索引均被采用，主资源表不走 Seq Scan。legacy admin/platform capability 全局旁路保留同一语句快照重校验，但不再生成资源候选自扫描；Account/Group 计划均确认资源关系只出现一次。migration 242 在线补齐 public scope 部分索引及 invalid-index retry。
- SIMPLE Mode 现在同时在 SettingService、生产 PolicyStore 和 Scope 分支 fail closed；即使五个数据库原始开关全为 true，普通用户仍不能生成 Owner/ACL Scope，legacy 管理员治理保持兼容。Standard/SIMPLE × 五个 Feature Flag 的 64 组机械矩阵与 canonical role mode 解析已覆盖。
- 当前 Phase 1 管理员写面新增真实双事务 TOCTOU 回归：两个不同管理员 Actor 携带同一 `access_version` 并发写，恰一提交/一版本冲突；SERIALIZABLE 重试使 mutation closure 可执行 1 或 2 次，竞争 loser 的事务尝试会完整回滚，最终业务状态、版本、durable event 和 Scheduler Outbox 均恰好一次。真实 `AdminService.ClearAccountError` PostgreSQL 测试证明生产 after-commit callback 只在提交后执行且恰好一次，通用 commit/rollback/panic 语义另由 coordinator 单测覆盖；普通 Owner/Grant 写尚未开放，其完整并发栈仍属于 Phase 2/3。
- production `PolicyService` 的 capability/create/resource/scope 四个入口在 shadow mode 并行计算 legacy 与 RBAC，始终返回 legacy 结果；管理员 JWT 与固定 Admin API Key 生产认证入口已接入 Policy。差异以不含主体、角色、Grant、资源 ID 的低基数结构化日志记录，行为差异为 WARN、等价比较为 INFO，并隔离 observer panic；日志可由外部系统聚合，当前没有独立进程内指标计数器。
- 新增 Testcontainers 持久升级回归，从 migration 228 分段推进至当前版本并重复 `ApplyMigrations`，覆盖存量数据、seed/backfill、触发器、在线索引和幂等；本机无 Docker，随后由 GitHub Actions 完整 integration suite 动态验证通过。
- GitHub Actions push Run `32711471080`（attempt 1）在 SHA `2d203b601c5d5b6578e91020bdbfbff4eb5bae6b`、Ubuntu runner 与 Go 1.26.6 上执行 `make test-integration`，实际为 `go test -tags=integration ./...`；PostgreSQL `18.1-alpine3.23`、Redis `8.4-alpine` Testcontainers harness 成功，`internal/repository` 非缓存运行 `41.430s`。该命令没有 `-run` 过滤，因而包含 228→current 持久升级/reapply 回归。
- Draft PR #1 已提供统一评审入口；分支相对 `origin/main` ahead 17/behind 0，merge-tree 可干净合并，但改动规模为 477 个文件，因此必须按提交顺序评审并保持 Draft。
- GitHub `Security Scan` workflow 已从当前 fork 的 `disabled_fork` 状态启用为 `active`；SHA `1523aa1740140b6c7de9ae5553545856f141b889` 的 [push Run 32727879905](https://github.com/savvym/sub2api/actions/runs/32727879905) 与 [PR Run 32727886870](https://github.com/savvym/sub2api/actions/runs/32727886870) 均通过。两个 run 的 backend `govulncheck ./...` 均报告可调用路径 0 个漏洞，frontend production audit 例外校验均通过。
- 新增只读 `credential-key-preflight.sql`，仅聚合 credentials/extra 的键名、平台/类型、软删除状态、帐号 status、JSON shape 和计数，不读取值或帐号 ID；当前没有生产连接配置，尚未执行或归档真实数据结果。

## 下一步

1. 由平台/认证/安全负责人复核并批准 0.4 credentials/extra 清单和 0.5 自助平台/出站 allowlist。
2. 对批准的生产只读副本运行 `data-preflight.sql` 与 `credential-key-preflight.sql`，分别归档数据异常/回填规模和 credentials/extra 键名统计。
3. 在目标环境运行 role-mode readiness，按批准方案推进 legacy→shadow，聚合 production shadow 结构化日志并记录具体差异指标、日志量与 sink `dropped_count`、观察窗口和回滚结果；完成 Draft PR 评审及平台/认证/安全批准后，才能勾选 0.8/1.12 并进入 2.1。

## 阻塞与风险

- 1.12 当前工程测试、CI/Testcontainers、Security Scan、production role shadow 代码和 Draft PR 建立不等于正式 Phase 1 退出；仍缺 Phase 0 批准、生产预检与凭据键名统计、目标环境 shadow 观测和最终批准人，任务必须保持未勾选。
- 本地 `sub2api` 只是空测试实例，仅有 1 个管理员和 1 个平台默认分组；本地预检不能替代真实服务器只读报告。
- Phase 0 的 credentials/extra 与自助出站文档均为 Review Ready、尚未 Accepted；这是开放自助托管前的硬阻塞。
- fresh setup 缺失兼容角色的问题已修复，本地管理员也已由 migration 232 补齐；真实服务器升级后仍必须通过专用 GET status/readiness 入口验证全量一致性。
- 分组名称唯一索引本切片不修改，先完成大小写和 Owner 范围冲突预检。
- 1.6 scoped reader 与 ActorResolver 已完成真实贯通测试，但尚无普通用户 Handler/路由；当前仍是 dark foundation，不能作为已开放的普通用户资源读取入口。
- `role_authorization_mode` 当前仅由 production Policy shadow 比较链路消费，管理员认证响应在 shadow 下仍由 legacy 决定；部署环境必须先运行 readiness，并在正式观察窗口前保持 `legacy`。本地烟测已恢复为缺失 setting 的 legacy fallback。
- 1.7b 没有解除 RBAC 硬拒绝；1.8 ActorResolver 和管理员认证 shadow consumer 已完成，但其余授权 consumer 与外部门禁仍未完成，不能把 legacy↔shadow 管理入口或本地 shadow 记录能力解释为 RBAC 已交付。
- 通用管理员 settings PUT 仍跨多个服务，不是单一数据库事务；`role_authorization_mode` 已从该路径移除，其余新开关当前没有 consumer，因此不阻塞 dark launch，但启用任何授权 consumer 前仍需独立原子配置命令。
- 幂等升级 fence 允许新实例对新业务记录使用 Actor-qualified scope，并使仍写 raw scope 的旧实例 fail closed；混合版本期间旧请求可能收到冲突，因此发布仍应优先同版本切换或维护窗，不能宣称无感滚动升级。
- 1.8 新增并发用例已通过定向 race；扩大到相关包的 race 命令仍会命中 `grok_import_probe_test.go` 和 `channel_monitor_checker_body_test.go` 两处不在本次 diff 的既有测试辅助代码竞态，不能宣称全量 race 已通过。
- 1.10 只完成核心 Account/Group 管理写命令的事务内 Policy、版本、durable event 与适用 Outbox；读取、普通用户入口、ACL/RBAC 权威切换和旧分组资格 consumer 均未改变，不能把本切片解释为资源分享已开放。
- OAuth/privacy/probe 等外部网络动作无法与 PostgreSQL 形成分布式原子事务；Privacy 的本地持久化已作为独立 ResourceMutation 重新授权并写版本/事件，但不能宣称上游副作用可因本地提交失败而回滚。
- 1.11 的 5 秒是传播健康目标，30 秒是禁止扩大权限的安全线；它们不是对尚未迁移的数据面、WebSocket 或异步任务作出的端到端 SLA 承诺。权威 Policy/Scope 会按数据库时间同步拒绝到期来源；API Key 旧 allow snapshot 由 v22 拒绝 pre-v22 数据、首次写入 monotonic deadline、Redis 相对 TTL、rewrite 不续期和正向 L2 不提升 L1 共同约束在 30 秒内。
- 1.11 对 Account/Group Grant 到期只递增资源版本、写 durable event 并产生 Scheduler 事件；完整 `account_groups` 授权来源/验证版本扩展及撤权、到期、角色变化后的关系闭包重算仍属于任务 4.2/4.4，不能把 Scheduler 事件等同于关系已重算。
- `ResourceMutationCommand.ExpandsAccess` 已建立 fail-closed 契约，但当前没有 Grant 管理生产命令；后续新增或恢复授权路径必须显式标记，不能依赖调用方默认值绕过传播门。
- Usage/Ops/Dashboard 等派生 scope、RateLimit/CRS/probe 等专用后台写、Channel Monitor Run/History/worker、Ops alert event/evaluator 与 Payment retry/refund 事务履约仍待后续切片；新增全新资源前缀时仍必须同步扩展入口分类和事务覆盖清单。

## 续作检查

开始下一次开发前依次执行：

```bash
git status --short --branch
git log -5 --oneline --decorate
openspec status --change redesign-resource-access-control
openspec validate redesign-resource-access-control --type change --strict --no-interactive
```

然后读取 `tasks.md` 中未完成的最前置依赖和 `implementation-evidence.md` 的最新记录。
