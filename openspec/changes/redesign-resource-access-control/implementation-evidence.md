# Implementation Evidence

## 2026-08-20 - Kickoff

- 基线分支：`codex/resource-access-control-foundation`
- 基线提交：`58de21e70`
- 设计来源：`docs/resource-access-control-redesign.md`
- 本地 PostgreSQL：18.6，`127.0.0.1:5432`，数据库/角色 `sub2api`
- 本地 Redis：8.10.1，`127.0.0.1:6379`，DB 0
- 前端包管理基线：pnpm 9.15.9
- Go：本机 1.26.5，`GOTOOLCHAIN=auto` 将按 `go.mod` 获取 1.26.6
- Docker：未安装；repository Testcontainers 集成测试当前不可执行

### 计划验证命令

```bash
openspec validate redesign-resource-access-control --type change --strict --no-interactive
make -C backend generate
make -C backend test-unit
make test-frontend
make -C backend build
pnpm --dir frontend run build
```

数据库严格集成门禁（当前机器受 Docker 缺失阻塞）：

```bash
cd backend
CI=1 go test -tags=integration ./internal/repository
```

### 结果

- 第一切片结果见下节；命令未执行时仍不得标记为通过。

## 2026-08-20 - Dark Schema Foundation（1.1-1.4）

### 实现范围

- 新增 `229_resource_authorization_rbac.sql`：RBAC、Service Principal、用户授权版本、4 个系统角色、11 个权限和兼容角色回填。
- 新增 `230_resource_access_control_foundation.sql`：Account/Group Owner、creator、public/access version/mode、typed Grant 和 append-only `resource_authorization_events`。
- 新增 `231_resource_access_control_foundation_indexes_notx.sql`：对 5 个存量表索引使用 `CREATE INDEX CONCURRENTLY`；迁移 runner 在重试前删除同名 invalid index。
- 新增 9 个 Ent Schema 并重新生成代码；SQL migration 仍是生产 Schema 权威源。
- 新增 5 个默认关闭设置和 `role_authorization_mode=legacy`，有效值依赖由统一 runtime getter fail closed 计算。
- 管理员 settings API 契约、部分 payload 保留语义和审计差异测试已更新；未新增普通用户路由或 UI。

### 审查修正

- 将存量 accounts/groups 的 5 个索引从事务迁移拆到 `_notx.sql`，避免部署时长时间阻塞热表写入。
- `system_bootstrap` 回填在 `users.role` 变化后只收敛过时的 user/admin 兼容角色，保留人工、其他 Service Principal 和 bootstrap 的其他角色授权。
- 修复 integration-tag 测试中的短变量声明错误；独立复核后无剩余阻塞 finding。

### 自动化验证

| 命令/门禁 | 结果 |
| --- | --- |
| `git diff --check` | 通过 |
| `openspec validate redesign-resource-access-control --type change --strict --no-interactive` | 通过，change is valid |
| `make -C backend generate` | 通过 |
| `make -C backend test-unit` | 通过 |
| `cd backend && go test ./migrations ./internal/repository` | 通过 |
| `make -C backend build` | 通过 |
| `npx --yes pnpm@9.15.9 --dir frontend run lint` | 通过 |
| `npx --yes pnpm@9.15.9 --dir frontend run typecheck` | 通过 |
| `npx --yes pnpm@9.15.9 --dir frontend test -- --run` | 通过，236 files / 1654 tests |
| `npx --yes pnpm@9.15.9 --dir frontend run build` | 通过；仅有既有 chunk/dynamic import 警告 |
| `cd backend && go test -tags=integration ./internal/repository -run '^$' -count=1` | 编译通过；因无 Docker 未执行 Testcontainers 场景，不计为 integration 通过 |

### PostgreSQL 18.6 动态迁移验证

- 临时库 `sub2api_codex_migration_hardening_20260820`：连续两次调用 `ApplyMigrations`，验证 fresh/reapply、admin↔user 兼容角色收敛，以及 5 个索引均为 `indisvalid=true`；验证后数据库和临时入口已删除。
- 临时库 `sub2api_codex_upgrade_check_20260820`：先用迁移器应用 001-228，再插入存量 admin/user，最后运行当前 `ApplyMigrations` 两次；229/230/231 均登记成功，角色双向变化后收敛正确，5 个索引均有效。
- 第二个验证命令：`SUB2API_UPGRADE_CHECK_DSN='postgres://.../sub2api_codex_upgrade_check_20260820?...' go test ./internal/repository -run '^TestTemporaryResourceAccessUpgradeFrom228$' -count=1 -v`，结果 PASS（1.47s）。一次性测试文件和数据库均已删除，`pg_database` 复查数量为 0。
- 迁移专项还动态验证了 Grant XOR/访问级别/FK/部分唯一索引、版本/模式约束和 authz event 的 UPDATE/DELETE/TRUNCATE 拒绝。

### 1.6a 当时未完成的门禁（已由下一节 1.6b 收口）

- Docker/Testcontainers repository integration suite 未执行；必须在 CI 或有 Docker 的机器补跑。
- 本地测试库已完成只读预检，但不是生产数据；仍需对真实服务器只读数据运行同一脚本。
- credentials/extra 与自助 outbound allowlist 已达到 Review Ready，尚待负责人批准，不得据此开放 Phase 2。

### 提交与远程

- 提交：`215536582`（`feat(authz): add resource access control foundation`）。
- 远程：`origin/codex/resource-access-control-foundation`，已设置为当前分支 upstream。
- 推送前再次通过 `git diff --check`、OpenSpec strict validate、`make -C backend test-unit` 和迁移/Ent/Repository 专项测试。

## 2026-08-20 - Phase 0 本地数据预检

执行命令：

```bash
PGOPTIONS='-c default_transaction_read_only=on' \
  psql -X -v ON_ERROR_STOP=1 -P pager=off \
  -h 127.0.0.1 -U sub2api -d sub2api \
  -f openspec/changes/redesign-resource-access-control/data-preflight.sql
```

脚本以 `BEGIN TRANSACTION READ ONLY` 开始、`ROLLBACK` 结束，退出码 0。

- legacy role：`admin=1`；异常或 NULL role 为 0。
- 活动帐号/分组大小写重名、默认组重名、孤立 `user_allowed_groups` 和孤立 `account_groups` 均为 0。
- 规模：users 1、active accounts 0、active groups 1、legacy group grants 0、account-group links 0、active API keys 0。
- 229/230/231 已登记；4 roles、11 permissions、17 role_permissions、5 个并发索引和 append-only event schema 均存在。
- 唯一分组是 platform-owned legacy 默认分组；Account/Group Grant 和授权事件均为空。
- 6 个新 setting 没有物理行，runtime getter 仍按缺失值安全返回全 false 和 `legacy`。

本地实例只有 setup 管理员和默认分组，异常为 0 只能证明脚本可执行与空实例基线干净，不能替代真实服务器数据门禁。

预检发现 fresh setup 顺序缺口：迁移在管理员创建前完成，因此当前 admin 没有 `user_roles` 兼容行。legacy 模式不受影响；进入 shadow/rbac 前必须由 1.7 的 bootstrap/readiness 逻辑补齐并增加 fresh setup 测试。

## 2026-08-20 - Phase 0 安全清单 Review Ready

- `credential-inventory.md` 已覆盖 credentials/extra 键族、PostgreSQL/Redis/DTO/日志/导出/备份暴露面、JSONB/CAS/调度依赖和加密迁移批次。
- `outbound-security.md` 已冻结 Review Ready 候选：首批仅考虑固定官方端点的 OpenAI/Anthropic/Gemini API Key；其他平台、OAuth、代理、自定义 URL/Header 和云凭证暂缓或禁止。
- 两份文档均未标为 Accepted；生产键名统计、direct safe dialer、专用 DTO、Owner-bound OAuth、限频/配额、泄漏 canary 和负责人批准仍是硬门禁。

## 2026-08-20 - Authorization Domain Contract（1.5）

### 实现范围

- 新增 `backend/internal/authz`，定义 User、Service Principal 与进程内 System Actor；Actor 状态和构造器均为包内，HTTP/业务包不能注入管理员、能力或版本快照。
- 定义 11 个平台能力并用测试机械对照 migration 229 seed；未知能力 fail closed。
- 定义 Account/Group 13 个原子动作、ResourceRef 和 viewer/consumer/maintainer/manager 固定映射；manager 不含 delete/transfer，public 只允许 viewer/consumer。
- Decision 使用 typed provenance 保存 legacy admin、平台 capability、Owner、public、user Grant 或 role Grant 来源，以及 capability/grant/role ID。
- 稳定拒绝原因分为 not_found、forbidden、unauthenticated、unavailable 和 invalid；资源不可见与可见但动作不足可区分。
- 进程内 System Actor 没有 durable subject；任何写授权事件的 Worker 必须使用持久 Service Principal Actor。
- 本切片不接 DI、路由、Repository 或 Policy consumer，不改变 legacy 行为，也不打开任何开关。

### 自动化验证

| 命令/门禁 | 结果 |
| --- | --- |
| `cd backend && go test ./internal/authz` | 通过 |
| `cd backend && go test -race ./internal/authz` | 通过 |
| `cd backend && go vet ./internal/authz` | 通过 |
| `make -C backend test-unit` | 通过 |
| `make -C backend build` | 通过 |
| `git diff --check` | 通过 |

独立安全审查提出的 Actor 可伪造、legacy admin 来源缺失、拒绝传输类别混淆、System Actor durable audit 和 provenance 不足问题已在本切片内修正。复审确认原 6 项均关闭、无残余 blocker；Phase 1.6/1.10 仍须落实事务内版本重校验、`Unavailable -> 503` 和 System Actor durable mutation 拒绝。

## 2026-08-20 - Fresh Setup Compatibility Bootstrap（1.5a）

### 实现范围

- `createAdminUser` 使用数据库事务和 `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE` 串行化 Web、CLI、AutoSetup 及多进程初始化入口。
- 完整 `Install` 使用 PostgreSQL session advisory lock 覆盖数据库创建、迁移、管理员事务、配置和安装标记；拿锁后再次检查安装状态，竞争者不会继续写配置。
- Web、CLI 和 AutoSetup 共用 `Install`；AutoSetup 不再复制 JWT、连接测试、迁移、管理员和文件写入流程。
- 同一事务内完成 legacy 管理员写入、按 `users.role` 选择 `admin/user` 兼容角色、以 `system_bootstrap` 归因、校验无人工 grantor/无过期时间，并在任一步失败时整体回滚。
- 新增 migration 232 修复在 229 已执行后才由旧 setup 创建的用户；收敛 SQL 与 229 保持一致，只替换 `system_bootstrap` 拥有的错误 `admin/user` Grant。
- setup 与正常运行时 PostgreSQL DSN 统一为结构化 URL，修复空密码 keyword DSN 将后续 `dbname` 解析丢失的问题，同时正确编码密码特殊字符和时区。
- 不写 Feature Flag，不切换 `role_authorization_mode`，不改变 `users.role` 的 legacy 权威行为。

### 自动化与动态验证

| 命令/门禁 | 结果 |
| --- | --- |
| `cd backend && go test ./internal/setup ./migrations -count=1` | 通过 |
| `cd backend && go test -race ./internal/setup ./migrations -count=1` | 通过 |
| `cd backend && go vet ./internal/setup ./migrations` | 通过 |
| `make -C backend test-unit` | 通过 |
| `make -C backend build` | 通过 |
| `openspec validate redesign-resource-access-control --type change --strict --no-interactive` | 通过，change is valid |
| setup sqlmock：事务成功、兼容角色无法验证时 rollback、repeat skip | 通过 |
| migration 232/229 收敛 SQL 等价与不 seed settings contract | 通过 |
| PostgreSQL 18.6 临时 fresh 库：migrate → create → repeat | 通过；users=1、admin bootstrap Grant=1、authz_version=1、mode=legacy |
| PostgreSQL 18.6 临时 fresh 库：两个并发 admin bootstrap | 通过；恰好一个 created，最终 users/Grant 均为 1 |
| PostgreSQL 18.6 + Redis 完整双 `Install` 并发 | 通过；恰好一个成功，config JWT、admin email/password 与同一 winner 一致 |
| 本地开发库应用 migration 232 后核对 `dev-admin@sub2api.local` | 通过；legacy role=admin、compatibility role=admin、grantor=system_bootstrap、无过期 |
| 最新 backend 以统一 runtime DSN 重启 | 通过；DB pool、migration、Server started 正常，`/health=200`，无 ERROR/FATAL/PANIC/5xx |
| 本地管理员登录与现有页面 API | 通过；`auth/me`、dashboard stats、groups、accounts 均为 200，未登录 dashboard 正确跳转登录页 |
| 前端 auth guard、DashboardView 与 client 专项 | 通过，3 files / 55 tests |

临时验证库 `sub2api_codex_setup_bootstrap_20260820_a7f4` 与 `sub2api_codex_install_lock_20260820_b3e9` 均已在验证后删除。Docker/Testcontainers 门禁状态不变，仍需在 CI 补跑。

## 2026-08-20 - Policy 与 SQL Scope Foundation（1.6a）

### 实现范围

- 新增 `PolicyService` 的平台能力、资源创建、单资源授权和可信 `AccessibleScope`，覆盖 User、Service Principal、legacy/shadow/RBAC、Owner、public、直接用户 Grant 与角色 Grant。
- PolicyStore 以单个 PostgreSQL snapshot 查询读取当前主体、有效角色、能力、Feature Flag、资源和有效 Grant；数据库 JSON 文档缺字段、空对象或非法枚举均 fail closed。
- `group.use` 在资源为 legacy/shadow 时显式返回 legacy authority required，禁止将 ACL 与旧权威结果 OR 合并。
- Account/Group SQL scope predicate 绑定精确 `account.view`/`group.view`，在查询时重新校验主体版本、角色版本、能力集合、角色模式和 Feature Flag。
- public/Grant 到期采用数据库时间严格边界 `expires_at > CURRENT_TIMESTAMP`；相同最高等级 provenance 使用稳定排序。
- 本切片不新增普通用户路由、Handler、DTO 或 DI consumer；管理员和存量运行时行为不变。

### 自动化与动态验证

| 命令/门禁 | 结果 |
| --- | --- |
| `make -C backend test-unit` | 通过；包含 authz、repository 与 service 全套 unit-tag 测试 |
| `cd backend && go test -tags=unit -count=1 ./internal/authz ./internal/repository` | 通过 |
| `cd backend && go test ./internal/repository -count=1` | 通过 |
| 定向 repository race 测试 | 通过 |
| `git diff --check` | 通过 |
| `openspec validate redesign-resource-access-control --type change --strict --no-interactive` | 通过，change is valid |
| PostgreSQL 18.6 PolicyStore snapshot/expiry 动态测试 | 通过 |
| PostgreSQL 18.6 Account/Group Ent Scope IDs、Count、分页与 stale-version 动态测试 | 通过 |

动态测试首先捕获 Ent `ExprP` 会在 PostgreSQL 原样保留 `?` 的真实执行错误，随后改为 `entsql.P + Builder.Arg`，由外层 selector 正确生成 `$n` 参数。测试同时覆盖已有外层参数时的编号、参数数量不匹配 fail closed、Owner/public/direct/role Grant、严格过期边界和主体版本失效。临时数据库已删除，残留数量为 0。

### 未完成门禁

- 1.6b 尚未将 Scope 接入真实帐号/分组 reader；因此搜索、排序、分页、total、聚合和 hydration 的端到端同范围约束仍不得标为通过。
- 普通用户资源 DTO 尚未定义；管理员 Account DTO 包含 credentials/extra/proxy/调度字段，管理员 Group DTO 的 `account_count` 等聚合也可能跨可见帐号泄漏，均不得复用。
- Actor Resolver、Handler/DI 接线及 `Unavailable -> 503` 仍属于后续切片；当前没有新增授权放行路径。

## 2026-08-20 - Scoped Resource Reader（1.6b）

### 实现范围

- 新增独立 `ResourceReadService` 和 Account/Group scoped repository，不修改或复用现有管理员读取链。
- 帐号/分组列表均以同一可信 SQL Scope 为首个 predicate，再应用业务筛选、Count、白名单稳定排序和分页；详情查询在同一 SQL 中合并 Scope 与资源 ID。
- 不存在与不可见的详情统一返回既有 Account/Group not found，避免 ID 枚举；无效 Scope、查询、分页、排序和 ID 在访问数据库前 fail closed。
- 帐号 SELECT 仅包含 id/name/platform/type/status/owner/public level/时间；分组 SELECT 仅增加 description。帐号凭证、extra、代理、错误、额度、调度和关系字段，以及分组帐号拓扑、计数、价格、利润和路由字段均不会被读取。
- 普通 HTTP DTO 只序列化共享安全白名单；Owner ID 仅在进程内计算 `owned_by_me`，不会输出。Group 的 `account_count` 排序、聚合和 edge hydration 在本读取模型中显式拒绝，后续若开放必须使用独立 `account.view` Scope。
- 查询归一化拒绝未知排序、超长/非法 UTF-8/控制字符筛选和会导致 `int` offset 溢出的极端分页，避免客户端输入在 scoped Count 后退化为数据库 500 或错误页。
- 本切片不注册 Handler、路由或 DI，不开启 Feature Flag，不改变管理员或存量运行时行为。

### 自动化与动态验证

| 命令/门禁 | 结果 |
| --- | --- |
| `make -C backend test-unit` | 通过 |
| `cd backend && go test -race ./internal/service ./internal/handler/dto -run '^(TestResourceRead)' -count=1` | 通过 |
| scoped repository/authz scope 定向 race 测试 | 通过 |
| `cd backend && go vet ./internal/service ./internal/repository ./internal/handler/dto` | 通过 |
| `make -C backend build` | 通过 |
| `npx --yes pnpm@9.15.9 run build`（frontend） | 通过；仅有既有 chunk/dynamic import 警告 |
| `openspec validate redesign-resource-access-control --type change --strict --no-interactive` | 通过，change is valid |
| `git diff --check` | 通过 |
| PostgreSQL 18.6 `TestScopedResourceReaderPostgres` | 通过 |
| PostgreSQL 临时测试库残留检查 | 通过，残留数量为 0 |

PostgreSQL 动态测试覆盖 Owner、public、直接用户 Grant、角色 Grant、私有资源、严格过期 direct/role Grant、筛选后 total、稳定排序分页、不可见详情 not found、主体授权版本失效和窄字段 SELECT。Repository SQL 断言同时证明 Count 与页面查询均保留 Scope，且页面投影不含敏感列或未授权聚合列。

### 后续门禁

- Actor Resolver、Handler/DI 接线与 HTTP `Unavailable -> 503` 仍属于后续切片，当前没有新增普通用户授权放行路径。
- 真实 Actor → opaque `AccessibleScope` → 公开 reader 的跨包贯通测试等待 1.8 Actor Resolver 提供唯一可信 Actor 构造路径；本切片不为测试暴露 Actor/Scope 后门。当前分别覆盖 Policy 生成的 Scope 契约、公开 service 调用契约和 PostgreSQL reader SQL 行为。
- 自动完成、用量聚合和关系 hydration 尚未作为普通资源读取能力开放；实现时必须分别使用同一 Actor Scope，不能扩展当前 DTO 或复用管理员查询。

## 2026-08-20 - RoleService Core（1.7a）

### 实现范围

- 新增 RoleService/RoleRepository，作为 legacy/shadow 下 admin/user 角色变更的唯一 command path；管理员用户更新已接入生产 DI，通用 `UserRepository.Update` 的 Role 写能力已删除，不能绕过 RoleService。
- Role mutation 在同一 PostgreSQL 事务中串行锁定 actor/target，维护 `users.role`、`system_bootstrap` 拥有的兼容 `user_roles`、`users.authz_version` 和相邻管理员用户字段；任一步失败都整体回滚。
- migration 233 扩展既有用户缓存失效 trigger，使纯 `authz_version` 变化也为该用户每个有效 API Key 写入哈希后的 `auth_cache_invalidation_outbox`；重复应用安全，普通非授权字段更新不入队。
- 所有生产用户创建路径经 `userRepository.create` 事务内补齐 admin/user 兼容角色，避免 232 后新用户再次产生 shadow readiness 缺口。
- 角色命令要求 actor 是 active legacy admin，并拒绝陈旧 expected role、自我降级、最后一个 active admin 降级，以及 disabled 用户提升为 admin；普通 status writer 在用户行锁内按数据库当前角色复核，阻止自动封禁与并发提升形成 disabled admin。
- Role management 使用 PostgreSQL transaction advisory lock，actor/target 按 ID 排序后取行锁；普通用户更新预先取得 `ROW EXCLUSIVE` 表锁再取用户行锁，readiness 预先取得 `SHARE` 表锁，从而固定锁顺序并避免 transition/update 锁升级死锁。
- 内部 readiness 对 legacy→shadow 检查 migration 229/232/233、系统角色、active `system_bootstrap`、旧角色可映射性、兼容角色完整/无陈旧项及主体/角色版本；shadow→legacy 拒绝不能映射到 legacy admin 的 RBAC 权限与任何 active Service Principal role。
- 通用 settings PUT 不再写 `role_authorization_mode`，直接修改会返回 guarded-transition 错误；内部 `TransitionAuthorizationMode` 仅允许 legacy↔shadow，任何涉及 RBAC 的 transition 都硬拒绝。
- 1.7a 交付时尚无生产 mode transition Handler/CLI、step-up authentication 或 mode transition durable audit，`TransitionAuthorizationMode` 仅为内部 service/repository core；这些缺口现由下节 1.7b 代码收口，RBAC 仍未交付。
- 没有打开任何 Feature Flag、没有新增普通用户资源路由，当前授权权威和 `role_authorization_mode=legacy` 行为保持不变。

### 自动化与动态验证

| 命令/门禁 | 结果 |
| --- | --- |
| `make -C backend test-unit` | 通过 |
| RoleService、AdminService 与 ContentModeration 定向 race tests | 通过 |
| Role/用户更新/setting guard 相关包 `go vet` | 通过 |
| `make -C backend build` | 通过；生产 Wire 已包含 RoleRepository/RoleService |
| `CI=1 npx --yes pnpm@9.15.9 run build`（frontend） | 通过；仅有既有 chunk/dynamic import 警告 |
| `openspec validate redesign-resource-access-control --type change --strict --no-interactive` | 通过，change is valid |
| `git diff --check` | 通过 |
| migration 233 contract + 本机 PostgreSQL 18.6 isolated dynamic test | 通过；覆盖 reapply、纯版本更新入队、非授权字段不入队、role+version 每个 key 仅一条 |
| 本机 PostgreSQL 18.6 临时 harness：RoleRepository 7 个 top-level integration 场景 | 通过；覆盖 create compatibility、外层事务回滚、promotion/version/outbox、并发互降、混合更新回滚、readiness/mode 和 transition/update 无死锁 |
| 本机 PostgreSQL 18.6 自动封禁/管理员提升竞态场景 | 通过；最终保持 active admin |
| PostgreSQL 临时测试库与临时 harness 改动残留检查 | 通过，均已删除 |

### 1.7a 结束时的未完成门禁

- 1.7b 管理入口、step-up、CAS/readiness 和事务内 durable audit 的代码缺口已由下一节收口；当前验证状态以 1.7b 记录为准。
- RBAC transition 必须继续硬拒绝，直到 1.8 Actor Resolver 及全部授权 consumer 迁移完成；legacy↔shadow readiness 通过不等于 RBAC 可用。
- 本机 PostgreSQL 18.6 动态验证已完成，但 Docker/Testcontainers repository integration suite 仍未执行；必须在 CI 或有 Docker 的机器运行 `CI=1 go test -tags=integration ./internal/repository`，不得把本机临时 harness 记录为 CI 等价门禁。

## 2026-08-20 - Role Authorization Mode Management（1.7b）

### 实现范围

- 新增管理员认证后的 `GET /api/v1/admin/authorization/role-mode`，通过 read-only repeatable-read snapshot 返回 `current_mode`、唯一允许的 `target_mode`、稳定数组形式的 readiness blockers 和 `can_transition`；GET 不取得 command advisory/表锁、不执行 step-up，也不写 mode。
- 新增 `POST /api/v1/admin/authorization/role-mode/transitions`，请求体只允许非空 `expected_mode`/`target_mode`，拒绝未知字段、尾随 JSON 和请求内 actor；actor 只取认证上下文，RoleService 以 expected mode 做 CAS。
- POST 在解析和事务之前调用 session-bound TOTP step-up gate，不读取全局 step-up 开关；只接受具有非空 session ID 和近期 TOTP grant 的 JWT 管理员。Admin API Key 返回 `STEP_UP_ADMIN_API_KEY_FORBIDDEN`，未知认证方式与 sid-less legacy JWT 也在读取用户/grant 前 fail closed，均不进入 RoleService transaction。
- 成功 transition 在设置 mode 的同一 RoleRepository transaction 中直接写 `audit_logs`：固定 action `admin.authorization.role_mode.transition`、POST path、JWT auth method、actor user/email/role 快照、request ID/client IP/user agent，以及 previous/current mode。审计 insert 或 commit 失败时 mode 更新一起回滚。
- 成功变更后 handler 调用 `SkipAudit`，避免事务内成功记录与通用 AuditLog middleware 重复；CAS conflict、readiness blocker、请求错误和依赖失败不设置 skip，仍进入现有异步 middleware 的 best-effort 尝试审计。该失败路径可能在队列满、服务停止或批量写失败时丢失，不作为 durable 审计承诺。
- GET/POST 路由、AuthorizationHandler 和 RoleService/TOTP/UserService 依赖已接入生产 Wire；通用 settings PUT 仍不能写 `role_authorization_mode`。
- 入口只允许 legacy↔shadow。GET 遇到 RBAC mode、POST expected/target 涉及 RBAC 均返回 `RBAC_CONSUMERS_NOT_MIGRATED`；1.7b 没有交付 RBAC consumer 或启用 RBAC。
- 当前代码没有新增 mode 管理 UI，也没有切换本地/生产 mode；权威行为仍为 `legacy`。

### 当前已通过验证

| 命令/门禁 | 结果 |
| --- | --- |
| RoleService、AuthorizationHandler、authorization routes 与 audit action 聚焦 unit tests | 通过；覆盖 read-only GET、strict POST、session-bound JWT step-up、sid-less/未知 auth/Admin API Key 拒绝、CAS、trace、SkipAudit/失败 best-effort eligibility 和 audit failure rollback contract |
| 1.7b 相关 service/handler/routes/middleware/repository 包 `go vet` | 通过 |
| `make -C backend build` | 通过 |
| production Wire 生成/依赖图与编译 | 通过；AuthorizationHandler 已注入 AdminHandlers |
| `make -C backend test-unit` | 通过 |
| OpenSpec strict validate | 通过；`redesign-resource-access-control` valid |
| 本机 PostgreSQL 18.6 role mode 场景 | 通过；read-only GET 不阻塞持有中的 user write、成功 audit 恰好一条、audit trigger 失败时 mode 回滚 |
| 本地 HTTP 烟测 | 通过；未认证/无 TOTP/Admin API Key 拒绝、`blockers: []`、legacy↔shadow、409 CAS 与两条成功 durable audit；结束后恢复 legacy fallback 并清理测试状态 |
| `git diff --check` | 通过 |

### 剩余外部验证

- Docker/Testcontainers repository integration suite 仍待 CI 或有 Docker 的机器执行；本机没有 Docker。

## 2026-08-21 - Trusted Runtime Actor Integration（1.8）

### 实现范围

- PostgreSQL `ActorResolverStore` 以单条 SQL 按固定 code 解析 Service Principal 的状态、授权版本、有效角色、能力和当前配置；生产 Wire 为 JWT、Optional JWT、管理员 JWT/WebSocket 与 Admin API Key 注入同一个可信 Resolver。
- 普通/管理员 JWT 从数据库最新快照生成 User Actor；管理员校验不再信任先前加载的 legacy role。Admin API Key 只允许解析固定 `admin_api_key` code，并生成独立 Service Principal Actor；首管理员 `AuthSubject` 仅为尚未迁移的旧 Handler 提供兼容字段。
- `admin_api_key` 不拥有 RBAC 角色。migration 234 清理升级前同 code 碰撞留下的 `service_principal_roles`，但不修改已有主体状态；Resolver 遇到该主体带角色或能力时继续返回 authorization unavailable。RBAC transition 仍硬拒绝。
- `audit_logs` 新增 `actor_service_principal_id`、User/SP at-most-one 约束、Service Principal restrict FK 和两个并发部分索引。写入、COPY、列表、详情、搜索/筛选与前端展示均支持机器主体；Actor 存在时审计不再接受首管理员 shim 覆写机器身份。
- `audit_logs.actor_user_id` 保留既有无 FK 行为，以兼容历史记录和用户生命周期；用户 email/role 继续作为写入时快照保存。Service Principal 显示名/code 由受 restrict FK 保护的主体表读取。
- 新幂等业务记录使用 `<operation-scope>|user:<id>` 或 `<operation-scope>|service_principal:<id>`；raw scope 仅作为升级 fence 或升级前记录存在。兼容回放/回收必须匹配 method、route、payload 和当前/旧 actor scope 的完整 fingerprint，升级 fence 使旧、新实例不能同时取得副作用所有权。
- 兼容 fingerprint 明确区分 raw 历史记录与 Actor-qualified 历史记录：raw 只接受明确列出的 current/legacy actor-payload 组合；qualified 额外接受已发布版本写入的 canonical actor + legacy system payload，不能把该 qualified-only 组合放宽到 raw scope。migration 236 使过期清理在 DELETE 时重新校验 `expires_at`，避免并发续期后的升级 fence 被旧清理快照删除。
- 幂等成功/失败终态使用保留 request values、移除 request cancellation 的 5 秒有界 context 写回；system update/rollback 在客户端断线后完成时不会把记录遗留在 `processing`。restart 仅在外层终态落库并写出响应后、且不是 replay 时安排进程退出。
- 前端 system update/rollback/restart 使用 session-scoped 幂等 key。restart 成功或无响应/`status: 0` 的模糊结果会消费 pending key并进入重启等待；明确 HTTP 失败保留 key、停止倒计时并显示错误，避免把 409/503 误报为正在重启或在响应丢失后永久 replay 旧成功。
- Account/Group/Channel Monitor duplicate recovery 与 system operation ID 使用同一 durable Actor scope；不再生成 `admin:0`、`user:0`。User Actor 必须与 `AuthSubject` 同 ID，Admin API Key SP 必须携带有效首管理员兼容 shim，但 shim 不参与新作用域、审计或机器主体归因。
- 幂等 scope 在访问存储前按 PostgreSQL `VARCHAR(128)` 字符语义校验，并拒绝非法控制字符；缺 Actor、主体类型错误或 shim 不一致稳定返回 `503 AUTHORIZATION_UNAVAILABLE`。
- 新增真实 `ActorResolver -> PolicyService -> AccessibleScope -> ResourceReadService -> scoped reader` 跨包测试，证明 Actor 快照不能伪造、Policy 使用当前快照生成 opaque scope，主体版本陈旧时 reader 不会被调用。
- 本切片保持 dark launch：没有新增普通用户帐号/分组路由，没有开启任何 ACL/RBAC Feature Flag，没有改变现有管理员全局资源行为。

### 自动化与动态验证

| 命令/门禁 | 结果 |
| --- | --- |
| `make -C backend test-unit` | 通过 |
| `make -C backend build` | 通过 |
| 相关 authz/repository/middleware/handler/service 包 `go vet` | 通过 |
| `npx --yes pnpm@9.15.9 --dir frontend run lint:check` | 通过 |
| `npx --yes pnpm@9.15.9 --dir frontend run typecheck` | 通过 |
| `npx --yes pnpm@9.15.9 --dir frontend run build` | 通过；仅有既有 chunk/dynamic import 警告 |
| `npx --yes pnpm@9.15.9 --dir frontend run test:run` | 通过；237 个 test files、1661 个 tests |
| 新增幂等 finalization / fingerprint / restart 顺序相关 `go test -race` | 通过 |
| `openspec validate redesign-resource-access-control --type change --strict --no-interactive` | 通过，change is valid |
| `git diff --check` | 通过 |
| PostgreSQL 18.6 migration 234/235/236 隔离库动态测试 | 通过；覆盖 fresh/reapply、disabled 保留、历史 admin role 清理、User/SP XOR、restrict FK、并发部分索引，以及续期与旧 cleanup snapshot 的真实锁竞态；临时数据库残留为 0 |
| PostgreSQL 18.6 `AuthzPolicyStore` 隔离库动态测试 | 通过；覆盖单快照、严格到期边界和缺失 Service Principal fail closed |

### 发布边界

- raw-scope upgrade fence 会阻止旧实例与新实例对同一 key 双重执行，但混合版本期间旧实例可能收到 fingerprint conflict；生产发布应优先同版本切换或维护窗，并观察幂等 conflict/store-unavailable 指标。
- 扩大到相关 handler/service 包的 race 命令仍会命中两处既有测试辅助代码竞态：`grok_import_probe_test.go` 与后台 slog 共用 `bytes.Buffer`，以及 `channel_monitor_checker_body_test.go` 并发写共享 capture handler；两处首因均不在 1.8 diff。新增 1.8 并发用例已单独通过 race，但不能把全量 race 记为通过。
- Docker/Testcontainers repository integration suite 仍待 CI 或有 Docker 的机器执行；本机 PostgreSQL 隔离库验证不冒充 CI 等价门禁。

## 2026-08-21 - Admin Resource Actor Propagation（1.9）

### 实现范围

- 新增统一 `adminResourceActor` 入口：只从认证 middleware 写入的 request context 读取可信 Actor，并要求 JWT User 与兼容 `AuthSubject` 同 ID；Admin API Key 必须解析为独立 Service Principal Actor，首管理员 subject 只保留为旧 Handler shim。
- Account、Group、Claude/OpenAI/Grok/Gemini/Antigravity OAuth 与 CN Provider 的资源 Handler 在首个可执行逻辑 fail closed，先于 path/query/body 解析、依赖检查、仓储、网络调用和副作用；Actor 显式传入对应 Admin service facade 以及关联 usage、quota、scheduler、probe、import/export 和 OAuth 服务。
- 直接资源引用入口同步接线：User/API Key 的 Group 关系、Channel CRUD、Payment Plan、Redeem、Settings 默认订阅、Content Moderation 配置/API Key 测试、Channel Monitor CRUD/模板，以及 Ops alert rule/silence。模板 response 的关联 monitor count 也经过 Actor-aware facade，`Apply` 的 `monitor_ids` 不会绕过服务边界。
- `ValidateAdminResourceActor` 只接受 auth method 匹配的 JWT User 或 Admin API Key Service Principal；每个新 facade 首语句验证 Actor，再委托原业务方法。缺 Actor、subject 不一致或主体类型/认证方式错误返回 `503 AUTHORIZATION_UNAVAILABLE`，且输入解析、仓储和外部副作用为零。
- 注册路由 AST 门禁现在对整个既有 1.9 路径族要求入口 guard 和 Actor 向后传递；Account 与 Group/OAuth/CN 另有 malformed-input 运行时矩阵，覆盖 JWT User、Admin API Key Service Principal 和缺 Actor fail closed。静态 capabilities、runtime sanity、默认模型映射通过显式排除断言固定边界。
- 本切片保持 dark launch：service facade 尚未调用资源 Policy 或 SQL scope，没有启用 ACL/RBAC Feature Flag、没有新增普通用户路由，也没有改变可信旧管理员的成功响应和全局资源行为。

### 自动化验证

| 命令/门禁 | 结果 |
| --- | --- |
| `make -C backend test-unit` | 通过；包含完整 Handler/Service 回归 |
| `go test -race ./internal/service ./internal/handler/admin ./internal/server -run 'Actor\|Resource\|GroupOAuthAndCN' -count=1` | 通过 |
| `go vet ./internal/service ./internal/handler/admin ./internal/server` | 通过 |
| `go test ./... -run '^$' -count=1` | 通过；默认标签全仓编译 |
| `make -C backend build` | 通过 |
| `openspec validate redesign-resource-access-control --type change --strict --no-interactive` | 通过，change is valid |
| `git diff --check` | 通过 |

### 后续边界

- 1.10 仍需把安全关键写、durable audit、Auth Outbox 与 Scheduler Outbox 纳入同一事务，并在事务内重校验主体/角色/资源版本；1.9 的 Actor 参数不是授权决定，不能替代 Policy。
- Usage/Ops/Dashboard 等派生 scope、Channel Monitor Run/History/worker、Ops alert event/evaluator 和 Payment retry/refund 履约未在本切片宣称完成。
- 中央门禁会自动覆盖当前已分类路径族内的新路由；未来新增全新资源前缀时，必须同步扩展 `isAdminResourceRoute` 分类和显式边界断言。
