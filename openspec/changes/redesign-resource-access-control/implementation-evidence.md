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

- Docker/Testcontainers repository integration suite 当时未执行；该阶段要求后续在 CI 或有 Docker 的机器补跑，最终动态结果见 1.12 CI 记录。
- 本地测试库已完成只读预检，但不是生产数据；仍需对真实服务器只读数据运行同一脚本。
- credentials/extra 与自助 outbound allowlist 已达到 Phase 0 `Review Ready`，尚待负责人将设计决策批准为 `Decision Accepted`；Phase 2 `Release Accepted` 仍需生产数据、实现与目标环境证据，不得据此开放 Phase 2。

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
- 两份文档的 Phase 0 状态均未达到 `Decision Accepted`，Phase 2 状态也未达到 `Release Accepted`；生产键名统计、direct safe dialer、专用 DTO、Owner-bound OAuth、限频/配额和泄漏 canary 属于发布验收硬门禁，不能反向阻塞 Phase 0 设计批准，但必须有 owner、目标任务和验收方法。

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

临时验证库 `sub2api_codex_setup_bootstrap_20260820_a7f4` 与 `sub2api_codex_install_lock_20260820_b3e9` 均已在验证后删除。该阶段提交时未运行 Docker/Testcontainers 门禁；后续动态执行已由 1.12 CI 记录补齐。

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
- 本机 PostgreSQL 18.6 动态验证已完成，但该阶段 Docker/Testcontainers repository integration suite 尚未执行；要求后续在 CI 或有 Docker 的机器运行，不得把本机临时 harness 记录为 CI 等价门禁；最终动态结果见 1.12 CI 记录。

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

- 该阶段提交时本机没有 Docker，未执行 Docker/Testcontainers repository integration suite；后续动态执行已由 1.12 CI 记录补齐。

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
- 该阶段提交时本机没有 Docker，未执行 Docker/Testcontainers repository integration suite；本机 PostgreSQL 隔离库验证不冒充 CI 等价门禁，后续动态执行已由 1.12 CI 记录补齐。

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

- 1.9 结束时尚缺安全关键写、durable audit、Auth Outbox、Scheduler Outbox 与事务内版本重校验；该缺口已由下一节 1.10 的核心 Account/Group 写协调器收口。Actor 参数本身仍不是授权决定，必须由 Policy 判定。
- Usage/Ops/Dashboard 等派生 scope、Channel Monitor Run/History/worker、Ops alert event/evaluator 和 Payment retry/refund 履约未在本切片宣称完成。
- 中央门禁会自动覆盖当前已分类路径族内的新路由；未来新增全新资源前缀时，必须同步扩展 `isAdminResourceRoute` 分类和显式边界断言。

## 2026-08-21 - Transactional Resource Mutation Coordination（1.10）

### 实现范围

- 新增生产 `ResourceMutationCoordinator` 与 PostgreSQL repository。每个命令使用 `SERIALIZABLE` 事务，按固定顺序锁定 durable Actor、有效角色和排序后的 Account/Group；调用方已处于 Ent 事务但无法证明隔离级别时 fail closed。
- `NewAdminService` 在缺少 coordinator 时注入不可用哨兵，公开构造出的 Account/Group 写 facade 稳定返回 503，不能回退到 legacy 直写；API contract fixture 显式接入完整的 Resolver、Policy 和事务 repository。
- 协调器在事务内重新解析 JWT User 或固定 `admin_api_key` Service Principal，比较主体授权版本、角色版本、能力快照、legacy admin 状态和认证方式，再执行 `CanCreate`/`Authorize`。预检查后的 Actor、角色、Owner 或 `access_version` 变化不会沿用旧决定。
- Account 的 create/duplicate/update/extra/bulk/delete/batch delete、batch credentials、error/schedulable/proxy/quota、Shadow 和 Group 关系命令，以及 Group 的 create/duplicate/update/delete/sort、Composite Route、rate multiplier、RPM override、API Key Group 变更和用户 Group 替换，均复用协调器事务。关联引用先全部解析，任一不可见、无权或版本不符时不执行业务写。
- repository 的 Account、Group、Composite Route、API Key 与 user-group rate 写路径复用 `TxFromContext`；业务写、实际变更资源的 `access_version`、append-only `resource_authorization_events` 和适用 Scheduler Outbox 共用外层事务。Group/API Key/旧 Group 关系的 Auth Cache Outbox 由数据库 trigger 在同一事务产生，任一 audit/outbox 写失败会回滚业务状态。
- 新建资源从 `access_version=1` 开始；实际变更目标每个命令只递增一次，只用于锁定和授权的引用不递增。duplicate replay 与空命令统一 rollback 为 no-op，不写版本、event、durable marker 或提交后 callback。
- durable resource event 固定记录 Account/Group、Owner 快照、User 或 Service Principal Actor、auth method、event type、提交版本、request ID、规范化字段类别和 `result=success`；不写 credentials/extra 值、HTTP body 或其他 secret。通用 `audit_logs` 仍由 AuditLog middleware 异步记录，只有显式 `SkipAudit` 才跳过，不能用 resource event 取代控制台审计。
- 平台资源由 JWT User 创建时写实际 `created_by_user_id`；Admin API Key 创建时 creator 保持 `NULL`，以 Service Principal durable event 归因。普通 Account/Group Update 不再回写 Owner、creator、public level、authorization mode 或 access version。
- migration 237 扩展 Group 授权快照相关字段的 durable Auth Cache 失效，Outbox 只写 API Key SHA-256；静态 contract 已覆盖字段清单、hash 与 cosmetic 字段边界。本机 PostgreSQL 18.6 隔离库动态用例已覆盖所有监控字段、cosmetic silent 和事务 rollback，运行后临时数据库残留为 0。
- 本地缓存、Redis 和网络动作通过 after-commit callback 延迟到提交后；callback panic 逐个隔离。SQLSTATE `40001`/`40P01` 稳定映射 409，其他事务基础设施失败映射 503，Policy 403/404 与既有业务校验错误保持原语义。

### 自动化与动态验证

| 命令/门禁 | 结果 |
| --- | --- |
| `make -C backend test-unit` | 通过 |
| 聚焦 `internal/authz`、`internal/service`、`internal/repository`、`internal/handler/admin` resource mutation race | 通过 |
| 相关 authz/repository/service/middleware 包 `go vet` | 通过 |
| 默认标签全仓编译 | 通过 |
| integration 标签全仓编译 | 通过；只证明编译，不冒充动态执行 |
| `make -C backend build` | 通过 |
| migration 237 contract | 通过；覆盖授权快照字段清单、SHA-256 与 cosmetic silent 边界 |
| `SUB2API_AUTHZ_POLICY_POSTGRES_ADMIN_DSN=... go test -tags=integration ./migrations -run '^TestGroupAuthorizationCacheInvalidationPostgres$' -count=1 -v` | PostgreSQL 18.6 通过；27 个子场景全部通过，临时库残留为 0 |
| `openspec validate redesign-resource-access-control --type change --strict --no-interactive` | 通过，change is valid |
| 五个 1.10 OpenSpec 追踪文件 `git diff --check` | 通过 |
| `CI=1 go test -tags=integration ./internal/repository` | 当时未运行；本机无 Docker。新增 Auth/Scheduler Outbox 故障注入场景已通过 integration 标签编译；后续动态结果见 1.12 CI 记录 |

### 发布边界

- OAuth、Privacy、probe 等外部网络副作用不能与 PostgreSQL 构成分布式事务。Privacy 网络调用完成后的本地持久化已作为独立 ResourceMutation 重新授权并写版本/event/outbox，但上游副作用无法因后续数据库失败而回滚。
- 1.10 只完成适用 Auth/Scheduler Outbox 的原子 enqueue 与失败 rollback；Worker 幂等消费、lag 指标、多实例恢复、5 秒/30 秒传播 SLA、到期协调器和积压降级门仍属于 1.11 或后续切片。
- 没有新增普通用户帐号/分组路由，没有启用 ACL/RBAC Feature Flag，没有切换旧分组资格权威源，也没有完成数据面、WebSocket、异步任务或全部后台 System/Service Principal 写路径。
- 该阶段提交时本机没有 Docker，未执行 repository Testcontainers 动态套件；integration 标签编译不计为该门禁通过，后续动态执行已由 1.12 CI 记录补齐。

## 2026-08-24 - Bounded Authorization Propagation and Expiry Coordination（1.11）

### 实现范围

- migration 238 新增 `authorization_expiry_jobs`，由 `user_roles`、`service_principal_roles`、`account_access_grants` 和 `group_access_grants` 的 trigger 原子维护，并回填 future/already-due 来源。相同 generation 的 migration reapply 或无关字段更新不会复活已处理 job，`expires_at` 变化会清空旧 retry/lease 并重臂。
- 新增保留 Service Principal `authorization_expiry_coordinator` 作为 durable worker 审计身份；迁移清理其全部角色但保留既有 disabled 状态，运行时在每个到期事务内以 `FOR UPDATE` 验证 active 且零角色。缺失来源和提前 claim 也必须先通过该 readiness，禁止 coordinator 失效时静默消费队列。
- 到期 Worker 使用数据库时间、`SKIP LOCKED` claim、30 秒可恢复租约、有界重试和 detached 2 秒 retry/release。Repository 采用 `SERIALIZABLE` 及 `parent -> source -> job -> coordinator` 锁序；同一事务递增 User/Service Principal `authz_version` 或 Account/Group `access_version`，写 durable audit/resource event，并为 Account/Group Grant 到期 enqueue Scheduler 事件后才完成 job。request ID 绑定 job ID 与 `expires_at` 微秒 generation。
- PolicyStore 与 Account/Group SQL Scope 的所有 role/Grant 有效性判断统一使用严格 `expires_at > statement_timestamp()`；协调器负责跨实例收敛版本、缓存、审计与 Scheduler 状态，不作为同步拒绝的替代品。
- migration 239 为 Scheduler Outbox 增加 lease token、lease expiry、next attempt、attempt count 和 bounded last error，新增 CHECK 使用 `NOT VALID` 避免升级时全表验证阻塞写入；claim 索引拆到 migration 241，以 `_notx` `CREATE INDEX CONCURRENTLY` 在线创建并在重试前清理同名 invalid index。生产 Worker 改用 PostgreSQL claim/ack/retry 与 token fencing，不再以 Redis watermark 决定消费所有权；低 ID 晚提交和过期 lease recovery 都不会丢事件或允许旧 owner ack。durable bucket/lifecycle/full rebuild 使用 strict rebuild，锁忙或 fencing 返回错误并 fenced retry，恢复前不会 ACK。
- Auth Cache Invalidation Outbox 固定 `delivery_stage, available_at, id` 顺序，使 primary pass 始终先于延迟 safety pass；migration 240 以 `_notx` `CREATE INDEX CONCURRENTLY` 建索引，runner 会删除同名 invalid index 后重试。stage 0/1 分别计数、记录 lag/error，Worker 停机时使用 detached 2 秒 context 释放整批未结清 claim；service 仅提交相对 delay，Repository 以 `statement_timestamp() + interval` 设置 second pass/retry 的 `available_at`。
- API Key allow snapshot 版本提升到 v22，v21 及更早 snapshot 一律丢弃；首次正向 L1/L2 写入同时受 jitter 后 30 秒上限与不可序列化的进程内 monotonic deadline 约束。正向 L2 命中不提升到 L1，Redis 相对 TTL 是跨实例权威；缺失、过期或未来时间戳视为 miss 并回源，不删除或广播可能已被并发刷新的 L2 值。
- 新增 `AuthorizationPropagationGuard`：数据库单快照分别统计 Auth primary、Auth safety pass、Scheduler 与 Expiry 的 pending、ready、oldest lag，并检查三个必需 Worker 与 expiry coordinator readiness。5 秒为 primary 健康目标，30 秒为扩大权限安全线；统计失败、Worker 缺失/停止、coordinator disabled/缺失/带角色或 lag 达线时返回稳定 `AUTHORIZATION_PROPAGATION_DEGRADED`。
- Settings 只在有效开关状态从关闭变为开放时调用传播门；显式关闭和撤权始终可执行。ResourceMutation 提供 `ExpandsAccess` 契约供后续 Grant 命令显式声明，当前生产代码尚无 Grant 管理命令。Ops 新增 `GET /api/v1/admin/ops/authorization/propagation/health`，输出 target/safety、队列、Worker、`expiry_coordinator_ready` 和稳定降级原因；该安全入口不经过可选 Ops monitoring enabled 门禁，Ops disabled 时仍可读取 fail-closed 状态。

### 自动化与动态验证

| 命令/门禁 | 结果 |
| --- | --- |
| propagation service/repository/handler 聚焦 unit race 与 vet | 通过；覆盖 5 秒/30 秒边界、stage 分离、Worker/coordinator fail closed、Ops disabled 返回 200、OpsService 缺失返回 503 和 JSON 字段 |
| expiry repository/worker 聚焦 unit race 与 vet | 通过；覆盖 claim/retry/release、停机 detached cleanup、错误清除、锁序和 generation request ID |
| Auth Cache v22 TTL/monotonic/L2 clock-skew/interleaving/miniredis TTL 与 JSON 往返、Auth Outbox stage/claim/release/数据库相对时间与 migration 240 runner tests | 通过 |
| Scheduler Outbox repository/service 默认测试、strict lock-busy retry、migration 239/241 runner、聚焦 race 与 vet | 通过 |
| 默认标签全仓编译与 integration 标签全仓编译 | 通过；integration 仅证明编译，不代表 Testcontainers 动态执行 |
| 本机 PostgreSQL 18 expiry repository 动态套件 | 通过；覆盖四来源 exact-once、副作用、lease recovery、rearm、orphan、audit rollback、parent-first 无死锁、coordinator 锁与 Stats readiness |
| 本机 PostgreSQL 18 Scheduler commit-order/lease recovery 动态场景 | 通过 |
| `openspec validate redesign-resource-access-control --type change --strict --no-interactive` | 通过，change is valid |
| `git diff --check` | 通过 |
| `CI=1 go test -tags=integration ./internal/repository` | 当时未运行；本机无 Docker。integration 标签编译或本地 PostgreSQL harness 不计为 Testcontainers 门禁；后续动态结果见 1.12 CI 记录 |

### 发布边界

- 5 秒是 primary queue/Worker 健康目标，30 秒是禁止新增或恢复授权路径的安全线；当前没有为未迁移的数据面、WebSocket、异步任务或全链路发布窗口作出端到端 SLA 承诺。数据库 Policy/Scope 同步拒绝已到期来源；API Key 旧 allow snapshot 由 v22 拒绝 pre-v22 数据、首次写入 monotonic deadline、Redis 相对 TTL、rewrite 不续期和正向 L2 不提升 L1共同约束在 30 秒内。
- 本切片没有新增普通用户资源路由、没有打开 ACL/RBAC Feature Flag、没有切换旧分组资格权威源。`ExpandsAccess` 是后续生产 Grant 命令必须显式采用的契约，不能把当前无调用方解释为分享入口已受完整验证。
- Account/Group Grant 到期目前只递增版本、记录 durable event 并产生 Scheduler 事件。完整 `account_groups` 链接人/授权来源/Owner 批准/验证版本扩展属于任务 4.2，撤权、到期和角色变化后的关系闭包重算属于任务 4.4；1.11 不宣称这些关系已重算。
- 该阶段提交时本机没有 Docker，未执行 Docker/Testcontainers repository integration suite，不能在当时记录为通过；后续动态执行已由 1.12 CI 记录补齐。

## 2026-08-24 - Phase 1 Engineering Exit Verification（1.12）

### 实现范围

- 将 Account/Group 稀疏 SQL Scope 从逐行相关 OR 子查询改为同一语句内的候选资源 ID `LATERAL UNION ALL`，再由资源主键连接候选集；Owner、public、直接用户 Grant 和角色 Grant 各自保留可索引分支，过期判断继续使用数据库 `statement_timestamp()`。legacy admin 与 platform capability 全局旁路改为同一快照重校验内的布尔条件，不再生成第二次 Account/Group 候选扫描。
- migration 242 以 `_notx.sql` 和 `CREATE INDEX CONCURRENTLY` 为 Account/Group 增加 public access 部分索引；migration runner 对这两个索引提供与既有在线索引相同的 invalid-index 清理重试语义。
- SIMPLE Mode 在 SettingService、生产 PolicyStore 与生成 Scope 三层把 self-service、group sharing 和 account sharing 的有效值强制为 false。数据库 raw flags 保留配置意图，legacy 管理员治理保持兼容，但普通用户不能据此获得 Owner/ACL Scope。
- `role_authorization_mode` 只接受 canonical `legacy|shadow|rbac`；缺失/空值使用安全默认 `legacy`，带空白、大小写变体和其他 malformed 值按非法值回退 `legacy`。固定 `admin_api_key` Service Principal 在 legacy/shadow 的管理员兼容读取仍可用，但 SQL Scope 每次重新验证固定 code、active 状态和无角色约束。
- 新增真实 PostgreSQL 跨租户全链矩阵，覆盖 User/Service Principal、Account/Group、搜索、排序、分页、total、详情 IDOR 404、陈旧主体/角色/开关 Scope，以及 Admin API Key code/status 失效。
- 新增当前 Phase 1 管理员资源写面的双事务 TOCTOU 回归：两个不同管理员 Actor 携带同一 `access_version` 时只能有一个提交；SERIALIZABLE 重试使 mutation closure 可执行 1 或 2 次，竞争 loser 的事务尝试完整回滚，最终业务状态、版本、durable resource event 和 Scheduler Outbox 都恰好一次。真实 `AdminService.ClearAccountError` PostgreSQL 测试证明 production after-commit callback 只在提交后执行且恰好一次，通用 commit/rollback/panic 语义另由 coordinator 单测覆盖；普通 Owner/Grant 写入口尚未开放，其并发矩阵属于 Phase 2/3。
- 新增 20,000 行 Account/Group 与大规模无关 Grant fixture 的 PostgreSQL EXPLAIN 门禁，明确要求 Owner、public、direct-user Grant、role Grant 四条稀疏索引路径，并禁止稀疏查询的 Account/Group 主表 Seq Scan；另覆盖 Account/Group 的 legacy admin 与 platform capability 全局旁路，要求资源关系在计划中恰好出现一次。
- 新增从 migration 228 分段升级到当前版本、再重复 `ApplyMigrations` 的 Testcontainers 回归，覆盖存量数据、seed/backfill、trigger、在线索引和幂等；提交前本机没有 Docker，因此本地仅完成代码审查和 `integration` 标签编译，后续 GitHub Actions 动态执行结果见下方 CI 记录。
- production `PolicyService` 的 `CheckCapability`、`CanCreate`、`Authorize` 与 `AccessibleScope` 在 shadow mode 并行计算 legacy/RBAC，observer 只记录比较且响应始终采用 legacy。管理员 JWT 与固定 Admin API Key 生产认证入口已接入 Policy，固定 API Key 的 legacy allow/RBAC deny 可被观察。日志字段仅包含固定枚举，不含主体、角色、Grant 或资源 ID；行为差异记 WARN，等价比较记 INFO，observer panic 不影响授权响应。当前交付的是可由外部系统聚合的结构化日志，没有独立进程内指标计数器。

### 自动化与动态验证

| 命令/门禁 | 结果 |
| --- | --- |
| `make -C backend generate` | 通过；生成代码保持最新 |
| `make -C backend test-unit` | 通过 |
| authz、SettingService、PolicyStore、SQL Scope 与 migration runner 聚焦 unit/race | 通过 |
| 跨租户全链与 ResourceMutation TOCTOU PostgreSQL 聚焦 race | 通过 |
| 相关 authz/service/repository/migration 包 `go vet -tags=unit` | 通过 |
| 默认标签全仓编译与 `integration` 标签全仓编译 | 通过；仅证明编译，不代表 Testcontainers 动态执行 |
| `make -C backend build` | 通过；生产 Wire 生成与注入可编译 |
| frontend ESLint、完整 Vitest、TypeScript/production build | 通过；无 frontend 代码改动，build 仅有既有 chunk/dynamic-import 警告 |
| PostgreSQL 18.6 PolicyStore expiry、scoped reader、跨租户、Admin API Key、SIMPLE Mode 与 TOCTOU 动态套件 | 通过 |
| PostgreSQL 18.6 生产规模 SQL Scope EXPLAIN | 通过；四类稀疏索引路径均被采用且 Account/Group 主表无 Seq Scan；legacy admin/platform capability 的 Account/Group 全局计划均只访问资源关系一次 |
| migration 242 contract、runner invalid-index retry 与 PostgreSQL index validity | 通过 |
| production role shadow Policy/observer/管理员认证接线 unit 与聚焦 race | 通过；四个 Policy 入口保持 legacy 响应，结构化日志无 ID，observer panic 被隔离 |
| 228→current 持久升级/reapply Testcontainers | 通过；GitHub Actions 无 `-run` 过滤的完整 repository integration suite 包含 `TestResourceAccessControlUpgradeFrom228ThroughCurrent` |
| CI repository Testcontainers 动态套件 | 通过；CI 实际执行 `make test-integration` → `go test -tags=integration ./...`，repository 非缓存运行 41.430s |
| `openspec validate redesign-resource-access-control --type change --strict --no-interactive` | 通过，change is valid |
| `git diff --check` | 通过 |

### CI Dynamic Verification

| 字段 | 证据 |
| --- | --- |
| Workflow | GitHub Actions `CI` [Run 32711471080](https://github.com/savvym/sub2api/actions/runs/32711471080)，push event，attempt 1，结论 success |
| Test job | [job 97383587468](https://github.com/savvym/sub2api/actions/runs/32711471080/job/97383587468)，Ubuntu runner，结论 success |
| 被测提交 | `2d203b601c5d5b6578e91020bdbfbff4eb5bae6b` |
| 工具链 | Go 1.26.6；Testcontainers harness 使用 PostgreSQL `18.1-alpine3.23`、Redis `8.4-alpine` |
| 实际命令 | workflow 运行 `make test-integration`，Makefile 展开为 `go test -tags=integration ./...`；没有 `-run` 过滤 |
| Repository 结果 | `ok github.com/Wei-Shaw/sub2api/internal/repository 41.430s`，输出无 `(cached)`；因此 integration-tag repository 全套动态执行，包含 228→current 持久升级/reapply 回归 |

### PR 与 Security Review Entry

| 字段 | 证据 |
| --- | --- |
| Draft PR | [PR #1 - feat(authz): add default-off resource access control foundation](https://github.com/savvym/sub2api/pull/1)；base `main`，head `codex/resource-access-control-foundation`，保持 Draft |
| 合并预检 | 建立 PR 时分支相对 `origin/main` ahead 17/behind 0；`git merge-tree --write-tree origin/main HEAD` 成功，无内容冲突 |
| Backend security local equivalent | `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` 通过；可调用路径 0 个漏洞，依赖模块中不可达 finding 不计为代码受影响 |
| Frontend security local equivalent | `pnpm audit --prod --audit-level=high --json` 输出经 `tools/check_pnpm_audit_exceptions.py` 与 `.github/audit-exceptions.yml` 校验通过 |
| GitHub Security Scan | workflow 已从当前 fork 的 `disabled_fork` 状态启用为 `active`；SHA `1523aa1740140b6c7de9ae5553545856f141b889` 的 [push Run 32727879905](https://github.com/savvym/sub2api/actions/runs/32727879905) 与 [PR Run 32727886870](https://github.com/savvym/sub2api/actions/runs/32727886870) 均成功；两个 run 的 backend `govulncheck` 均报告可调用路径 0 个漏洞，frontend audit exception check 均通过 |
| Production credential-key preflight | 已新增只读 `credential-key-preflight.sql`，只输出文档名、软删除状态、帐号 status、平台/类型、键名、JSON shape 和计数；本地空实例以 `psql -v ON_ERROR_STOP=1` 执行通过，但本机无生产连接配置，未执行真实数据查询 |

### Phase 1 Exit Review

- 1.12 当前已实现的本地验证项、CI/Testcontainers 动态套件及 production role shadow 代码已通过，但不能宣称正式 Phase 1 退出；`tasks.md` 的 1.12 必须保持未勾选，任务进度保持 20/49。
- Phase 0 的 0.4 credentials/extra 清单、0.5 自助平台 allowlist/SSRF/限频策略与 0.8 退出评审尚无批准链接；真实服务器只读 `data-preflight.sql` 与 `credential-key-preflight.sql` 结果也未归档。
- CI/Docker 动态门禁已由 Run `32711471080` 满足；正式退出仍取决于外部安全评审、生产数据和目标环境证据，不因工程套件通过而自动批准。
- Draft PR #1 已建立但保持 Draft；GitHub Security Scan 已启用，并由 push/PR 两个 event 在同一 SHA 上通过。PR 仍须等待生产/目标环境证据与批准人，不因扫描通过而转 Ready。
- 目标环境仍为 `legacy`。正式退出前必须运行 role-mode readiness，按批准方案推进至 `shadow`，由日志系统聚合 production shadow 记录并归档具体差异指标、日志量与 sink `dropped_count`、观察窗口、回滚结果，以及平台/认证/安全批准人。
- SIMPLE Mode 限制是 Phase 1 临时发布护栏，不修改最终产品规格。只有完成 2.6 的 group `0`/平台默认组 `owner_user_id IS NULL` 隔离、生产规模验证和兼容矩阵复审后才可解除。

## 2026-08-25 - Latest Main Integration

### 合并范围

- merge commit `e63f0859c299e39ace1d78305064bf9b9b3bbeb3` 将 `origin/main@027d442f9bb705a1aa356c99ffbd0ae2ee40e646` 合入本分支；`origin/main` 已成为当前分支祖先。
- `grok_oauth_handler.go` 保留 trusted Actor 并调用 `AdminResetQuota`；`openai_oauth_handler.go` 采用窄 adapter 调用 upstream `RunOpenAIQuotaResetPostProcess`，同一 User 或 Admin API Key Service Principal Actor 继续贯穿 quota query/cache、account recovery 和 account reload。生成后的 Wire 同时包含 plugin、OpenAI auto-reset、ActorResolver、Policy observer 与 authorization workers。
- main 新增 `229_plugins.sql` 和 `230_plugin_artifacts.sql`，与本分支 authz migration 229/230 共用数字前缀。migration runner 以完整 filename/checksum 记账，重编号会使已部署 migration 被当作新文件重放，因此保留原名；词法顺序为 plugin 229、authz 229、plugin 230、authz 230。
- 新增 integration test `TestSharedMigrationNumberLineagesConverge`，分别模拟 main-first 与 authz-first 历史，在两次完整 `ApplyMigrations` 后验证四个文件各记录一次、plugin/authz schema 与 fixture 均保留、Atlas baseline 不漂移。

### Actor 覆盖结论

- 手动 OpenAI quota reset 与 Grok 管理入口：通过。冲突解法没有丢弃 HTTP trusted Actor，User 与固定 Admin API Key Service Principal 均继续走 Actor-aware facade。
- main 新增的 `OpenAIQuotaAutoResetService`：明确 deferred、不得计入后台 Actor 完成覆盖。该默认关闭的 scanner/worker 只接收裸 `accountID`，直接调用 repository/quota/recoverer，幂等 scope 不是 durable Actor，审计中的 `system` 字符串也没有 `ActorServicePrincipalID`。
- 当前 legacy/shadow dark foundation 未发现普通用户通过 HTTP 指定任意 account ID 的直接提权入口，因此 main integration 结论为 `PASS WITH EXPLICIT DEFERRED GAP`。在 ACL/RBAC enforcement 前，必须新增受限 Service Principal，并验证主体 missing/disabled/capability 不足时 query/reset/recover/cache/load/update 与上游请求均为零副作用；幂等 scope 和 durable audit 必须归因同一主体。完成前 enforcement gate 为 `BLOCKED`。

### 本地验证

| 命令/门禁 | 结果 |
| --- | --- |
| `make -C backend generate` | 通过；Ent 与 Wire 生成结果稳定 |
| `make -C backend test-unit` | 通过 |
| `go vet -tags=unit ./...` | 通过 |
| 默认标签与 integration 标签全仓编译 | 通过；integration 编译不代表动态 Testcontainers 执行 |
| `make -C backend build` | 通过 |
| OpenAI/Grok handler、authz、repository 与 migration 聚焦测试 | 通过 |
| frontend ESLint、完整 Vitest、TypeScript/production build | 通过；全局 pnpm 11 因 build-script approval policy 在 script 前失败，CI 固定 pnpm 9，直接本地工具验证通过 |
| `TestSharedMigrationNumberLineagesConverge` 动态 PostgreSQL/Testcontainers | 本机无 Docker；最终代码 SHA 的 push/PR CI 均以无 `-run` 过滤的 integration suite 非缓存执行 repository 包并通过，动态门禁已补齐 |

### Final Main-Integration CI / PR Evidence

| 字段 | 证据 |
| --- | --- |
| 被测提交 | `d47ca4bea567f778c6356a761344f376ca471ea4`；包含 main merge、Grok typed-nil 修复、Actor gap 记录与 build-aware AST gate |
| Push CI | GitHub Actions `CI` [Run 32812029206](https://github.com/savvym/sub2api/actions/runs/32812029206)，attempt 1，结论 success；[test job 97693053049](https://github.com/savvym/sub2api/actions/runs/32812029206/job/97693053049) 总时长 `10m02s`，integration step `3m33s` |
| PR CI | GitHub Actions `CI` [Run 32812033899](https://github.com/savvym/sub2api/actions/runs/32812033899)，attempt 1，结论 success；[test job 97693066526](https://github.com/savvym/sub2api/actions/runs/32812033899/job/97693066526) 总时长 `9m44s`，integration step `3m28s` |
| Integration 动态执行 | 两个 job 均在 Ubuntu、Go 1.27.0 上运行 `make test-integration` → `go test -tags=integration ./...`，没有 `-run` 过滤；`internal/repository` 分别非缓存运行 `55.422s` 与 `48.026s` 并成功 |
| 双线收敛测试归属 | `shared_migration_number_integration_test.go` 由 `integration` build tag 纳入 `internal/repository`，目标测试无 skip，并覆盖 `main_first`/`authz_first`；默认 `go test` 无过滤执行整个非缓存 package，因此两次均动态执行。workflow 未加 `-v`，日志不会逐个打印成功测试名 |
| Security Scan | [push Run 32812029208](https://github.com/savvym/sub2api/actions/runs/32812029208) 与 [PR Run 32812033933](https://github.com/savvym/sub2api/actions/runs/32812033933) 均在同一 SHA 成功；backend `govulncheck` 报告可调用路径 0 个漏洞，frontend production audit exception check 通过 |
| Draft PR | 采证快照（2026-08-25）：[PR #1](https://github.com/savvym/sub2api/pull/1) 为 Draft，base `main`，head `codex/resource-access-control-foundation`，head OID 为 `d47ca4bea567f778c6356a761344f376ca471ea4`；当时 GitHub 报告 `MERGEABLE/CLEAN` |

### 当前门禁

- 本次 main integration 不改变任务进度：仍为 20/49，1.12 保持未勾选，Phase 2 的 2.1 不得开始。
- 最终代码 SHA 的 push/PR CI、Testcontainers、Security Scan 与 Draft PR mergeability 已归档；旧 Run `32711471080` 不作为 main 新增 migration 和双线收敛测试的证据。
- Phase 0 批准、生产预检/凭据键名统计、目标环境 shadow 观察与平台/认证/安全批准仍未完成。

## 2026-09-02 - OpenAI Quota Auto-Reset Hardening and Latest Main Rebase

本节记录上一节 `d47ca4bea567f778c6356a761344f376ca471ea4` 采证之后的 migration 243/auto-reset hardening、2026-09-02 对最新主线的本地收口，以及随后完成的当前代码 SHA push/PR CI、Testcontainers、lint、Security Scan 与 PR 状态。上一节关于当时 CI 和 PR 状态的结论保留为历史事实。

### Latest Main Rebase 收口

- 已获取并将 24 个分支提交线性 rebase 到 `origin/main@efb46db0a`；rebase 后 `origin/main` 是当前分支祖先。随后加入 hardening 与 CI 收口两个提交，最终代码提交 `aeb967ebe0d9ed9aa5b43f0f9e60dc030f3839e6` 相对该主线为 ahead 26/behind 0。
- 生成文件冲突统一保留最新 main 版本，rebase 与 hardening 恢复完成后重新运行 Ent/Wire generation。源码冲突保留主线新增的 upstream model catalog、共享 OpenAI quota post-process 和 Grok unsupported 语义，同时新增/保留 Actor-aware facade，使同一管理员 User 或固定 Admin API Key Service Principal Actor 继续贯穿模型同步、quota query/cache、account recovery 和 account reload。
- Go 1.27 的 Wire generation 要求显式记录 `github.com/google/subcommands v1.2.0` 工具依赖；`go.mod`/`go.sum` 已补齐后生成成功。占位内容的临时 `frontend/pnpm-workspace.yaml` 未纳入提交。

### Worker Actor 与逐操作授权

- migration 243 新增固定、可停用且 roleless 的 `openai_quota_auto_reset_worker` Service Principal，只通过独立关联表直接拥有一个 `platform.account.openai_quota_auto_reset` Worker permission。它不获得普通 RBAC role；主体缺失、disabled、授权版本变化、出现任意角色、缺少或多出 direct permission 时，专用 WorkerPolicy 均 fail closed。
- scanner、帐号读取、quota 查询/缓存、幂等 claim/reclaim/lease 等 repository 操作、上游 reset、恢复、帐号重新加载和状态写入在各自新操作前重新解析或校验同一 Worker Actor。授权失败发生在对应 repository 或上游调用之前，不能沿用启动时快照继续执行；已经发生上游效果后的原子事实终结是下节明确记录的唯一例外。
- recovery candidate 先于普通 enabled-account 扫描，并使用独立 PostgreSQL keyset pager（`id > after_id ORDER BY id LIMIT`）。查询只保留运行时结构边界（未删除、OpenAI OAuth、非 shadow）和可能存在上游效果的 state，不依赖 auto-reset enabled、帐号 status 或 schedulable；`resetting` 无/坏 hash 与 `failed` partial hash 也会入选，交由 strict parser fail closed。scanner 使用可取消的阻塞式去重入队，队列满时等待 worker 容量，避免持续失败的低 ID 反复占据 offset 页并饿死后续 recovery；请求热路径 `Notify` 仍是可丢信号的非阻塞入口。
- managed-state key 存在且非 null 时以 `DisallowUnknownFields` 解码，拒绝错误字段类型和未知字段；status 只允许六个已知值，attempt credit/cycle hash 必须同时为空或同时为 24 位小写十六进制，`resetting` 必须携带 hash 对，`available_count` 必须是 `0..2147483647` 的整数。任何错误都在普通 eligibility/query/reset 前返回 reconciliation-required，不能把 malformed state 当作 nil 后开始新 attempt。
- 普通执行先在初次帐号加载、quota query 后重新加载、幂等 claim 前第三次加载执行 mutable eligibility reload；帐号必须非 shadow、auto-reset config enabled、active 且 schedulable。第四次检查位于每个 POST 前的 proxy/account 锁内，并同时绑定完整 upstream identity。测试分别在 query 期间、post-query reload 之后和锁升级边界修改 eligibility/identity，均在 POST 前停止且不写 reset audit。
- 新执行使用 `openai_auto_reset_credit|service_principal:<id>` Actor-qualified scope；稳定 key 同时绑定 account、credit hash 与 cycle hash，redeem request ID 由该 key 确定性生成。durable audit 只归因同一个 Service Principal，不再用 `system` 字符串或 account-qualified scope 代替主体身份。

### 跨实例账号租约与原子终结

- 每次帐号评估在首次帐号业务读取前取得 PostgreSQL transaction advisory lease；请求 context 取消不会让已开始的 lease 提前丢锁。claim、查询和发布 `resetting` 的前半程只持 advisory lock，避免 worker 被自己的独立帐号写事务阻塞。真正发出每次上游 POST 前，external-effect guard 才在同一 lease 内先对所选 proxy `FOR SHARE`、再对 account `FOR UPDATE`，并复核 proxy ID 未切换、完整 eligibility，以及 credential/auth fingerprint、ChatGPT account/FedRAMP、proxy ID/URL fingerprint 等完整 upstream identity。POST 返回后立即 release/rollback 该 lease，不把帐号或 proxy 行锁带入 recovery、audit/finalizer 与状态写；agent task recovery 后的第二次 POST 必须取得新 lease 并重新执行全部锁定与复核。不同实例不能同时跨过同一帐号的 POST 边界，不同帐号仍可并行；未取得 lease 时无副作用，接口错误返回 acquired 但 lease 为 nil 或 typed-nil 时显式失败。
- 专用 PostgreSQL finalizer 以 `FOR UPDATE` 锁定 Actor-qualified `processing` 记录，在同一事务内校验 scope、fingerprint、expiry、终态内容和 audit Service Principal 归属，写入唯一 audit，再完成 `processing -> succeeded`。audit 冲突、写入失败或状态更新失败会整体回滚，不能留下“成功幂等记录但无审计”或相反的半提交状态。
- finalizer 有意不重新解析或授权 Worker。reset 上游效果已返回后若 Worker 权限被撤销，post-process 的 recovery/query/cache/load/update 会停止，但 finalizer 仍可把 `recovery_deferred` audit 与 succeeded 幂等终态作为已经发生的同一事实原子提交，随后 service 返回授权错误；恢复授权后只做 recovery-only，不再调用 reset。该例外不允许 claim、reset、恢复或任何其他新副作用绕过逐操作授权。
- 真实 PostgreSQL 并发用例证明：相同 outcome 的两个 finalizer 都可幂等确认同一提交；不同 outcome 只允许一个提交，另一个得到 conflict，最终 response 与 audit 始终属于同一 winner。外部调用已经开始后的 timeout、终结失败和过期 `processing` 不会自动重发，保留给 reconciliation。

### Migration 243 升级与混合版本围栏

- migration 243 新增五列 `openai_quota_auto_reset_protected_attempts` marker：record 主键/RESTRICT FK、正 `account_id`、保护时间和成对的 reconciliation 时间/audit request ID。`account_id` 故意没有 accounts FK，使帐号物理删除后审计 provenance 仍保留。`openai_quota_auto_reset_protection_backfill` 只允许 `completed=TRUE`，作为精确一次 snapshot sentinel。
- snapshot 中 account scope 直接解析帐号；raw/任意旧 SP scope 必须以 OpenAI 帐号 `extra` 中 24 位 attempt credit/cycle hash 重建 `SHA256("oarc:<account_id>:<credit_hash>:<cycle_hash>")` 并与 idempotency key hash 唯一匹配。0 或多候选会抛 `check_violation` 回滚整个 migration，而不是猜测。被标记的 `failed_retryable` 冻结为 `processing`；全部 account 记录（含 succeeded replay）与 protected raw/旧 SP scope 归一到 reserved SP。raw fence、malformed raw 和 post-snapshot current retryable 不迁移、不标记，reapply sentinel 使当前行不被重新扫描。
- 两个协调函数都在 SQL 内强制 audit extra 的 `account_id`、`idempotency_record_id`、`request_fingerprint` 与调用/父记录精确一致，`evidence_ref` 必须为 trim 后 1..256 字符的 string，`decision_owner` 必须为 trim 后 1..128 字符的 string；任一缺失、错类型、越界或不匹配都以 `check_violation` 使 audit/状态整体回滚。evidence ref 只引用已归档证据，不得携带凭据或上游秘密。
- confirmed-success 使用 `reconcile_openai_quota_auto_reset_protected_attempt`：调用方 account ID 必须与 marker 精确一致，audit path/extra 由该 provenance 约束，audit request ID 必须为专用 deterministic decision ID `reconcile-success:<record_id>`，不得复用 upstream redeem request ID；函数同事务锁 attempt/marker、插入或核验 SP audit、写 reconciled tombstone，再执行唯一 `processing -> succeeded`。调用方不再传入终态过期时间；数据库固定设为 reconciliation statement time + 8 天，exact retry 核验该保留期。marker 不在终态前删除，通用 UPDATE 在 unresolved/reconciled 两态都拒绝旧 status-only succeeded/failed，等待行锁的旧请求在协调提交后仍失败。
- confirmed-success 当前唯一入口为八参数 `reconcile_openai_quota_auto_reset_protected_attempt(bigint,text,text,bigint,timestamptz,text,integer,jsonb)`；终态 body、result 与固定八天 retention 均由数据库从受约束 audit 构造，不再由调用方传入。raw migration reapply 显式 `DROP FUNCTION IF EXISTS` 两个废弃的 caller-controlled outcome/expiry overload；PostgreSQL 回归在 reapply 后按 `REGPROCEDURE` 校验旧签名不存在，避免 `CREATE OR REPLACE` 留下旁路入口。
- confirmed-no-effect 使用 `discard_openai_quota_auto_reset_protected_attempt_no_effect`，audit request ID 必须为 `reconcile-no-effect:<record_id>`，仅限操作者已停止并排空全部旧 Worker、且正面确认受保护上游调用无效果。`p_old_fleet_drained=TRUE` 只是调用方断言，函数无法探测 fleet；批准执行前必须归档 shutdown/drain 与 upstream no-effect 证据，并在 audit extra 记录不含凭据的可追溯 evidence ref 和 decision owner。函数校验 marker account provenance 与 audit 精确一致，在同一事务写 409 SP audit（`result_code=reconciled_no_effect`、`windows_reset=0`），仅当帐号当前 `resetting|failed` managed state 重算的 stable-key hash 精确匹配父 record 时清除该 state，不同、更新或已缺失 state 保留，再删除 unresolved marker 和父 record；exact retry 只接受同一 audit，等待行锁的迟到旧 UPDATE 在删除提交后影响 0 行。两个协调函数均为 `SECURITY INVOKER` 且 `REVOKE ALL ... FROM PUBLIC`；unknown 结果不得调用任一函数。
- 未协调 DELETE 返回 `NULL` 使 cleanup 跳过；confirmed-success 的 reconciled terminal 至少保留 8 天，过期后 trigger 仍会重算帐号当前 managed state：同一 stable-key 且状态为 `resetting|failed` 时返回 `NULL` 继续保留父行/tombstone，只在 recovery 已终结、不匹配或已缺失时才在父行前删除 tombstone。trigger 缺失/禁用时 RESTRICT FK fail closed。confirmed-no-effect 的 marker/父行只由上述 owner-only 函数原子删除。
- raw upgrade fence、malformed raw identity 和首次 snapshot 后由当前版本创建的 retryable attempt 不迁移、不标记。raw migration reapply 因 `completed=TRUE` sentinel 已存在而不会把 post-snapshot current retryable 冻结；最终 PostgreSQL 回归已证明该行仍可 reclaim、finalize 和 delete。
- 安装写围栏前以 `LOCK TABLE idempotency_records IN SHARE ROW EXCLUSIVE MODE` 关闭迁移扫描与 trigger 生效之间的并发写窗口。后续 trigger 拒绝 `openai_auto_reset_credit|account:<id>` 的 INSERT 和 UPDATE；raw upgrade fence、当前 Service Principal scope 和无关业务 scope 仍可正常写入。
- seed/table/index/trigger 都校验精确数据库形状；不安全的保留 code 碰撞或 look-alike marker/sentinel schema 会在授予 Worker 权限前失败。

### 双 Inventory 发布门禁与处理路由

`data-preflight.sql` 在同一个 `REPEATABLE READ READ ONLY` snapshot 中返回两份独立结果。第一份五列 provenance inventory 会同时输出 `resolved` 和 blocker，只有每行 `provenance_state='resolved'` 才能继续；第二份三列 terminal-recovery inventory 只输出 blocker，因此必须为零行。两份结果都不得包含 key hash、fingerprint、response body、attempt hash 或凭据，且每次数据修复后都必须在同一批准只读副本上重新执行并归档。

| Inventory / blocker | 含义 | 允许的处理路由 |
| --- | --- | --- |
| provenance / `malformed_identity` | scope、key hash、fingerprint 或 scope ID 不是 canonical identity | 从可信数据库备份、audit 或 upstream 证据重建精确 identity；不能靠字符串猜测补齐 |
| provenance / `unmatched` | raw/旧 SP attempt 无法映射到持久 pending state | 找回唯一 account/stable-key provenance，或依据外部效果证据先完成人工协调；不得强行指定帐号 |
| provenance / `ambiguous` | 同一 attempt 可映射多个帐号 | 消除重复/冲突数据并证明唯一 owner；在唯一性恢复前保持阻断 |
| provenance / `recovery_fingerprint_mismatch` | stable key 可映射但 Actor/payload fingerprint 不一致 | 核对原始 Actor 与 canonical payload，仅凭可信 provenance 修复；无法证明时升级人工事件 |
| provenance / `target_scope_collision` | 归一到 reserved Worker scope 会违反 `(scope,key_hash)` 唯一性 | 逐条确认重复记录的外部效果与 owner，消除 collision 后重跑；不能删除“看起来多余”的记录 |
| terminal / `malformed_pending_state` | managed state 违反 strict schema、状态、类型、hash pair 或 count 范围 | 从可信 state/audit 修复为 strict schema；若状态已无法恢复，须先取得上游效果证据并按批准 runbook 人工收口 |
| terminal / `unreachable_account` | pending attempt 位于 deleted、非 OpenAI OAuth 或 shadow 帐号，runtime pager/GetByID 不可达 | 若帐号仍应恢复则先恢复为 runtime 可达结构并运行 recovery；否则凭权威效果证据完成人工协调并清除 pending state，不能原样迁移 |
| terminal / `missing_attempt_record` | pending state 没有 exact stable-key durable record/in-flight parent | 定位并恢复权威 durable record，或用 upstream/audit 证据人工协调帐号状态；禁止合成猜测记录 |
| terminal / `unreachable_scope` | succeeded record 位于 raw、旧/其他 SP 等 runtime recovery 不读取的 scope | 先证明 account、stable key 与 fingerprint 的唯一绑定，再通过评审的数据修复归一到同帐号或 reserved Worker scope |
| terminal / `fingerprint_mismatch` | record scope/key 可达但请求 fingerprint 与 legacy/current Actor 都不一致 | 调查 conflicting Actor/payload，仅凭可信原始请求证据修复；无法证明时保持阻断 |
| terminal / `legacy_redacted_result` | 旧 generic redactor 已把 legacy `code` 破坏为 `***` | 从 upstream/audit 证明真实 outcome，再写入经验证的 canonical terminal result 并收敛帐号 state；绝不把 `***` 猜成 success/no-credit |
| terminal / `invalid_terminal_response` | response status/body/schema/count/flag 不能被 hardened replay decoder 消费 | 根据权威 outcome 证据修复 canonical response 与帐号 state，或走人工协调；不得只为清空 inventory 放宽 decoder |
| 任意未知分类 | 当前 runbook 未覆盖的数据形态 | 停止 migration，升级平台与安全评审并补充分类/测试；不得默认视为 resolved |

### 当前本地验证

| 命令/门禁 | 结果 |
| --- | --- |
| `make -C backend generate` | 通过；Ent 与 Wire 已在 `origin/main@efb46db0a` 基线上按 Worker permission、repository 和 service 注入重新生成 |
| `make -C backend test-unit` | 通过；Go 1.27.0，`internal/service` 非缓存执行 161.400s |
| `cd backend && go test -race ./internal/service ./internal/repository -run 'OpenAIQuotaAutoReset' -count=1` | 通过；service 5.046s、repository 3.696s，覆盖三次 mutable eligibility reload、POST 前第四次 locked eligibility/upstream identity 检查、逐操作授权、reset 后撤权仅原子终结、恢复授权后不二次 reset、nil/typed-nil lease 以及不同 attempt 并发 |
| `SUB2API_AUTHZ_POLICY_POSTGRES_ADMIN_DSN=... go test ./internal/repository -run 'Test(OpenAIQuotaAutoResetFinalizerPostgres\|AuthzWorkerPolicyStorePostgresMigration243AuthorizationMatrix)' -count=1 -v` | 通过，33.793s；本机 PostgreSQL 18.6 覆盖 WorkerPolicy 数据库矩阵和 5 个 finalizer PG tests，包括原子提交、失败回滚、精确重试和相同/不同 outcome 并发 |
| `cd backend && go test ./migrations -count=1` | 通过；当前 migration 243 static/default migration tests 全部执行 |
| `SUB2API_AUTHZ_POLICY_POSTGRES_ADMIN_DSN=... go test -tags=integration ./migrations -count=1` | 通过，22.454s；使用本机 PostgreSQL 18.6 管理员角色完整动态执行。首次用非管理员 `sub2api` 角色只因缺少 `CREATEROLE`/`pg_terminate_backend` 权限失败，改用本机管理员角色后全包通过 |
| `SUB2API_AUTHZ_POLICY_POSTGRES_ADMIN_DSN=... go test -tags=integration ./migrations -run 'TestOpenAIQuotaAutoResetActorMigrationPostgres\|TestOpenAIQuotaAutoResetDataPreflightPostgres\|TestOpenAIQuotaAutoResetTerminalRecoveryDataPreflightPostgres' -count=1 -v` | 通过；定向覆盖 actor migration/reapply、五列 provenance inventory 与三列 terminal-recovery inventory。terminal fixture 覆盖 strict managed state、不可达帐号、缺 record、scope/fingerprint、redacted/invalid response；不替代生产只读结果归档 |
| `cd backend && go test -tags=integration ./... -run '^$' -count=1` | 通过；全 integration 标签树编译成功，只证明 tagged tree 编译，不代表当前提交执行了 Testcontainers 动态套件 |
| `cd backend && go vet ./internal/service ./internal/repository ./internal/authz ./migrations ./cmd/server` | 通过；当前最终运行时代码重跑 |
| `make -C backend build` | 通过；当前最终运行时代码重跑 |
| `openspec validate redesign-resource-access-control --type change --strict --no-interactive` | 通过，change is valid |
| `git diff --check` | 通过 |

当前机器仍无 Docker，因此以上本机 PostgreSQL 18.6 动态测试本身不冒充 Testcontainers 门禁；最终代码提交的远端动态结果由下节单独归档。

### Current Code SHA CI / PR 收口

首次推送的代码提交 `7acf5a0ddadb1b915392594eedb97f2b58dfd39a` 暴露了两类 CI 问题，随后由 `aeb967ebe0d9ed9aa5b43f0f9e60dc030f3839e6` 修复并重新采证。

| 字段 | 证据 |
| --- | --- |
| 首次失败 | push [CI Run 33607286800](https://github.com/savvym/sub2api/actions/runs/33607286800) 与 PR [CI Run 33607293167](https://github.com/savvym/sub2api/actions/runs/33607293167) 均在 `7acf5a0ddadb1b915392594eedb97f2b58dfd39a` 上失败；两边 unit step 均成功，integration 在同一 repository 用例失败，lint 同时失败 |
| Integration 根因 | `TestListOpenAIAutoResetRecoveryCandidatePageBypassesMutableEligibilityWithoutSkippingAfterConvergence` 的 shadow fixture 写入了 `parent_account_id`，却沿用默认 `quota_dimension='global'`，违反最新 main 的 `chk_accounts_parent_dimension`；push 日志中的 repository package 在 `34.791s` 后报告该唯一失败 |
| Lint 根因 | golangci-lint v2.13.2 报告 6 项：Ed25519 公钥类型断言 1 项 errcheck、故意 nil context 测试 2 项 SA1012、布尔表达式 2 项 QF1001、嵌入字段 selector 1 项 QF1008 |
| 修复提交 | `aeb967ebe0d9ed9aa5b43f0f9e60dc030f3839e6`：父 shadow fixture 显式写 `spark`、根帐号写 `global`；Ed25519 公钥断言改为显式校验并传播错误；其余 lint 命中按意图收敛。提交已推送到 `origin/codex/resource-access-control-foundation` |
| Push CI | [Run 33608505225](https://github.com/savvym/sub2api/actions/runs/33608505225)，attempt 1，结论 success；[test job 100177927975](https://github.com/savvym/sub2api/actions/runs/33608505225/job/100177927975) 总时长 `10m07s`，unit `6m12s`，integration `3m38s` |
| PR CI | [Run 33608510880](https://github.com/savvym/sub2api/actions/runs/33608510880)，attempt 1，结论 success；[test job 100177945050](https://github.com/savvym/sub2api/actions/runs/33608510880/job/100177945050) 总时长 `10m08s`，unit `6m12s`，integration `3m38s` |
| Integration 动态执行 | 两个 Ubuntu job 均使用 Go 1.27.0，执行 `make test-integration` → `go test -tags=integration ./...`，没有 `-run` 过滤；Testcontainers harness 配置 PostgreSQL `18.1-alpine3.23` 与 Redis `8.4-alpine`；`internal/repository` 分别非缓存运行 `50.632s` 与 `49.677s` 并成功，因此包含 228→migration 243 持久升级/reapply 与完整 repository suite |
| Lint | push [job 100177928348](https://github.com/savvym/sub2api/actions/runs/33608505225/job/100177928348) 使用 golangci-lint v2.13.2，输出 `0 issues`，总时长 `4m06s`；PR [job 100177945213](https://github.com/savvym/sub2api/actions/runs/33608510880/job/100177945213) 同样成功，总时长 `3m58s` |
| Security Scan | push [Run 33608505242](https://github.com/savvym/sub2api/actions/runs/33608505242) 与 PR [Run 33608511032](https://github.com/savvym/sub2api/actions/runs/33608511032) 均成功；两个 backend job 的 `govulncheck ./...` 均报告代码受影响漏洞 0 个，两个 frontend production audit exception check 均通过 |
| Draft PR | 2026-09-02 采证时 [PR #1](https://github.com/savvym/sub2api/pull/1) 仍为 Draft，base OID `efb46db0a960fdad94502b1c3a982a0051cf5245`，head OID `aeb967ebe0d9ed9aa5b43f0f9e60dc030f3839e6`，GitHub 报告 `MERGEABLE/CLEAN`；退出门禁完成前不得转 Ready 或合并 |

上述结果补齐 migration 243、Worker Actor、帐号租约、原子 finalizer、recovery pager 与最新 main 约束的当前代码 SHA push/PR CI、完整 Testcontainers、lint 和 Security Scan 工程证据。它不替代生产只读 preflight、旧 Worker drain、目标环境 shadow 观察或批准人证据。

### 发布、回滚与剩余门禁

- migration 243 不能撤回旧 Worker 已经发出的上游调用。生产迁移前必须对批准的只读副本执行并归档完整 `data-preflight.sql`：五列 provenance inventory 的每行必须 `provenance_state='resolved'`，三列 terminal-recovery inventory 必须零行；任一非 `resolved` provenance 或任一 terminal row 都按上表阻断并处理，不能绕过或猜测。发布必须保持 auto-reset 关闭，在维护窗停止并排空全部旧 Worker，再应用 migration 243，并将所有实例切换为同一兼容版本。
- 迁移后每条 unresolved marker 只能依据外部证据选择 confirmed-success 或 confirmed-no-effect；unknown 必须保持保护。confirmed-no-effect 批准前必须归档旧 fleet shutdown/drain 与上游无效果证明，并将不含凭据的 evidence ref/decision owner 写入该条 409 SP audit；函数的 drained 布尔参数不是环境检测或证据本身。当前代码 SHA 工程门禁已经完成，但仍须 unresolved=0、Worker identity/readiness、回滚演练及生产批准后才能重新启用，不能采用新旧 Worker 混跑。
- 回滚到旧 binary 时 auto-reset 必须继续关闭；数据库围栏会有意拒绝旧版本使用的 account-qualified scope，不能在旧 binary 上重新启用该功能。回滚演练和证据需要纳入目标环境 readiness 记录。
- Phase 0 的 0.4/0.5 仍是 `Review Ready` 而非 `Decision Accepted`，0.8 尚无平台/认证/安全批准；生产 `data-preflight.sql` 与 `credential-key-preflight.sql` 也未执行归档。目标环境仍需完成 role-mode readiness、shadow 观察窗口、具体差异指标、日志量、sink `dropped_count` 和回滚证据。
- 当前代码 SHA 的 CI/Testcontainers/lint/Security Scan 已完成，但不改变任务进度：仍为 20/49，0.4、0.5、0.8 和 1.12 保持未勾选；生产/目标环境证据及平台/认证/安全最终批准完成前，不得宣称 Phase 1 正式退出或开始 Phase 2。

## 2026-09-02 - Single Maintainer No-Deployment Exit Decision

本节是同日较早“等待外部批准/生产证据”记录之后的新范围决定；在当前无部署、空 allowlist 范围内，以本节和 `phase-0-exit-record.md` 为最新状态。较早记录继续保留为决策历史，不再代表当前任务勾选状态。

### 事实与范围

- 维护者确认当前不存在 production 或 staging 环境，因此没有真实数据库、运行中的旧 auto-reset Worker、目标环境日志系统或可执行的生产 shadow 窗口。
- 项目当前只有唯一维护者 Savin Zhang（GitHub `savvym`）。本次记录透明合并产品、平台、认证、安全、迁移和风险 owner，不声称存在独立三方评审。
- 维护者决定当前自助 allowlist 为空：OpenAI、Anthropic、Gemini API Key 仅保留为未来候选并全部暂缓；OAuth、setup token、Cookie/密码、自定义 upstream、云凭证和复合平台继续禁止。
- 所有新增 Feature Flag 保持关闭，角色权威保持 `legacy`；当前范围只接受默认关闭的 dark foundation，不启用 Phase 2、自助托管、凭据导出、资源分享、shadow/RBAC 或 ACL enforcement。

### 版本化决定

- 新增 [`phase-0-exit-record.md`](phase-0-exit-record.md)，以基线 `674a5387e8e112553e8d5188441d3edaf427296c` 和 Draft PR #1 固定适用事实、单维护者风险接受、未决 release 项 owner/触发时点/验收方法和自动重开条件。
- `credential-inventory.md` 达到设计级 `Decision Accepted`：未知键和 `header_overrides` fail closed，管理员明文导出关闭，具体 KMS/provider 与加密实现保留为首次部署/启用前的 `Release Accepted` 门禁。
- `outbound-security.md` 达到空 allowlist 的设计级 `Decision Accepted`：未来候选仍受固定目标、direct transport、SSRF、禁重定向/代理、多维限频、Redis fail-closed、DTO 和 canary 要求约束。
- 0.4、0.5、0.8 和 1.12 勾选，任务进度从 20/49 更新为 24/49。1.12 只批准当前无部署 dark-foundation 的工程阶段退出，不批准发布或 Phase 2。

### 自动重开门禁

- 首次创建 production/staging、导入真实数据或升级现有数据库前，必须运行并归档 `data-preflight.sql` 与 `credential-key-preflight.sql`；provenance inventory 全部 `resolved`、terminal inventory 零行。
- 仅当未来环境运行过旧 auto-reset Worker 时，migration 243 才要求 maintenance window、auto-reset 关闭、旧 fleet 停止排空、同版本切换、unresolved marker 协调、Worker readiness 和回滚演练。当前 fresh/no-data 状态不伪造这些证据。
- 任一环境首次进入 `shadow` 前后必须归档 readiness、差异指标、日志量、sink `dropped_count`、观察窗口和回滚结果。
- 首次启用任一自助组合、凭据导出、RBAC 或 ACL enforcement 前，两份安全清单必须分别达到 `Release Accepted`；生产风险场景应优先引入独立安全 reviewer。

Draft PR #1 继续保持 Draft，不因本次阶段退出自动转 Ready 或合并。

## 2026-09-02 - Hosting Qualification and Quota Foundation（2.1）

### 实现范围

- 新增 migration 244 `user_hosting_entitlements`，以一用户一行保存 Account/Group 非负配额、正 CAS version、创建/更新管理员和时间戳。`user_id` 删除级联，两个管理员 provenance FK 使用 restrict；hoster 资格仍只由系统 `user_roles` 中的 `hoster` 角色决定，migration 不写角色、setting 或 Feature Flag。
- 新增对应 Ent Schema、生成代码和生产 Wire。Schema 契约固定唯一列、三个 FK symbol 与删除策略；生成过程中发现一对一 edge 已隐式提供 `user_id` 唯一性，删除重复显式索引后 SQLite `enttest` 建库恢复正常。
- 新增管理员 `GET/PUT /api/v1/admin/authorization/hosting-entitlements/:user_id`。GET 返回 hoster 有效资格、配额、当前未删除 Owner 资源用量、剩余量、CAS version 和授权版本；PUT 要求完整严格 JSON、非负 expected/version quotas，以及带 session ID 的近期 JWT TOTP step-up，Admin API Key 不允许执行写入。
- PUT 在 `SERIALIZABLE` 事务中按稳定顺序锁 actor/target user、actor roles、系统 hoster role、目标 hoster assignment 和 entitlement；随后重新解析 active legacy admin 并比较 Actor 授权快照，以 expected version 执行 CAS。成功 hoster/配额写和固定 action/method/path 的 durable audit 同事务提交，audit 失败整体回滚；no-op 保留通用 attempt audit，不伪造事务内成功变更记录。
- 新增或撤销 hoster、以及把临时 hoster assignment 转为永久时递增目标 `users.authz_version`，由现有 trigger 为有效 API Key 写 cache invalidation Outbox；纯配额变化只递增 entitlement version。首次配置从 version 0 物化为 1；管理员可把配额降到当前 usage 以下，存量资源不删除，后续创建拒绝。
- 新增事务绑定 `HostingCapacityGuard`。Account/Group 创建调用方必须持有 `SERIALIZABLE` 数据库事务，guard 在锁内重解析 JWT User、重校验 Actor、active hoster、Policy `CanCreate`、实时 usage 与 quota，并要求锁持续到资源 insert 提交或回滚；其他隔离级别 fail closed，避免 Repeatable Read 在等待行锁后继续读取旧快照。并发创建共享同一 entitlement/user/role 锁，不能共同越过最后一个容量。
- role shadow 增加窄例外：普通 JWT User 的 Account/Group `CanCreate` 使用 RBAC 结果；管理员、Service Principal、`CheckCapability`、`Authorize`、`AccessibleScope` 和其他资源类型仍返回 legacy。例外继续受总开关、self-service、主体状态、对应创建能力与事务内配额约束。
- 本切片没有注册普通用户 Account/Group 路由或 UI，没有启用平台/OAuth/凭据导出，也没有修改数据库 setting。当前 Feature Flag 全部关闭、`role_authorization_mode=legacy`，因此新增 shadow 例外在当前运行范围不可触发自助创建。

### 自动化验证

| 命令/门禁 | 结果 |
| --- | --- |
| hosting entitlement migration、service、authz、admin handler 与 route 聚焦 unit | 通过；`internal/service` 非缓存运行 176.277s，其他聚焦包均成功 |
| Ent schema/migrate 与 generated-schema contract | 通过；覆盖唯一列、FK symbol、CASCADE/RESTRICT 和 SQLite 建库 |
| `make -C backend generate` | 通过；Ent 与 Wire 已按最终 Schema/DI 重新生成 |
| `make -C backend test-unit` | 通过；完整 unit-tag backend tree 成功 |
| shadow `CanCreate` 窄例外与 self-service 关闭回归 | 通过；普通 User Account/Group 创建能力可用，开关关闭仍拒绝，管理员/Service Principal/其他 Policy API 保持 legacy |
| `cd backend && go vet ./internal/authz ./internal/service ./internal/repository ./internal/handler/admin ./internal/server/routes ./migrations` | 通过 |
| `make -C backend build` | 通过；生产 Wire 包含 repository/service/handler |
| `cd backend && go test -tags=integration ./... -run '^$' -count=1` | 通过；全 integration 标签树编译成功，不代表 Testcontainers 动态执行 |
| `cd backend && go test -tags=integration ./internal/repository -count=1` | 通过并明确跳过；本机没有 Docker，repository 报告 `docker is not available; skipping integration tests` |
| `cd backend && CI=1 go test -tags=integration ./... -count=1` | 环境门禁失败；唯一失败原因为 `docker is not available (CI=true)`，其余 tagged packages 成功，不能记录为动态通过 |
| `openspec validate redesign-resource-access-control --type change --strict --no-interactive` | 通过，change is valid |
| `git diff --check` | 通过 |

### PostgreSQL/Testcontainers 待补证据

新增 integration 场景已经覆盖 migration/schema、hoster grant/quota/revoke、授权版本与 cache Outbox、audit failure 全事务回滚、首次及后续版本并发 CAS、非 `SERIALIZABLE` 容量事务拒绝、降额低于 usage 后拒绝，以及 Account/Group 两类并发创建至多一个成功。当前机器没有 Docker，这些场景只完成标签编译和显式本地跳过；本次提交推送后必须由 Draft PR #1 的 GitHub Actions 无过滤 integration suite 动态执行并补录 run/job/SHA。该外部结果不影响代码提交，但在记录成功前不得把 Testcontainers 门禁标为通过或启用 self-service。

## 2026-09-02 - Private Self-Service Account CRUD（2.2）

### 实现范围

- 新增普通 JWT 用户 `GET/POST /api/v1/accounts`、`GET /api/v1/accounts/products`、`GET/PATCH/DELETE /api/v1/accounts/:id`。入口在解析查询、path 或 body 后进入业务前要求可信 JWT User Actor，并校验其 user ID 与认证中间件 `AuthSubject` 一致；Admin API Key、缺失 Actor、主体不一致、未知查询键、重复查询值、未知 JSON 字段和尾随 JSON 均 fail closed。
- list/get 复用既有 `ResourceReadService`、Policy `AccessibleScope` 与 PostgreSQL scoped reader。Scope 先于筛选、Count、排序和分页应用，并重校验主体/角色/能力/开关快照；详情不可见与不存在统一返回 not found。Account SELECT/HTTP DTO 新增 `credential_configured` 布尔值，但继续不查询或序列化 credentials、extra、Owner ID、proxy、运行时状态、错误/额度、父帐号或帐号-分组关系。
- 简化创建只接受 `name`、服务端 `product_id` 和 `api_key`。平台与认证类型只能来自不可变服务端目录，客户端不能提交 endpoint、OAuth、proxy 或其他平台配置；生产 `SelfServiceAccountCatalog` 为空，测试目录也只允许 OpenAI/Anthropic/Gemini 的 API Key 类型候选，所有 OAuth 与其他候选继续禁止。
- Account 创建在单一 `SERIALIZABLE` 事务内调用 `HostingCapacityGuard`，从 JWT User 绑定相同的 `owner_user_id` 与 `created_by_user_id`，写入 private、ungrouped、`schedulable=false`、access version 1 的帐号。Account insert、Scheduler Outbox 和 `account.created` durable authorization event 原子提交；配额、资格、Policy、目录或任一写失败整体回滚。
- rename/delete 在同一事务内按 Actor 授权行与 Account 的顺序加锁，重新解析当前 JWT User、比较授权快照、校验 Owner、重跑 edit/delete Policy，并以锁内 `access_version` 执行更新。非 Owner conceal 为 not found；成功写业务状态、版本、Scheduler Outbox 和 durable event。删除会清理现有关联并携带受影响 group IDs 触发 Scheduler 重建，但 2.5 的默认私有组绑定和 2.6 的 group `0` 隔离仍未提前实现。
- 公共设置响应与注入 payload 新增后端计算的有效 `self_service_hosting_enabled`，Backend/SIMPLE/原始 flag 组合继续 fail closed。前端新增 `/accounts` opt-in 路由与侧栏项、API/types、中英文文案和完整 CRUD 页面，覆盖检索、排序、分页、详情、重命名、删除、两步创建、空产品目录、错误重试和响应式布局；同时修复 `BaseDialog` 标题 ID 冲突与空结果分页显示。

### 本地自动化验证

| 命令/门禁 | 结果 |
| --- | --- |
| `go test ./internal/service ./internal/repository ./internal/handler ./internal/server/routes -run 'SelfServiceAccount\|ScopedResourceReader\|GetPublicSettings_ExposesEffectiveSelfServiceHosting\|GetPublicSettings_ExposesOnlyEffectiveSelfServiceHosting\|AuthzCurrentCapabilities' -count=1` | 通过；覆盖 service/repository/handler/routes、公共有效开关、Owner conceal、strict DTO、窄 SELECT、事务 rollback 和路由接线 |
| 7 个 2.2 前端聚焦 Vitest 文件 | 通过；35 tests，覆盖 Account API/view、feature flag、router/sidebar、Dialog 与 Pagination |
| `cd backend && go test ./...` | 通过；默认标签全仓测试成功 |
| `cd backend && go vet ./...` | 通过；全仓静态检查成功 |
| `make -C backend build` | 通过；版本 `0.1.183` 的生产 server 构建成功 |
| `cd backend && go test -tags=integration ./... -run '^$' -count=1` | 通过；全 integration 标签树编译成功，不代表 Testcontainers 动态执行 |
| `cd frontend && npm run test:run` | 通过；251 files、1797 tests，耗时 50.09s；仅有仓库既有 jsdom/Vue/i18n 警告 |
| `cd frontend && npm run typecheck` | 通过 |
| `cd frontend && npm run lint:check` | 通过，0 errors |
| `cd frontend && npm run build` | 通过；Vite production build 完成，仅有既有动态/静态 import 与 chunk size 警告 |
| `openspec status --change redesign-resource-access-control` | 通过；4/4 planning artifacts complete |
| `openspec validate redesign-resource-access-control --type change --strict --no-interactive` | 通过，change is valid |
| `git diff --check` | 通过 |

### 剩余发布边界

- 生产 self-service 产品目录保持空数组，OAuth 与全部候选产品保持禁止；Feature Flag 仍默认关闭，`role_authorization_mode` 仍为 `legacy`。公共设置只暴露有效值，因此前端路由/侧栏与后端 Policy/Scope 在当前配置下都不会开放该能力。
- 新建租户 Account 固定 private、ungrouped、`schedulable=false`。在 2.5/2.6 完成 Owner 私有默认组绑定和 group `0`/平台默认组隔离前，它不会进入调度；这些规则不属于 2.2，不能通过临时前端或配置绕过。
- 当前没有 production/staging、真实数据或旧 Worker。本次提交的远端无过滤 Testcontainers、lint 与 Security Scan 结果需在推送后补录；在此之前不得把动态 CI 门禁标记为通过，也不得把 2.2 完成解释为 self-service `Release Accepted`。

## 2026-09-02 - Private Self-Service Group CRUD（2.3）

### 实现范围

- 新增普通 JWT 用户 `GET/POST /api/v1/groups`、`GET /api/v1/groups/platforms`、`GET/PATCH/DELETE /api/v1/groups/:id`。Handler 要求可信 JWT User Actor 与认证中间件 `AuthSubject` 一致；查询、path 和 body 使用白名单、单值校验、`DisallowUnknownFields` 与尾随 JSON 拒绝，Admin API Key、主体不一致和未知字段均 fail closed。
- list/get 复用既有 `ResourceReadService`、Policy `AccessibleScope` 与 PostgreSQL scoped Group reader。Scope 先于筛选、Count、排序和分页执行；详情不可见与不存在统一返回 not found。普通用户投影只包含 ID、名称、描述、平台、状态、`owned_by_me`、公开级别和时间戳，不查询或序列化 Owner ID、帐号拓扑/计数、价格、订阅、路由、利润或其他平台治理字段。
- 创建只接受 `name`、`description` 和服务端 `platform_id`。平台只能来自不可变 `SelfServiceGroupCatalog`，客户端不能直接提交 platform、定价、订阅、路由、fallback 或治理字段；生产目录为空。测试候选也只允许已知平台。新 Group 强制相同 Owner/creator、private、active、exclusive、access version 1 和 legacy authorization mode，其余管理员策略保持数据库安全默认值。
- Group 创建在单一 `SERIALIZABLE` 事务内调用 `HostingCapacityGuard`，锁持续到 Group insert、Scheduler Outbox 和 `group.created` durable authorization event 一并提交。update/delete 在事务内锁定 Actor 授权行与 Group，重新解析当前 JWT User、比较授权快照、校验 Owner、重跑 edit/delete Policy，并以锁内 `access_version` 执行 CAS；非 Owner conceal 为 not found，冲突或 audit/outbox 故障整体回滚。
- update 只允许名称和描述。delete 软删除前检查仍在运行或可重新启用的引用：帐号绑定、API Key、订阅、用户资格/倍率、渠道、组合路由、Grant、fallback、兑换码/套餐、Channel Monitor、帐号统计定价、Content Moderation/Prompt Audit、默认订阅、未归档公告和待履约订阅订单。历史用量、监控聚合、审计事实、durable event、归档公告和完成订单不阻止软删除。
- migration 245 以 `_notx` 在线迁移建立两个 active 部分唯一索引：平台组按 `lower(name)` 全局唯一，租户组按 `(owner_user_id, lower(name))` 唯一，并在两个新索引有效后删除旧全局索引。`data-preflight.sql` 的普通名称和 `%-default` 清单同步输出 `owner_user_id` 并按相同范围聚合，避免把不同 Owner 的合法同名误报为冲突。
- 前端新增 `/groups` opt-in 路由、侧栏项、API/types、中英文文案和完整管理页，覆盖列表、检索、排序、分页、详情、创建、编辑、删除、空平台目录、错误重试和响应式布局。公共有效 `self_service_hosting_enabled`、Backend Mode 与 SIMPLE Mode 继续共同 fail closed；生产 Account/Group 目录都保持空。

### 本地自动化验证

| 命令/门禁 | 结果 |
| --- | --- |
| Group service/repository/handler/routes、migration 245 与 scoped reader 聚焦测试 | 通过；覆盖可信 Actor、Owner conceal、strict DTO、安全默认值、CAS、Policy 重检、事务 rollback、Outbox/event 和路由接线 |
| `cd backend && go test -tags=integration ./internal/repository -run 'TestSelfServiceGroupBlockingReferences' -count=1` | 通过；真实 PostgreSQL 验证数组、JSON 配置、公告和订单引用矩阵，归档公告/完成订单不阻止删除 |
| `cd backend && go test -tags=integration ./migrations -run 'TestGroupNameDataPreflightUsesOwnerScopePostgres' -count=1` | 通过；真实 PostgreSQL 验证平台与同 Owner 冲突会报告，不同 Owner 合法同名不报告，普通名称与 `%-default` 两份清单一致 |
| `make -C backend test-unit` | 通过；完整 unit-tag backend tree 成功 |
| `cd backend && go vet ./...` | 通过；全仓静态检查成功 |
| `make -C backend build` | 通过；生产 server 与 Wire 构建成功 |
| `cd backend && go test -tags=integration ./... -run '^$' -count=1` | 通过；全 integration 标签树编译成功，不代表远端无过滤 Testcontainers 动态执行 |
| 9 个 2.3 前端聚焦 Vitest 文件 | 通过；82 tests，覆盖 Group API/view、feature flag、router/sidebar 和共享 guard |
| `cd frontend && npm run test:run` | 通过；254 files、1818 tests |
| `cd frontend && npm run typecheck` | 通过 |
| `cd frontend && npm run lint:check` | 通过，0 errors |
| `cd frontend && npm run build` | 通过；Vite production build 完成，仅有仓库既有分块告警 |
| `npm exec --yes --package=pnpm@9.15.9 -- make build` | 通过；根目录前后端构建成功，使用仓库兼容的 pnpm 9，未产生 lockfile churn |
| `openspec validate redesign-resource-access-control --type change --strict --no-interactive` | 通过，change is valid |
| `git diff --check` 与 `git diff --quiet -- frontend/pnpm-lock.yaml` | 通过；无 whitespace 错误，锁文件无差异 |
| GitHub Actions [PR CI Run 33649130206](https://github.com/savvym/sub2api/actions/runs/33649130206) / [test job 100311347354](https://github.com/savvym/sub2api/actions/runs/33649130206/job/100311347354) | 通过；完整 SHA `bf19faedf2bf2b4920d61e7058ae95eabb5d487e`，test 10m03s，其中 unit 6m05s、无过滤 integration 3m35s；golangci-lint、frontend 和 shell 同 Run 成功 |
| GitHub Actions [push CI Run 33649126676](https://github.com/savvym/sub2api/actions/runs/33649126676) / [test job 100311332689](https://github.com/savvym/sub2api/actions/runs/33649126676/job/100311332689) | 通过；相同 SHA，test 9m58s，其中 unit 6m03s、无过滤 integration 3m33s；golangci-lint、frontend 和 shell 同 Run 成功 |
| GitHub Actions [PR Security Scan 33649130214](https://github.com/savvym/sub2api/actions/runs/33649130214) 与 [push Security Scan 33649126817](https://github.com/savvym/sub2api/actions/runs/33649126817) | 通过；相同 SHA 的 backend `govulncheck`、frontend `pnpm audit` 与 audit exception 门禁均成功 |

### 剩余发布边界

- 生产 `SelfServiceAccountCatalog` 与 `SelfServiceGroupCatalog` 均为空，OAuth、导入、复制、批量、自定义 endpoint 和全部候选产品继续禁止；Feature Flag 默认关闭，`role_authorization_mode=legacy`。2.3 完成不启用任何自助资源，也不构成 `Release Accepted`。
- 新建 Account 仍固定 private、ungrouped、`schedulable=false`。2.5 的 Owner 私有默认组同事务绑定与 2.6 的 group `0`/平台默认组/SIMPLE Mode 租户隔离尚未实现，不能通过创建 Group 规避这些门禁。
- 当前没有 production/staging、真实数据或旧 Worker。2.3 SHA `bf19faedf2bf2b4920d61e7058ae95eabb5d487e` 的 PR/push 无过滤 Testcontainers、lint 与 Security Scan 已按上述 Run 归档；这些代码级证据不替代首次真实数据预检、目标环境验证或 `Release Accepted`。
- 下一切片为 2.4 的 OAuth、导入、复制、批量和 callback 可信 Owner 绑定。即使实现完成，空 allowlist、出站安全清单与首次启用 `Release Accepted` 门禁仍必须保持。

## 2026-09-02 - Trusted Account Creation and OAuth Callback Binding（2.4）

### 实现范围

- 新增 server-owned `accountCreationAuthority` 与 `oauthflow.Binding`。authority 只能从可信 `authz.Actor` 构造，HTTP/import DTO 不能提交或替换它；管理员 JWT 产生平台 Owner、记录相同 JWT user creator，Admin API Key Service Principal 产生平台 Owner且 creator 为空。应用 authority 时会覆盖输入中的 `owner_user_id`、`created_by_user_id` 和 `public_access_level`，防止复制、导入或批量 payload 继承/伪造租户归属。
- 管理员基础 `AccountService` 创建、AdminService 单建及其 OAuth 后建号/导入/批量 sink、复制、OpenAI spark shadow 和 CRS 新建帐号统一应用平台 authority。复制和 shadow 不再继承源帐号 Owner；CRS 只对新建帐号应用 authority，匹配到的存量帐号更新继续保留已有 Owner/creator。普通自助 Account 创建改为从同一 authority 构造可信 JWT User Owner/creator，Service Principal 不能进入自助创建。
- Claude、OpenAI、Grok、Gemini 和 Antigravity OAuth session 新增 server-owned Actor subject、Owner kind、可选 Owner user ID、proxy ID 和已有流程参数。管理员生成 URL 时写入平台 binding；callback 重新从当前可信 Actor 构造 binding 并与 session 精确比较，missing/tampered binding 或其他 Actor 均在上游和 session 消费前拒绝。
- callback 只能确认、不能替换 session 中的 proxy、redirect URI、OAuth type、tier 与 state。OpenAI/Grok 校验 redirect，Gemini 校验 type/tier，五类流程都校验 proxy；OpenAI/Gemini/Antigravity/Grok 以固定时间比较 callback state，Claude callback 不接受可覆盖 state，而是把 session state 直接传给 token exchange。空 code、缺失依赖和不合法 flow state 也在消费前失败。
- 五个 session store 新增一次性 `TryConsumeSession`。所有合法 callback 都在首次上游 token exchange 前原子 claim，并在结束时删除；上游失败后也不能重放。并发 callback 只有一个能到达上游，loser 在 winner 尚未完成时即失败。无效 Actor/state/proxy/redirect/type/tier 不消费 session，修正请求后仍可继续。
- xAI/Grok Redis session DTO 同步持久化 binding 与 proxy ID，并通过 Redis compare-and-delete 跨实例原子消费；remote write 失败才允许本地 fallback，成功写入的 remote session 不因单实例本地状态形成第二个消费机会。
- 本切片没有新增普通用户 OAuth/import/copy/batch 路由，没有增加生产 self-service 产品或 OAuth allowlist，也没有修改数据库 setting。全部新增 Feature Flag 保持关闭且 `role_authorization_mode=legacy`；2.4 是既有创建和 callback 路径的可信归属/重放加固，不是 self-service `Release Accepted`。

### 本地自动化验证

| 命令/门禁 | 结果 |
| --- | --- |
| `cd backend && go test ./internal/pkg/oauthflow -count=1` | 通过；覆盖平台/User binding 的合法形态、subject/Owner 一致性与拒绝边界 |
| `cd backend && go test -tags=unit ./internal/service -run 'Test(AccountCreationAuthorityOverwritesUntrustedOwnership\|OwnedAccountCreationAuthorityBindsJWTUser\|AccountCreationWriteSinksApplyPlatformAuthority\|AdminOAuthFlowsBindCallbackToInitiatingActor\|OpenAIOAuthCallbackRejectsMissingOrTamperedBindingWithoutConsumption\|OpenAIOAuthCallbackUsesServerFlowStateAndConsumesOnce\|GeminiOAuthCallbackRejectsTypeAndTierOverridesWithoutConsumption\|OpenAIOAuthUpstreamFailureCannotBeReplayed\|OpenAIOAuthConcurrentCallbacksReachUpstreamOnce)$' -count=1` | 通过，1.521s；覆盖五类管理员 flow Actor 绑定、OpenAI/Gemini server-owned 参数、无效 binding 不消费、上游失败不可重放、并发 callback 单 winner，以及管理员/self-service Owner 归属 |
| `cd backend && go test -tags=unit ./internal/service -count=1` | 通过；`internal/service` 非缓存执行 161.393s |
| 上述 service 聚焦命令增加 `-race` | 通过，2.781s；覆盖 callback claim 与 Account creation authority 的并发/归属路径 |
| `cd backend && go test -race ./internal/pkg/oauth ./internal/pkg/openai ./internal/pkg/geminicli ./internal/pkg/antigravity ./internal/pkg/xai -run 'TestSessionStore(TryConsumeSessionConcurrent\|RedisRoundTripsBindingAndConsumesAcrossInstances\|RedisFallbackIsLimitedToFailedWrites)$' -count=10` | 通过；Claude/OpenAI/Gemini/Antigravity/xAI 每轮并发消费恰好一个 winner，xAI 另覆盖跨实例 Redis round-trip/consume |
| `make -C backend test-unit` | 通过；完整 unit-tag backend tree 成功 |
| `cd backend && go test ./... -count=1` | 通过；默认标签全仓测试成功 |
| `cd backend && go vet -tags=unit ./...` | 通过 |
| `cd backend && go test -tags=integration ./... -run '^$' -count=1` | 通过；全 integration 标签树编译成功，不代表远端无过滤 Testcontainers 动态执行 |
| `cd backend && CGO_ENABLED=0 go build ./cmd/server` | 通过；生产 server 构建成功 |
| `openspec validate redesign-resource-access-control --type change --strict --no-interactive` 与 `git diff --check` | 通过 |
| `cd backend && GOBIN=/tmp/sub2api-tools go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2 && /tmp/sub2api-tools/golangci-lint run --timeout=30m` | 通过，`0 issues`；与 GitHub Actions 的 v2.13 系列一致 |

### 远端 CI / Security Scan

首次代码提交 `a875536ccb6f97e58de9d6f81ea4aa71abe6a05b` 的 push [CI Run 33657551511](https://github.com/savvym/sub2api/actions/runs/33657551511) 与 PR [CI Run 33657568910](https://github.com/savvym/sub2api/actions/runs/33657568910) 均只有 golangci-lint job 失败；两边 test job 的 unit 与无过滤 integration、frontend 和 shell 均成功，对应 push/PR Security Scan `33657551526`/`33657569173` 也成功。GitHub lint 输出包含 Gemini OAuth 未使用的 `redirectURI` 参数 SA4009、3 个错误字符串大写 ST1005 和 1 条 SA4009 related annotation；本地固定版本 lint 随后又报告 2 个同类 ST1005。提交 `bf4903ab92095acc4f11cc477cc7777c14d53d8f` 移除无效参数并修复全部 5 个错误字符串，最终本地 lint 为 `0 issues`。

| 远端门禁 | 结果 |
| --- | --- |
| [push CI Run 33659274194](https://github.com/savvym/sub2api/actions/runs/33659274194) / [test job 100345512840](https://github.com/savvym/sub2api/actions/runs/33659274194/job/100345512840) | 通过；SHA `bf4903ab92095acc4f11cc477cc7777c14d53d8f`，test 10m19s，unit 6m13s，无过滤 integration 3m40s，repository 非缓存运行 52.288s；[golangci-lint job 100345512876](https://github.com/savvym/sub2api/actions/runs/33659274194/job/100345512876)、frontend 与 shell 同 Run 成功 |
| [PR CI Run 33659279649](https://github.com/savvym/sub2api/actions/runs/33659279649) / [test job 100345528974](https://github.com/savvym/sub2api/actions/runs/33659279649/job/100345528974) | 通过；相同 SHA，test 9m02s，unit 5m22s，无过滤 integration 3m10s，repository 非缓存运行 36.991s；[golangci-lint job 100345528797](https://github.com/savvym/sub2api/actions/runs/33659279649/job/100345528797)、frontend 与 shell 同 Run 成功 |
| [push Security Scan 33659274223](https://github.com/savvym/sub2api/actions/runs/33659274223) 与 [PR Security Scan 33659279961](https://github.com/savvym/sub2api/actions/runs/33659279961) | 通过；相同 SHA 的两个 backend `govulncheck ./...` 均报告代码受影响漏洞 0 个，两个 frontend `pnpm audit` 与 audit exception 门禁均成功 |

### 剩余发布边界

- Draft PR #1 的 push/PR CI、无过滤 Testcontainers、固定版本 golangci-lint 与 Security Scan 已在相同代码 SHA 上通过并补录。该结果只完成 2.4 的工程门禁；当前无 production/staging、真实数据或旧 Worker，且生产目录、OAuth allowlist 与全部新增 Feature Flag 仍关闭，不能据此标记 self-service `Release Accepted`。
- OAuth session claim 解决 callback 重放与跨 Actor 使用，不提供跨上游与 PostgreSQL 的分布式事务。token exchange 已发生后若后续本地建号失败，不能通过重放 callback 自动补偿；需要沿既有受审计恢复流程处理。
- 下一切片为 2.5 的 Owner 私有默认分组按需创建及 Account 同事务绑定。生产目录、OAuth allowlist、Feature Flag 和 authorization mode 继续保持当前关闭状态。

## 2026-09-02 - Owner Private Default Group Binding（2.5）

### 实现范围

- 自助 Account 创建为每个 Owner/平台保留 `<platform>-default` 名称。Repository 在调用方 `SERIALIZABLE` 事务中按 `owner_user_id + lower(name)` 查找并锁定未删除 Group，Service 对 Owner、平台、名称、active、private、exclusive、legacy authorization mode 和正 `access_version` 再校验；已有合法默认组直接复用，不重复消耗 Group 配额。
- 默认组缺失时，Account 创建先完成 Account 容量检查，再执行 Group 容量检查并调用与 2.3 普通私有 Group 创建共享的 `createSelfServiceGroupRecord`。自动 Group 因此固定相同 Owner/creator、private、active、exclusive、access version 1、legacy authorization mode，并在同一事务写 group Scheduler Outbox；唯一冲突映射为稳定 Account 409 conflict。
- 自助 Account 仍只接受服务端 product ID、名称和 API Key，客户端不能指定平台、类型或 Group ID。Repository 固定 Account private、active、`schedulable=true`，随后以优先级 50 插入 `account_groups`；绑定 SQL同时重校验 Group ID、Owner、大小写折叠名称、平台、active、private、exclusive、legacy 和未删除状态，任何不一致均拒绝提交。
- Account Scheduler Outbox payload 携带默认 group ID。新建默认组时，Service 依次写 `group.created` 与 `account.created` durable authorization event；Account event 的 changed fields 增加 `account_groups` 和 `schedulable`。Group/Account、关系、两类 Outbox 和两条 event 共用一次 commit，绑定、Outbox、event 或 commit 失败整体回滚。
- Account 内部 mutation state 增加 `Schedulable`，lock/rename/delete 的 SQL 列与 scanner 同步更新，创建成功必须验证它为 true。公开 `AccountListItem` 和 Handler DTO 未增加该字段，响应继续不暴露 Group ID、调度状态、Owner ID、凭据或关系。
- 新增 service 调用顺序、首次/复用容量分支、Group 配额拒绝、绑定失败、第二条 event 失败和 event 字段测试；PostgreSQL integration 用例覆盖完整提交、优先级/Outbox payload、多个 Account 复用、并发首次创建单组，以及 binding/Outbox/event 故障的零部分状态。Fixture cleanup 同时删除 Owner 的 Account 与 Group 产物。
- 本切片不修改生产 Account/Group 目录、OAuth allowlist、Feature Flag、SIMPLE Mode 护栏或 `role_authorization_mode=legacy`。2.6 的 group `0`、平台默认组和 SIMPLE Mode `owner_user_id IS NULL` 查询隔离尚未实现，不能因 2.5 已使帐号可调度就提前开启 self-service。

### 本地自动化验证

| 命令/门禁 | 结果 |
| --- | --- |
| `cd backend && go test ./... -count=1` | 通过；默认标签全仓成功，`internal/service` 非缓存运行 116.853s |
| `make -C backend test-unit` | 通过；完整 unit-tag backend tree 成功，`internal/service` 运行 166.239s |
| `cd backend && go test -race -run 'TestSelfServiceAccount' -count=1 ./internal/service` | 通过，2.539s；覆盖默认组容量/复用、事务回滚和 Account mutation 并发契约 |
| `cd backend && go test ./internal/handler -run 'TestSelfServiceAccountCreate' -count=1` | 通过，1.713s；Handler 测试替身同步默认组接口，公开投影仍不泄露 Group/调度字段 |
| `cd backend && go vet -tags=unit ./...` | 通过 |
| `cd backend && go test -tags=integration ./... -run '^$' -count=1` | 通过；全 integration 标签树编译成功，不代表 PostgreSQL/Testcontainers 动态执行 |
| `make -C backend build` | 通过；版本 `0.1.183` 的 CGO-off 生产 server 构建成功 |
| `cd backend && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.0 run --timeout=30m ./...` | 通过，`0 issues`；版本固定为 CI 的 v2.13 系列 |
| changed Go files `gofmt -d` 与 `git diff --check` | 通过；无格式或 whitespace 差异 |
| `openspec status --change redesign-resource-access-control` 与 strict validate | 通过；4/4 planning artifacts complete，change is valid |

### 远端 CI / Security Scan

- `make -C backend test-integration` 在本机执行到 repository TestMain 后检测到 `docker command unavailable` 并显式跳过，退出码 0 不能作为动态通过证据。聚焦 `TestSelfServiceAccountRepository*` 重跑也明确输出 `docker is not available; skipping integration tests`；因此当前只记录 integration 标签编译与测试代码覆盖，不伪造本机 PostgreSQL/Testcontainers 结果。
- 代码提交 `f532b2e5ecc0da16aa6d831d239f82a6633d1290` 的 push 与 Draft PR 两套无过滤 `make test-integration` 均成功。repository 包分别非缓存运行 39.640s 和 53.406s，覆盖默认组完整原子提交、已有组复用、并发首次创建单组，以及 binding/Outbox/event failure injection；固定版本 lint、frontend、shell 与两套 Security Scan 同时通过。

| 远端门禁 | 结果 |
| --- | --- |
| [push CI Run 33667177588](https://github.com/savvym/sub2api/actions/runs/33667177588) / [test job 100371684868](https://github.com/savvym/sub2api/actions/runs/33667177588/job/100371684868) | 通过；test 8m58s，unit 5m22s，无过滤 integration 3m12s，repository 39.640s；[golangci-lint job 100371684889](https://github.com/savvym/sub2api/actions/runs/33667177588/job/100371684889)、frontend 与 shell 同 Run 成功 |
| [PR CI Run 33667182608](https://github.com/savvym/sub2api/actions/runs/33667182608) / [test job 100371700879](https://github.com/savvym/sub2api/actions/runs/33667182608/job/100371700879) | 通过；test 10m10s，unit 6m09s，无过滤 integration 3m37s，repository 53.406s；[golangci-lint job 100371700951](https://github.com/savvym/sub2api/actions/runs/33667182608/job/100371700951)、frontend 与 shell 同 Run 成功 |
| [push Security Scan 33667177598](https://github.com/savvym/sub2api/actions/runs/33667177598) 与 [PR Security Scan 33667182624](https://github.com/savvym/sub2api/actions/runs/33667182624) | 通过；相同 SHA 的 backend `govulncheck`、frontend `pnpm audit` 与 audit exception 门禁均成功 |

- Draft PR #1 的 base 仍为 `main@efb46db0a960fdad94502b1c3a982a0051cf5245`，head 为上述代码 SHA，状态为 Draft/CLEAN；未转 Ready 或合并。
- 生产目录、OAuth allowlist、Feature Flag 和 authorization mode 继续保持关闭；当前没有 production/staging、真实数据或旧 Worker。远端工程证据完成也不等于 self-service `Release Accepted`，下一切片仍为 2.6 的 group `0`、平台默认组和 SIMPLE Mode 租户隔离。
