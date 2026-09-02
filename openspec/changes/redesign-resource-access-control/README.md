# redesign-resource-access-control

把 sub2api 从“管理员管理全站资源、普通用户消费资源”演进为支持私有托管、分级分享和渐进迁移的多租户资源权限系统。

## 阅读顺序

1. `progress.md`：当前状态、下一步和阻塞项，是每次续作的入口。
2. `proposal.md`：变更范围、能力和影响面。
3. `design.md`：已冻结决策、阶段边界和实现约束。
4. `specs/*/spec.md`：规范性需求与验收场景。
5. `permission-coverage.md` / `credential-inventory.md` / `outbound-security.md` / `compatibility-matrix.md`：Phase 0 安全清单。
6. `tasks.md`：Phase 0-5 的可执行任务清单。
7. `verification.md`：Requirement 到自动化/人工证据的映射。
8. `implementation-evidence.md`：命令、结果、提交和评审记录。
9. `phase-0-exit-record.md`：无部署、单维护者范围的设计接受、风险边界和首次激活门禁。

详细架构蓝图和设计理由见 `docs/resource-access-control-redesign.md`。若本文档包与总体设计出现冲突，以 `specs/*/spec.md` 的规范性要求和最新 Accepted 决策为准；冲突必须先记录并评审，不能静默选择实现。

## 工作约定

- 每次开发开始前更新 `progress.md`，结束前回写当前状态、下一步和验证证据。
- `tasks.md` 只使用 `[ ]` / `[x]`；进行中的唯一任务写在 `progress.md`。
- 每个阶段使用独立分支和 PR，避免跨阶段启用 Feature Flag。
- 生产 Schema 以 `backend/migrations` 的版本化 SQL 为权威，Ent Schema 必须同步但不能代替 SQL migration。
- 权限基础设施先以 dark launch 方式合并：新增结构和默认关闭开关，不新增普通用户放行路径。
- 未经明确灰度审批，不得把 `authorization_mode` 或 `role_authorization_mode` 切到新的权威读取模式。
