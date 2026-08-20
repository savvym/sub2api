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
- 当前本地业务库没有 `users` 表，`data-preflight.sql` 尚未对真实数据运行。
- credentials/extra 清单和自助 outbound allowlist 尚待负责人复核，不得据此开放 Phase 2。
