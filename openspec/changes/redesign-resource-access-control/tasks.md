## 0. 产品决策与安全基线

- [x] 0.1 冻结 V1 `owner_user_id`、访问预设、撤权、计费、停用、名称和传播窗口决策。
- [x] 0.2 确认 Backend Mode、SIMPLE Mode、私有默认分组和 group `0` 隔离原则。
- [x] 0.3 建立帐号/分组全部写入口与引用 ID 权限覆盖清单。
- [ ] 0.4 建立 credentials/extra 敏感子键、查询依赖、缓存依赖和加密迁移清单。
- [ ] 0.5 冻结自助平台/认证类型 allowlist、SSRF/出站限制和限频策略。
- [x] 0.6 编写只读数据预检：角色异常、孤立授权、同 Owner 名称冲突、默认组重名和回填规模。
- [x] 0.7 完成 Feature Flag × Backend Mode × SIMPLE Mode × authorization mode 兼容矩阵。
- [ ] 0.8 评审 Phase 0 退出门槛，确认没有未决安全阻塞。

## 1. 权限基础设施（默认关闭）

- [x] 1.1 新增 RBAC、Service Principal、用户授权版本、系统角色/权限种子和幂等回填 SQL。
- [x] 1.2 新增资源 Owner/public/access version/mode、帐号/分组 Grant 和 durable authz event SQL。
- [x] 1.3 同步 Ent Schema、生成代码，并验证 SQL 与 Ent 字段/约束一致。
- [x] 1.4 新增 5 个默认关闭 Feature Flag 和 `role_authorization_mode=legacy`，验证缺失/错误时 fail closed。
- [x] 1.5 新增 Actor、能力、动作、访问级别与稳定拒绝原因领域类型。
- [x] 1.5a 修复 fresh setup 管理员兼容角色的事务性引导，并为已受影响实例增加幂等补偿迁移。
- [x] 1.6 实现 PolicyService 决策矩阵和 Repository SQL 可访问范围。
- [x] 1.6a 实现 PolicyService、可信 AccessibleScope、PolicyStore 和 Account/Group SQL scope predicate，并完成 PostgreSQL 动态验证。
- [x] 1.6b 将同一 SQL scope 接入帐号/分组 scoped reader，覆盖筛选、total、排序和分页；聚合与 edge hydration 在普通读取模型中显式禁用，并使用独立窄字段 DTO，不复用管理员敏感 DTO。
- [x] 1.7a 实现 RoleService core：在 legacy/shadow 阶段原子维护 `users.role`、`system_bootstrap` 兼容 `user_roles`、`users.authz_version` 和 durable cache invalidation；补齐角色约束、readiness、RBAC 硬拒绝、通用 settings 写保护与生产 DI。
- [ ] 1.7b 新增专用 `role_authorization_mode` 管理入口，强制 step-up authentication 和 durable audit；入口完成前不得宣称 1.7 完成或开放 shadow/rbac 切换。
- [ ] 1.8 管理员 JWT 接入 User Actor，Admin API Key 接入独立 Service Principal Actor。
- [ ] 1.9 帐号/分组管理员入口显式传 Actor，保持响应和行为不变。
- [ ] 1.10 把安全关键资源写、durable audit、Auth Outbox、Scheduler Outbox 纳入同一事务。
- [ ] 1.11 实现授权/角色到期协调器、同步到期拒绝、5 秒/30 秒指标与降级门。
- [ ] 1.12 完成跨租户、TOCTOU、SQL scope、模式组合和迁移测试；批准 Phase 1 退出。

## 2. 私有自助托管

- [ ] 2.1 实现 hoster 资格、帐号/分组配额和管理员分配入口。
- [ ] 2.2 实现普通用户私有帐号 CRUD、字段投影和简化创建向导。
- [ ] 2.3 实现普通用户私有分组 CRUD，并限制平台策略字段。
- [ ] 2.4 覆盖 OAuth、导入、复制、批量和 callback 的可信 Owner 绑定。
- [ ] 2.5 按需创建 Owner 私有默认分组，帐号创建与绑定同事务。
- [ ] 2.6 让 group `0`、平台默认组与 SIMPLE Mode 查询排除租户帐号。
- [ ] 2.7 完成配额、SSRF、凭证、跨租户和存量兼容测试；批准 Phase 2 退出。

## 3. 分组分享

- [ ] 3.1 实现用户、角色、全站 Group Grant 和分享管理 API/UI。
- [ ] 3.2 实现目标搜索的精确匹配、限频、不可枚举和审计。
- [ ] 3.3 影子比较并按分组迁移 `user_allowed_groups` 到 ACL consumer Grant。
- [ ] 3.4 API Key 创建/更新/运行时统一检查 `group.use` 与业务资格。
- [ ] 3.5 完成多实例缓存失效、到期、WebSocket、异步任务和订阅叠加测试。
- [ ] 3.6 灰度 `group_sharing_enabled` 并按资源批次切换到 acl。

## 4. 帐号分享

- [ ] 4.1 实现 Account Grant、分级 DTO、维护动作和分享管理 API/UI。
- [ ] 4.2 扩展 account_groups 的链接人、授权来源、Owner 批准、状态和验证版本。
- [ ] 4.3 实现帐号受众覆盖分组受众的闭包检查，禁止借公开分组转授权。
- [ ] 4.4 实现撤权、到期和角色变化后的关系重算及 Scheduler Outbox。
- [ ] 4.5 实现 Account Owner 的预算、消耗和共享来源聚合观测。
- [ ] 4.6 完成凭证泄漏、派生读面、闭包、撤权和调度一致性测试。

## 5. 收口与清理

- [ ] 5.1 将资源授权和角色授权分别切换为 acl/rbac 权威读取。
- [ ] 5.2 停止旧授权写入并观察至少一个发布周期。
- [ ] 5.3 删除或只读化 `user_allowed_groups/is_exclusive` 的旧授权入口。
- [ ] 5.4 评估移除 `users.role` 单角色兼容字段和旧前端入口。
- [ ] 5.5 完成迁移、回滚、SLA、泄漏和全量回归证据。
- [ ] 5.6 将本变更归档为已发布 capability，并更新长期运维手册。
