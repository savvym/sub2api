# 当前进度

更新时间：2026-08-20

## 当前状态

- 当前阶段：Phase 0 安全评审 + Phase 1 权限领域契约。
- 当前分支：`codex/resource-access-control-foundation`。
- 基线提交：`58de21e70`（总体设计文档）。
- Foundation 提交：`215536582`，已推送 `origin/codex/resource-access-control-foundation`。
- Authorization Domain Contract 提交：`0d66334c9`，已推送同一远程分支。
- 当前切片：0.4/0.5 静态审计已到 Review Ready，1.5 与 fresh setup 兼容角色引导已实现并完成本机验证；Phase 0 尚未批准退出。
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
- 没有新增普通用户资源路由/UI，也没有将任何运行时授权切换到 ACL/RBAC。
- OpenSpec 严格校验、后端完整单测/构建、前端完整测试/构建和 PostgreSQL 18 动态迁移验证通过。

## 下一步

1. 由平台/认证/安全负责人复核并批准 0.4 credentials/extra 清单和 0.5 自助平台/出站 allowlist。
2. 对真实服务器只读数据运行 `data-preflight.sql`，记录异常角色、名称冲突、孤立关系和回填规模。
3. 在 Docker/CI 环境执行 `CI=1 go test -tags=integration ./internal/repository` 严格门禁。
4. 完成 Phase 0 退出评审后实现 1.6 PolicyService 与 SQL AccessibleScope；不得提前增加普通用户资源路由。
5. 在 1.7 readiness gate 中把 migration 232 覆盖、全量兼容角色一致性和 role 双写状态作为 shadow/rbac 的强制前置检查。

## 阻塞与风险

- 本机没有 Docker，带 `integration` tag 的 repository 测试无法获得 CI 等价覆盖；不带 `CI=1` 会静默跳过，禁止把它记录为通过。
- 本地 `sub2api` 只是空测试实例，仅有 1 个管理员和 1 个平台默认分组；本地预检不能替代真实服务器只读报告。
- Phase 0 的 credentials/extra 与自助出站文档均为 Review Ready、尚未 Accepted；这是开放自助托管前的硬阻塞。
- fresh setup 缺失兼容角色的问题已修复，本地管理员也已由 migration 232 补齐；真实服务器仍必须在升级后通过 1.7 readiness gate 验证全量一致性。
- 分组名称唯一索引本切片不修改，先完成大小写和 Owner 范围冲突预检。
- 1.5 只有领域类型和测试，没有 Actor Resolver、Policy consumer 或授权放行路径；1.6 完成前不能作为权限边界使用。
- `role_authorization_mode` 当前没有运行时 consumer；进入 shadow/rbac 前必须由 1.7 的专用 transition/readiness gate 接管。
- 通用管理员 settings PUT 跨多个服务不是单一数据库事务；新开关当前没有 consumer，因此不阻塞 dark launch，但需在 1.10 收口。

## 续作检查

开始下一次开发前依次执行：

```bash
git status --short --branch
git log -5 --oneline --decorate
openspec status --change redesign-resource-access-control
openspec validate redesign-resource-access-control --type change --strict --no-interactive
```

然后读取 `tasks.md` 中未完成的最前置依赖和 `implementation-evidence.md` 的最新记录。
