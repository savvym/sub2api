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

### 未完成门禁

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
