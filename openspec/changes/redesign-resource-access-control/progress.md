# 当前进度

更新时间：2026-08-20

## 当前状态

- 当前阶段：Phase 0 安全基线 + Phase 1 dark schema foundation。
- 当前分支：`codex/resource-access-control-foundation`。
- 基线提交：`58de21e70`（总体设计文档）。
- 当前切片：1.1-1.4 已实现并完成本机验证，等待评审/提交。
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
- 没有新增普通用户资源路由/UI，也没有将任何运行时授权切换到 ACL/RBAC。
- OpenSpec 严格校验、后端完整单测/构建、前端完整测试/构建和 PostgreSQL 18 动态迁移验证通过。

## 下一步

1. 评审并提交本切片；确认所有环境继续保持新开关关闭、角色模式为 legacy。
2. 由平台/认证负责人完成 0.4 credentials/extra 清单和 0.5 自助平台/出站 allowlist 复核。
3. 对真实只读数据运行 `data-preflight.sql`，记录异常角色、名称冲突、孤立关系和回填规模。
4. 在 Docker/CI 环境执行 `CI=1 go test -tags=integration ./internal/repository` 严格门禁。
5. 完成 Phase 0 退出评审后，下一切片从 1.5 Actor/领域类型和 1.6 PolicyService 开始；不得提前增加普通用户资源路由。

## 阻塞与风险

- 本机没有 Docker，带 `integration` tag 的 repository 测试无法获得 CI 等价覆盖；不带 `CI=1` 会静默跳过，禁止把它记录为通过。
- 当前本地 `sub2api` 数据库尚未初始化业务表，因此 `data-preflight.sql` 只完成编写，尚未形成真实数据报告。
- Phase 0 的 credentials/extra 键族与自助出站 allowlist 仍需负责人复核；这是开放自助托管前的硬阻塞。
- 分组名称唯一索引本切片不修改，先完成大小写和 Owner 范围冲突预检。
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
