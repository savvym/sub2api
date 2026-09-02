# 当前进度

更新时间：2026-09-02

## 当前状态

- 当前阶段：Phase 0 与 Phase 1 已按无部署、单维护者、默认关闭的 dark-foundation 范围完成；Phase 2 任务 2.1-2.5 已完成，下一切片为 2.6。2.2/2.3 已交付普通用户私有 Account/Group CRUD、窄字段投影和简化 UI，2.4 已把管理员创建与 OAuth/import/copy/batch/callback 收敛到可信 Owner/Actor，2.5 已让自助 Account 创建按需创建或复用 Owner 的 `<platform>-default` 私有分组，并把 Account、优先级 50 的绑定、可调度状态、Scheduler Outbox 与 durable event 放在同一事务提交。但生产产品目录仍为空、有效 self-service 开关仍关闭，2.6 的 group `0`、平台默认组和 SIMPLE Mode 租户隔离尚未完成。该进展不构成 `Release Accepted`，不启用任何自助平台、OAuth、ACL/RBAC authority 或 Feature Flag。
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
- Phase 1 CI Evidence 提交：`10e20f841`，已推送同一远程分支；[Draft PR #1](https://github.com/savvym/sub2api/pull/1) 已建立，base 为 `main`。退出门禁完成前不得合并；当前门禁已按无部署范围收口，但 PR 仍按维护者决定保持 Draft，不自动转 Ready 或合并。
- Main Integration 提交：`e63f0859c` 合并 `origin/main@027d442f9`。OpenAI/Grok 手动管理入口的 trusted Actor 传播与 upstream quota workflow/plugin/Wire 变更已同时保留；`origin/main` 现为当前分支祖先。
- Main Integration Stabilization 提交：`fcdba5537` 修复 Grok typed-nil prober，`8db5da13d` 记录 main 集成与后台 Actor 缺口，`d47ca4bea` 使管理员路由 AST 门禁按 Go build constraints 解析。当时的 main-integration 代码 SHA 为 `d47ca4bea567f778c6356a761344f376ca471ea4`；该称谓不包含随后完成的 migration 243/auto-reset hardening。
- Latest Main Rebase：2026-09-02 将 24 个分支提交线性 rebase 到 `origin/main@efb46db0a`。冲突收口保留主线的 upstream model catalog 与共享 OpenAI quota post-process，并继续通过 Actor-aware facade 传播管理员 User/Service Principal Actor；Ent/Wire 已重新生成。加入 hardening 与 CI 修复后，最终代码提交 `aeb967ebe` 相对该主线 ahead 26/behind 0。
- OpenAI Quota Auto-Reset Hardening 提交：`7acf5a0dd`；CI 收口提交：`aeb967ebe`。首次 push/PR CI 暴露 6 项 lint 与 shadow recovery fixture 的 `quota_dimension` 约束问题，修复后当前代码 SHA 的 push/PR CI、完整 Testcontainers、lint 与 Security Scan 均通过。
- Private Self-Service Group CRUD 提交：`bf19faedf`，完整 SHA `bf19faedf2bf2b4920d61e7058ae95eabb5d487e`。GitHub Actions [PR CI Run 33649130206](https://github.com/savvym/sub2api/actions/runs/33649130206) 的 [test job 100311347354](https://github.com/savvym/sub2api/actions/runs/33649130206/job/100311347354) 与 [push CI Run 33649126676](https://github.com/savvym/sub2api/actions/runs/33649126676) 的 [test job 100311332689](https://github.com/savvym/sub2api/actions/runs/33649126676/job/100311332689) 均通过；无过滤 integration step 分别为 3m35s 和 3m33s。对应 [PR Security Scan 33649130214](https://github.com/savvym/sub2api/actions/runs/33649130214) 与 [push Security Scan 33649126817](https://github.com/savvym/sub2api/actions/runs/33649126817) 也成功。
- Trusted Account Creation and OAuth Callback Binding 提交：`a875536cc`；lint 收口提交：`bf4903ab9`，完整 SHA `bf4903ab92095acc4f11cc477cc7777c14d53d8f`。首次 SHA 的 push/PR test、frontend、shell 与 Security Scan 均成功，但 CI 因 Gemini OAuth 的 SA4009/ST1005 lint 失败；修复后 [push CI Run 33659274194](https://github.com/savvym/sub2api/actions/runs/33659274194) / [test job 100345512840](https://github.com/savvym/sub2api/actions/runs/33659274194/job/100345512840) 与 [PR CI Run 33659279649](https://github.com/savvym/sub2api/actions/runs/33659279649) / [test job 100345528974](https://github.com/savvym/sub2api/actions/runs/33659279649/job/100345528974) 均成功；无过滤 integration step 分别为 3m40s 和 3m10s，repository 分别非缓存运行 52.288s 和 36.991s。对应 [push Security Scan 33659274223](https://github.com/savvym/sub2api/actions/runs/33659274223) 与 [PR Security Scan 33659279961](https://github.com/savvym/sub2api/actions/runs/33659279961) 也成功。
- Owner Private Default Group Binding（2.5）当前工作区已完成：每个 Owner/平台只复用或创建一个大小写不敏感名称为 `<platform>-default` 的 active/private/exclusive/legacy Group；首次创建同时检查 Account 与 Group 容量，已有默认组只检查 Account 容量。Account 固定 `schedulable=true` 并以优先级 50 绑定该 Group，Group/Account、两类 Scheduler Outbox 和 durable event 任一失败整体回滚。本地默认/unit/race/vet/build/lint 与 integration 标签编译已通过；本机没有 Docker，当前提交的无过滤 Testcontainers 与 Security Scan 待推送后补录。
- 任务进度：29/49。2.1 已新增 hoster 角色资格、Account/Group 配额、管理员分配入口和事务绑定容量锁；2.2 已新增普通 JWT 用户的私有 Account CRUD；2.3 已新增普通 JWT 用户的私有 Group CRUD 与 UI；2.4 已新增 server-owned Account creation authority、五类 OAuth session Actor/Owner/proxy 绑定、callback 一次性消费和管理员创建 sink 的平台 Owner 收敛；2.5 已完成 Owner 私有默认组按需创建/复用及 Account 同事务绑定。group `0`/平台默认组/SIMPLE Mode 租户隔离、出站安全发布证据和 Phase 2 E2E 仍由 2.6-2.7 完成。当前没有 production/staging、真实数据或旧 Worker；已有远端证据不替代未来生产预检、maintenance/drain、shadow 观察或 self-service `Release Accepted`。
- 当前权威行为仍为旧 `users.role` 与旧分组资格，全部新增 Feature Flag 关闭且 `role_authorization_mode=legacy`。2.2/2.3 已注册普通用户 Account/Group 路由和 UI，2.4 只加固既有管理员创建/OAuth/import/callback 路径并保留自助 JWT Owner 契约；后端读取/产品/写入仍分别受可信 JWT Actor、Backend Mode、Policy/Scope 有效开关、hoster 资格和事务内配额约束，前端路由与侧栏也只读取后端暴露的有效 self-service 值。生产 Account/Group 产品目录均为空，因此当前部署配置不能创建任何自助资源，也不得解释为 ACL/RBAC 已启用。

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
- credentials/extra 与自助出站安全清单已在 sole maintainer、无部署范围达到设计级 `Decision Accepted`：未知键和 `header_overrides` fail closed、管理员明文导出关闭、当前自助 allowlist 为空且 OAuth 禁止。生产键名统计、实现证据与泄漏测试仍属于独立的 Phase 2 `Release Accepted` 门禁。
- `data-preflight.sql` 以 `REPEATABLE READ READ ONLY` 事务运行，并包含两份相互独立的 auto-reset inventory：五列 provenance inventory 只输出 `idempotency_record_id/status/scope_kind/account_ids/provenance_state`，三列 terminal-recovery inventory 只输出 `idempotency_record_id/account_id/response_state`；两者均不输出 key hash、fingerprint、response body、attempt hash 或凭据。当前 PostgreSQL 定向回归已执行两份查询；因不存在真实服务器或真实数据，首次导入/升级现有数据库前必须执行并归档，不能用本地 fixture 结果替代该未来门禁。
- 已新增独立 `internal/authz` 领域契约：可信 Actor、12 个能力、13 个动作、四级访问映射、typed provenance 和稳定拒绝类别；第 12 个专用能力由 migration 243 只授予 roleless OpenAI quota auto-reset Worker。
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
- production `PolicyService` 的 capability/create/resource/scope 四个入口在 shadow mode 继续并行计算 legacy 与 RBAC。`CheckCapability`、`Authorize`、`AccessibleScope`，以及管理员 JWT/Service Principal 的 `CanCreate` 仍返回 legacy；唯一例外是普通 JWT 用户的 Account/Group `CanCreate` 返回 RBAC 结果。全部入口仍记录不含主体、角色、Grant、资源 ID 的低基数差异，observer panic 不影响授权响应。
- 新增 Testcontainers 持久升级回归，从 migration 228 分段推进至当前 migration 243 并重复 `ApplyMigrations`，覆盖存量数据、seed/backfill、触发器、在线索引和幂等；本机无 Docker，当前代码 SHA `aeb967ebe` 的 push/PR GitHub Actions 已以无过滤完整 integration suite 动态验证通过。
- main 新增 `229_plugins.sql`/`230_plugin_artifacts.sql`，与 authz 的 229/230 migration 共用数字前缀。runner 以完整文件名和 checksum 记账，不能通过重编号规避；`TestSharedMigrationNumberLineagesConverge` 覆盖 main-first、authz-first 两条已部署历史在两次完整 `ApplyMigrations` 后收敛。当前代码 SHA 的 push/PR CI 均以无 `-run` 过滤的 integration suite 非缓存执行 repository 包并通过，动态证据覆盖 migration 243。
- GitHub Actions push Run `32711471080`（attempt 1）在 SHA `2d203b601c5d5b6578e91020bdbfbff4eb5bae6b`、Ubuntu runner 与 Go 1.26.6 上执行 `make test-integration`，实际为 `go test -tags=integration ./...`；PostgreSQL `18.1-alpine3.23`、Redis `8.4-alpine` Testcontainers harness 成功，`internal/repository` 非缓存运行 `41.430s`。该命令没有 `-run` 过滤，因而包含 228→该 SHA 当时所含版本的持久升级/reapply 回归。
- [Draft PR #1](https://github.com/savvym/sub2api/pull/1) 已提供统一评审入口；2026-09-02 代码采证时 base OID 为 `efb46db0a960fdad94502b1c3a982a0051cf5245`，代码证据 OID 为 `aeb967ebe0d9ed9aa5b43f0f9e60dc030f3839e6`，GitHub 报告 `MERGEABLE/CLEAN`。Phase 1 无部署范围退出后，PR 仍按维护者决定保持 Draft，不自动转 Ready 或合并。
- 当前代码 SHA `aeb967ebe` 的 GitHub `Security Scan` [push Run 33608505242](https://github.com/savvym/sub2api/actions/runs/33608505242) 与 [PR Run 33608511032](https://github.com/savvym/sub2api/actions/runs/33608511032) 均通过。两个 backend `govulncheck ./...` 均报告代码受影响漏洞 0 个，两个 frontend production audit 例外校验均通过。
- 当前代码 SHA `aeb967ebe` 的 GitHub `CI` [push Run 33608505225](https://github.com/savvym/sub2api/actions/runs/33608505225) 与 [PR Run 33608510880](https://github.com/savvym/sub2api/actions/runs/33608510880) 均通过；test job `100177927975`/`100177945050` 以 Go 1.27.0 运行完整 unit/integration，integration step 均为 `3m38s`，repository integration 分别非缓存运行 `50.632s`/`49.677s`。golangci-lint v2.13.2 两边均为 0 issues。
- 新增只读 `credential-key-preflight.sql`，仅聚合 credentials/extra 的键名、平台/类型、软删除状态、帐号 status、JSON shape 和计数，不读取值或帐号 ID；当前没有生产连接配置，尚未执行或归档真实数据结果。
- OpenAI quota auto-reset 已改为固定、roleless、可停用的 Service Principal Worker；专用 WorkerPolicy 要求主体 active、零角色且恰好一个 direct permission，并在 scanner、帐号读取、quota 查询/缓存、幂等 repository 操作、reset、恢复和状态写入等新操作前逐次解析及重授权。普通新执行先完成初次加载、quota query 后重载、幂等 claim 前第三次 mutable eligibility reload；每次 POST 前还有第四次、位于 proxy/account 锁内的 eligibility 与完整 upstream identity 重检。任一条件变化都会在尚未发出的 reset 前停止。
- recovery 不复用普通 enabled-account offset pager。scanner 先用独立 `id > after_id ORDER BY id LIMIT` keyset pager 扫描结构可达的 `resetting` 及带任一 attempt hash 的 `failed` 帐号，故意不依赖 enabled、帐号 status 或 schedulable，并保留 malformed identity 让运行时 fail closed。recovery candidate 使用可取消的阻塞式、去重入队，队列满时等待容量而不丢弃，避免持续失败的低 ID 在每轮扫描中饿死后续帐号；请求热路径的 `Notify` 仍保持非阻塞。
- managed-state key 非空时使用 strict parser：拒绝未知字段和错误 JSON 类型，status 只允许 `checking/available/resetting/success/no_credit/failed`，attempt credit/cycle hash 必须同时为空或同时为 24 位小写十六进制，`resetting` 必须携带该 hash 对，`available_count` 必须是 `0..2147483647` 的整数。解析失败在任何普通 query/reset 前返回 reconciliation-required，不会把坏 state 当成无状态后重新消费。
- 每次帐号评估开始先取得 PostgreSQL transaction advisory lease；在 claim 与发布 `resetting` 的前半程只持 advisory lock。每次真正发出上游 POST 前，guard 才按 proxy `FOR SHARE` → account `FOR UPDATE` 的顺序短暂升级，在锁内复核 proxy 未切换、完整 eligibility，以及 credential/auth、ChatGPT account/FedRAMP、proxy ID/URL fingerprint 等完整 upstream identity。POST 返回立即释放事务与行锁；agent task recovery 后的再次 POST 必须取得新 lease 并重新执行全部复核，不把帐号/代理行锁带入 post-process。
- auto-reset 新执行使用 Service Principal-qualified 幂等 scope 与稳定 redeem request ID。上游 reset 返回后若 Worker 被撤权，恢复/query/cache/load/update 等新副作用继续 fail closed，但已发生的上游效果仍由不重新授权的 PostgreSQL 原子终结器把唯一 SP `recovery_deferred` audit 与 `processing -> succeeded` 作为同一事实提交；服务随后返回授权错误，恢复授权后只按已持久化 attempt hash 执行 recovery-only，不会第二次 reset。外部调用一旦开始后的 timeout、终结失败或过期 `processing` 也不自动重发。
- migration 243 以 `openai_quota_auto_reset_protected_attempts` 持久 marker 和 `openai_quota_auto_reset_protection_backfill(completed=TRUE)` 精确一次 sentinel 冻结 migration snapshot 中的历史 SP/account 及可识别 raw `processing|failed_retryable` 模糊结果。marker 固化正 `account_id` 且故意不引用 accounts FK：account scope 直接解析，raw/旧 SP scope 以帐号 `extra` 中持久 attempt credit/cycle hash 重算稳定 key；0 或多匹配会以 `check_violation` 使迁移整体回滚，不能猜审计归属。所有旧 account-qualified 记录（包括 succeeded replay）迁到保留 Worker SP scope，snapshot-protected valid raw/任意旧 SP attempt 也归一到同一保留 SP；raw fence、malformed raw 和 post-snapshot current retryable 不迁移、不标记。
- protected attempt 只有两个 owner-only 协调结果，两个 `SECURITY INVOKER` 函数均撤销 `PUBLIC EXECUTE`，且调用方 account、marker provenance、audit path/extra 必须精确一致：confirmed-success 强制专用 deterministic decision ID `reconcile-success:<record_id>`，同事务写/核验 SP audit、把 marker 转为 reconciled tombstone并执行唯一 `processing -> succeeded`，由数据库将终态固定保留 8 天；即使过期，同一 stable-key 的帐号 managed state 仍为 `resetting|failed` 时 cleanup 也继续保留父记录/tombstone。confirmed-no-effect 强制 `reconcile-no-effect:<record_id>`，只在全部旧 Worker 已停止排空且已正面确认上游无效果时，原子写 409 SP audit（`reconciled_no_effect`、`windows_reset=0`）并删除 marker/父记录，exact retry 只核验既有 audit。两种 decision ID 都不得复用 redeem request ID。`p_old_fleet_drained=TRUE` 只是调用方断言，函数不能探测 fleet；no-effect 批准前必须归档 shutdown/drain 与 upstream no-effect 证据，并在 audit extra 记录不含凭据的 evidence ref 和 decision owner。discard 仅在帐号当前 managed state 的 stable-key hash 精确匹配父记录时清除该 state，不同或更新 state 保留。unknown 不得猜。迟到旧 status-only succeeded/failed 在 success 前后都拒绝；与 discard 并发等待的旧 UPDATE 在删除提交后影响 0 行。未协调 cleanup 跳过，可清理的已协调终态过期时 trigger 同步删除 tombstone/父记录；trigger 缺失/禁用时 `ON DELETE RESTRICT` fail closed。raw reapply 不改变 post-snapshot current retryable。旧 account scope 写围栏与上述两阶段帐号 lease 继续阻止重复消费。
- 两个协调函数还由 SQL 强制 audit extra 的 account ID、record ID、request fingerprint 精确一致，`evidence_ref` 必须为 trim 后 1..256 字符的 string，`decision_owner` 必须为 trim 后 1..128 字符的 string；缺失或不匹配会使整条事务回滚。confirmed-success 当前唯一入口是八参数 `reconcile_openai_quota_auto_reset_protected_attempt(bigint,text,text,bigint,timestamptz,text,integer,jsonb)`；raw reapply 显式删除两个已废弃的 caller-controlled outcome/expiry overload，并校验它们不会被 reapply 复活。
- 当前本机 PostgreSQL 18.6 已实际通过 `go test ./migrations -count=1`、完整 `go test -tags=integration ./migrations -count=1`（23.704s）、actor migration + 两份 data-preflight 定向 PG 测试，以及 finalizer 的 5 个 PG tests。该证据与当前代码 SHA 的远端 CI/Testcontainers/Security Scan 已共同完成工程采证；当前没有可执行生产预检或 shadow 观察的环境，这些要求保留为首次导入/升级/启用触发门禁。
- migration 244 新增 `user_hosting_entitlements`，以一用户一行的 CAS 版本保存 Account/Group 配额；hoster 资格仍只由系统 `user_roles` 中的 `hoster` 角色决定，配额 `0` 表示没有创建容量。SQL、Ent 生成 Schema、显式 FK 名称与删除策略已由契约测试锁定。
- 新增管理员 `GET/PUT /api/v1/admin/authorization/hosting-entitlements/:user_id`。PUT 只接受完整严格 payload，要求带 session ID 的近期 JWT TOTP step-up，以目标版本执行 CAS，并在同一事务内重解析 active legacy admin、修改 hoster 角色/配额和写成功 durable audit；失败整体回滚，no-op 不伪造成功变更审计。
- hoster 角色新增、撤销或临时授权转永久会递增目标 `users.authz_version` 并触发现有 API Key cache invalidation Outbox；纯配额变化只递增 entitlement version。管理员可把配额降低到当前使用量以下，已有资源保留，后续创建同步拒绝。
- 新增事务绑定的 `HostingCapacityGuard`。Account/Group 创建调用方必须在持有 `SERIALIZABLE` 数据库事务时完成资格、Policy、实时用量与配额检查，并保持 entitlement/user/role 锁直到资源写入结束；其他隔离级别 fail closed，并发创建不能共同越过同一容量。2.1 未注册普通用户资源路由，也未启用任何平台、OAuth 或 Feature Flag。
- 新增普通 JWT 用户 `GET/POST /api/v1/accounts`、`GET /api/v1/accounts/products`、`GET/PATCH/DELETE /api/v1/accounts/:id`。Handler 在解析业务请求前要求可信 User Actor 与 `AuthSubject` 一致，查询和 JSON body 使用字段白名单、单值参数及 `DisallowUnknownFields`，Admin API Key、主体不一致、未知字段和尾随 JSON 均 fail closed。
- list/get 复用 `ResourceReadService` 与数据库 `AccessibleScope`，Owner/private 可见性、主体/角色/开关版本重校验、筛选、total、排序和分页都在 SQL 范围内完成；不可见详情与非 Owner 写统一 conceal 为 not found。HTTP 窄投影仅包含标识、名称、平台、类型、状态、`credential_configured` 布尔值、`owned_by_me`、公开级别和时间戳，不返回凭据、Owner ID、extra、proxy、运行时状态或关系。
- 创建只接受服务端产品 ID、名称和 API Key，客户端不能提交 platform、认证类型、endpoint、OAuth 参数或 Group ID。生产 `SelfServiceAccountCatalog` 为空；测试构造也只允许 OpenAI/Anthropic/Gemini 的 API Key 候选。创建在单一 `SERIALIZABLE` 事务内先检查 Account 容量，再按 Owner/平台锁定并复用默认组；缺失时检查 Group 容量并以与 2.3 相同的安全默认值创建。Account 固定 private、`schedulable=true`，与默认组优先级 50 的 `account_groups` 关系、Group/Account Scheduler Outbox 和 durable authorization event 原子提交。
- rename/delete 在同一事务内锁定 Actor 授权行与 Account，重新解析当前 JWT User、比较授权快照、重跑 Policy、校验 Owner 与 `access_version`；成功写业务状态、版本、Scheduler Outbox 和 durable event，冲突、越权或任一写失败整体回滚。删除同时清理既有关联并把受影响 group IDs 交给 Scheduler 事件；2.6 的 group `0`、平台默认组与 SIMPLE Mode 租户隔离仍未实现。
- 公共设置新增后端计算的有效 `self_service_hosting_enabled`，SIMPLE/Backend/原始 flag 组合继续 fail closed；前端 `/accounts` 路由和侧栏使用 opt-in 门禁。页面完成检索、排序、分页、详情、重命名、删除、两步创建向导、空产品目录和错误/重试状态，并补齐中英文文案、响应式布局、Dialog 可访问标题 ID 与空结果分页回归。
- 新增普通 JWT 用户 `GET/POST /api/v1/groups`、`GET /api/v1/groups/platforms`、`GET/PATCH/DELETE /api/v1/groups/:id`。list/get 继续复用同一 `AccessibleScope` 与 Group 窄投影；create 只接受服务端平台目录 ID、名称和描述，固定 private、active、exclusive、legacy authorization mode，并在同一 `SERIALIZABLE` 事务内执行 Group 容量检查、可信 Owner/creator 绑定、Scheduler Outbox 和 durable authorization event。
- Group update 只允许名称与描述，事务内重锁 Actor/Group、重解析 JWT Actor、重跑 Policy 并以 `access_version` 做 CAS；delete 在相同边界内拒绝仍被帐号、授权、订阅、路由、fallback、渠道、监控/定价数组、审计/默认订阅配置、未归档公告或待履约订阅订单引用的分组，历史用量、监控事实、审计日志和已完成订单不阻止软删除。
- migration 245 已把 active Group 名称唯一性改为平台组按 `lower(name)` 全局唯一、租户组按 `(owner_user_id, lower(name))` 唯一，并在线替换旧全局索引；`data-preflight.sql` 同步按相同 Owner 范围报告普通名称和 `%-default` 冲突，不再误报不同 Owner 的合法同名。
- 前端新增 `/groups` opt-in 路由、侧栏项、API/types、中英文文案和完整列表/检索/排序/分页/详情/创建/编辑/删除页面；生产 Group 平台目录保持空，Backend/SIMPLE/self-service 门禁继续关闭有效入口。
- 新增 server-owned `accountCreationAuthority`：管理员 JWT 创建的 Account 固定为平台 Owner 并记录 `created_by_user_id`，Admin API Key Service Principal 创建保持 creator 为空；所有不可信输入中的 Owner、creator 和 public level 都会被覆盖。普通自助创建继续只接受可信 JWT User，并把相同用户绑定为 Owner/creator。
- 管理员基础创建、OAuth 后建号、导入/批量、复制、shadow 与 CRS 新建帐号统一使用平台 authority；CRS 更新路径保留已有 Owner/creator。Claude、OpenAI、Grok、Gemini 与 Antigravity OAuth session 持久化发起 Actor、Owner kind 和 proxy，callback 不能替换服务端 state/redirect/type/tier/proxy；合法 callback 在首次上游调用前原子消费，无效 callback 不消费，上游失败或并发重放不能第二次到达上游。xAI Redis session 同步持久化 binding/proxy，并支持跨实例原子消费。

## 下一步

1. 实施任务 2.6：让 group `0`、平台默认组和 SIMPLE Mode 查询显式排除 `owner_user_id IS NOT NULL` 的租户帐号；当前空产品目录和有效 self-service 关闭边界保持不变。
2. 随后实施 2.7 的配额、SSRF、凭证、跨租户和存量兼容测试，并按当前无部署范围评审 Phase 2 退出；未经安全清单 `Release Accepted` 不增加生产候选。
3. 首次创建 production/staging、导入真实数据或升级现有数据库时，先执行 `data-preflight.sql` 与 `credential-key-preflight.sql`。provenance inventory 必须全为 `resolved`，terminal inventory 必须零行；任何 blocker 都按 `implementation-evidence.md` 路由处置后重跑。
4. 任一环境首次进入 `shadow` 前必须运行 role-mode readiness；切换后归档差异指标、日志量、sink `dropped_count`、观察窗口和回滚结果。任何自助、凭据导出、RBAC 或 ACL enforcement 启用前，分别完成两份安全清单的 `Release Accepted`。

## 阻塞与风险

- 0.4、0.5、0.8 和 1.12 已按 [`phase-0-exit-record.md`](phase-0-exit-record.md) 的单维护者、无部署、空 allowlist 范围勾选。该决定不是独立三方评审，也不是 production/staging、自助托管、凭据导出、shadow/RBAC 或 ACL enforcement 的发布批准。
- 本地 `sub2api` 只是空测试实例，仅有 1 个管理员和 1 个平台默认分组；当前不存在真实服务器。本地预检只证明脚本和空实例基线，首次导入或升级现有数据时仍必须运行真实只读报告。
- credentials/extra 与自助出站文档仅达到设计级 `Decision Accepted`；两份文档的 `Release Accepted` 仍是后续启用自助托管前的独立硬门禁，不能与设计批准合并。
- fresh setup 缺失兼容角色的问题已修复，本地管理员也已由 migration 232 补齐；真实服务器升级后仍必须通过专用 GET status/readiness 入口验证全量一致性。
- migration 245 已实现 Owner 范围的大小写不敏感 Group 名称唯一索引，预检也已镜像该范围；首次升级真实数据库前仍必须运行只读预检并先清理同一平台范围或同一 Owner 范围内的冲突。
- 1.6 scoped reader 与 ActorResolver 现已由 2.2/2.3 的普通用户 Account/Group Handler 和路由消费。当前有效 self-service 值关闭且生产 Account/Group 产品目录为空，不能把已接线的读取/写入入口解释为已开放自助托管。
- `role_authorization_mode` 当前保持 `legacy`；部署环境首次进入 shadow 前仍必须运行 readiness。shadow 下普通 JWT 用户的 Account/Group `CanCreate` 会使用 RBAC 结果，但总开关/self-service、hoster 资格和事务内配额仍是硬门禁。2.1 当时没有普通用户路由；2.2 现已注册 Account 路由，因此后端 Policy/Scope 有效值与空生产目录成为持续的运行时门禁。
- 1.7b 没有解除 RBAC 硬拒绝；1.8 ActorResolver 和管理员认证 shadow consumer 已完成，但其余授权 consumer 与外部门禁仍未完成，不能把 legacy↔shadow 管理入口或本地 shadow 记录能力解释为 RBAC 已交付。
- 通用管理员 settings PUT 仍跨多个服务，不是单一数据库事务；`role_authorization_mode` 已从该路径移除，其余新开关当前没有 consumer，因此不阻塞 dark launch，但启用任何授权 consumer 前仍需独立原子配置命令。
- 通用幂等升级 fence 允许新实例对新业务记录使用 Actor-qualified scope，并使仍写 raw scope 的旧实例 fail closed；auto-reset 的旧版本写的是 account-qualified scope，migration 243 另以数据库 trigger 拒绝迁移后新增/改写该 scope。当前没有旧 fleet；未来仅在升级运行过旧 Worker 的环境时，必须在维护窗先停掉并排空全部旧 Worker，不得宣称无感混合版本滚动，回滚旧 binary 时 auto-reset 必须保持关闭直至恢复兼容版本或完成记录协调。
- 1.8 新增并发用例已通过定向 race；扩大到相关包的 race 命令仍会命中 `grok_import_probe_test.go` 和 `channel_monitor_checker_body_test.go` 两处不在本次 diff 的既有测试辅助代码竞态，不能宣称全量 race 已通过。
- 1.10 只完成核心 Account/Group 管理写命令的事务内 Policy、版本、durable event 与适用 Outbox；读取、普通用户入口、ACL/RBAC 权威切换和旧分组资格 consumer 均未改变，不能把本切片解释为资源分享已开放。
- 2.1 的容量检查有意拒绝无调用方 `SERIALIZABLE` 事务或其他隔离级别的使用；2.2 Account 与 2.3 Group 创建都让容量锁持续到资源、Scheduler Outbox 和 durable event 一并提交。2.4 已统一可信 Owner/creator 来源，但没有提前开放普通用户 OAuth/import/copy/batch，也没有改变这些未来自助入口必须在同一事务执行容量检查与创建的契约。2.4 SHA `bf4903ab92095acc4f11cc477cc7777c14d53d8f` 的 push/PR 无过滤 Testcontainers、lint 与 Security Scan 已通过；这些工程证据不替代未来入口的事务容量契约或首次真实环境发布门禁。
- 2.5 在同一容量锁与 `SERIALIZABLE` 事务内创建/复用 Owner 默认组并绑定 Account；缺失默认组时 Group 也必须有剩余容量，已有默认组不会重复消耗 Group 配额。Account 只有随有效私有默认组关系同事务提交时才以 `schedulable=true` 落库，绑定、任一 Outbox、任一 durable event 或 commit 失败都会回滚全部新资源。2.6 完成前，有效 self-service 仍由 SIMPLE/Backend/Feature Flag 边界强制关闭，不能因帐号已可调度就提前启用。
- OAuth/privacy/probe 等外部网络动作无法与 PostgreSQL 形成分布式原子事务；2.4 以首次上游请求前的一次性 session claim 阻止 callback 重放，但不能宣称上游副作用可因后续本地提交失败而回滚。Privacy 的本地持久化已作为独立 ResourceMutation 重新授权并写版本/事件，仍受相同外部副作用边界约束。
- 1.11 的 5 秒是传播健康目标，30 秒是禁止扩大权限的安全线；它们不是对尚未迁移的数据面、WebSocket 或异步任务作出的端到端 SLA 承诺。权威 Policy/Scope 会按数据库时间同步拒绝到期来源；API Key 旧 allow snapshot 由 v22 拒绝 pre-v22 数据、首次写入 monotonic deadline、Redis 相对 TTL、rewrite 不续期和正向 L2 不提升 L1 共同约束在 30 秒内。
- 1.11 对 Account/Group Grant 到期只递增资源版本、写 durable event 并产生 Scheduler 事件；完整 `account_groups` 授权来源/验证版本扩展及撤权、到期、角色变化后的关系闭包重算仍属于任务 4.2/4.4，不能把 Scheduler 事件等同于关系已重算。
- `ResourceMutationCommand.ExpandsAccess` 已建立 fail-closed 契约，但当前没有 Grant 管理生产命令；后续新增或恢复授权路径必须显式标记，不能依赖调用方默认值绕过传播门。
- main 引入的 `OpenAIQuotaAutoResetService` 原始裸 `accountID`/`system` 审计缺口已在当前代码 SHA 以受限 Service Principal、专用 WorkerPolicy、逐操作重授权、Actor-qualified 幂等、原子 SP audit、账号锁和 reconciliation 收口，并通过远端 CI/Testcontainers/Security Scan。该结论只覆盖此 Worker，不属于 1.11 原完成范围，也不能外推为全部后台写 Actor 已覆盖；生产与目标环境发布门禁完成前不得启用 ACL/RBAC enforcement。
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
