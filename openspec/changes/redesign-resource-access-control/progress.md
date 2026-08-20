# 当前进度

更新时间：2026-08-20

## 当前状态

- 当前阶段：Phase 0 安全评审 + Phase 1 权限基础设施。
- 当前分支：`codex/resource-access-control-foundation`。
- 基线提交：`58de21e70`（总体设计文档）。
- Foundation 提交：`215536582`，已推送 `origin/codex/resource-access-control-foundation`。
- Authorization Domain Contract 提交：`0d66334c9`，已推送同一远程分支。
- Fresh Setup Compatibility Bootstrap 提交：`4505b0301`，已推送同一远程分支。
- Policy 与 SQL Scope Foundation 提交：`75b6582c2`，已推送同一远程分支。
- Scoped Resource Reader 提交：`af49971aa`，已推送同一远程分支。
- 当前切片：0.4/0.5 静态审计已到 Review Ready；1.7a/1.7b 已完成完整单测、构建、OpenSpec、本地 HTTP 与 PostgreSQL 动态门禁，专用 mode status/readiness 与 guarded transition API 已接入生产 Wire；下一开发切片为 1.8 ActorResolver。RBAC 未交付，Phase 0 尚未批准退出。
- 当前权威行为：旧 `users.role` 与旧分组资格；不得启用任何新 ACL 放行。

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

## 下一步

1. 由平台/认证/安全负责人复核并批准 0.4 credentials/extra 清单和 0.5 自助平台/出站 allowlist。
2. 对真实服务器只读数据运行 `data-preflight.sql`，记录异常角色、名称冲突、孤立关系和回填规模。
3. 在 Docker/CI 环境执行 `CI=1 go test -tags=integration ./internal/repository` 严格门禁。
4. 下一开发切片进入 1.8：增加 ActorResolver，将管理员 JWT 映射为 User Actor、Admin API Key 映射为独立 Service Principal Actor，并补齐审计主体与幂等作用域。

## 阻塞与风险

- 本机没有 Docker，带 `integration` tag 的 repository 测试无法获得 CI 等价覆盖；不带 `CI=1` 会静默跳过，禁止把它记录为通过。
- 本地 `sub2api` 只是空测试实例，仅有 1 个管理员和 1 个平台默认分组；本地预检不能替代真实服务器只读报告。
- Phase 0 的 credentials/extra 与自助出站文档均为 Review Ready、尚未 Accepted；这是开放自助托管前的硬阻塞。
- fresh setup 缺失兼容角色的问题已修复，本地管理员也已由 migration 232 补齐；真实服务器升级后仍必须通过专用 GET status/readiness 入口验证全量一致性。
- 分组名称唯一索引本切片不修改，先完成大小写和 Owner 范围冲突预检。
- 1.6 scoped reader 已完成，但尚无 Actor Resolver、Handler 或 DI 接线；当前仍是 dark foundation，不能作为已开放的普通用户资源读取入口。
- `role_authorization_mode` 当前仍没有授权 consumer；专用入口和 1.7b 动态门禁已完成，但部署环境仍必须先运行 readiness，且在 consumer 迁移前保持 `legacy`。本地烟测已恢复为缺失 setting 的 legacy fallback。
- 1.7b 没有解除 RBAC 硬拒绝；必须等待 1.8 ActorResolver 和全部授权 consumer 迁移后才能另行开放，不能把 legacy↔shadow 管理入口记录成 RBAC 已交付。
- 通用管理员 settings PUT 跨多个服务不是单一数据库事务；`role_authorization_mode` 已从该路径移除，其余新开关当前没有 consumer，因此不阻塞 dark launch，但需在 1.10 收口。

## 续作检查

开始下一次开发前依次执行：

```bash
git status --short --branch
git log -5 --oneline --decorate
openspec status --change redesign-resource-access-control
openspec validate redesign-resource-access-control --type change --strict --no-interactive
```

然后读取 `tasks.md` 中未完成的最前置依赖和 `implementation-evidence.md` 的最新记录。
