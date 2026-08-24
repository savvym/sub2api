## Context

详细现状、威胁模型、数据模型、API 草案和 Phase 0-5 路线位于 `docs/resource-access-control-redesign.md`。本文件只记录实施必须固定的决策与边界，避免长任务在多次续作中发生语义漂移。

当前系统的重要约束：

- `api_keys.user_id` 已表达 Key 所有权；帐号和分组没有 Owner。
- `user_allowed_groups` 与 `groups.is_exclusive` 表达旧分组消费资格，不是通用 ACL。
- Scheduler 的 group `0` 是全局未分组兼容桶；租户帐号不能进入该桶。
- API Key 鉴权快照和 Scheduler 帐号快照是两条独立缓存/Outbox 链。
- 生产 Schema 由版本化 SQL migration 驱动；Ent 生成模型必须保持一致。

## Goals / Non-Goals

**Goals:**

- 有资格用户可以管理自己默认私有的帐号、分组和 API Key。
- 支持用户、角色和全站范围的 viewer/consumer/maintainer/manager 分享。
- 保留管理员全局治理与存量用户调用行为。
- 授权、撤权、到期和用户停用及时影响控制面与数据面。
- 分享不泄露凭证、底层帐号、消费者私密信息或资源存在性。
- 所有变化可审计、可观测、可灰度、可在规定边界内回滚。

**Non-Goals:**

- V1 不引入 Workspace、组织层级、显式 Deny 或通用策略语言。
- V1 不分享 API Key，不实现多人共同拥有和资源交易结算。
- 不重写网关协议、调度算法或现有计费公式。
- 普通资源权限不允许导出上游凭证原文。

## Decisions

### 1. V1 使用用户 Owner

状态：Accepted（2026-08-20）。

帐号和分组使用 nullable `owner_user_id`；`NULL` 表示平台资源。V1 不引入 Workspace。Owner/creator 外键采用 restrict/no-action 语义，用户硬删除前必须先转移或平台接管资源。

### 2. 平台 RBAC 与资源 ACL 分层

状态：Accepted（2026-08-20）。

创建资格由平台能力决定，已有资源动作由 ACL 决定。Owner 有隐含资源权限，但分享和转移仍需额外平台能力。管理员旁路必须来自可信 Actor，不能由请求字段或旧角色与新角色长期 OR 得出。

### 3. 分享默认值与访问级别

状态：Accepted（2026-08-20）。

快捷分享默认 viewer；帐号只有 consumer 及以上才可用于接受者有权编辑的分组。viewer/consumer/maintainer/manager 映射为后端固定动作集合，不允许前端拼装动作。

### 4. 撤权、计费和停用语义

状态：Accepted（2026-08-20）。

- 撤销 `group.use` 后立即拒绝既有 Key 的调用，但保留绑定供用户改绑。
- 撤销 `account.use` 后，无其他授权或有效 Owner 批准时移除跨 Owner 关系并刷新调度。
- API Key Owner 扣平台余额；Account Owner 承担上游额度；历史报表保存请求时 Owner 快照。
- Owner 用户停用或软删除时暂停其托管帐号，管理员处理后才能恢复。

### 5. 默认私有与分享不级联

状态：Accepted（2026-08-20）。

新资源必须以私有状态创建。分享分组不授予底层帐号可见性；分享帐号不分享包含它的分组。Owner、manager 和管理员的常规 DTO 均不返回凭证原文；受控导出属于独立 break-glass 能力。

### 6. 名称、默认分组和 group 0

状态：Accepted（2026-08-20）。

分组名称最终改为 Owner 范围内大小写不敏感唯一。每个 hoster 按需拥有真实 ID 的私有默认分组。group `0` 和平台默认组查询只允许 `owner_user_id IS NULL`；SIMPLE Mode 不得绕过该条件。

### 7. 渐进迁移与撤权窗口

状态：Accepted（2026-08-20）。

所有新开关默认关闭；角色权威源使用 `legacy/shadow/rbac`，分组使用 `legacy/shadow/acl`。主动撤权目标为 99.9% 在 5 秒内收敛，任何允许缓存硬上界 30 秒；到期边界必须同步拒绝，不能只等待协调器清理。

### 8. 第一开发切片为 dark schema foundation

状态：Accepted（2026-08-20）。

第一切片只增加默认关闭的设置、additive SQL migration、Ent 同步、系统角色/权限种子和幂等用户角色回填。不增加普通用户路由/UI，不开放 ACL，不修改全局分组名称唯一索引，不改变存量调度或管理员行为。

### 9. Policy 判定与紧急关闭语义

状态：Accepted（2026-08-20）。

- 多个有效来源提供相同最高访问级别时，审计归因固定为 public、直接用户 Grant、角色 Grant；同类 Grant 选择最小 Grant ID，角色 Grant 再以最小 Role ID 决定，避免查询计划改变审计结果。
- `group_sharing_enabled` 或 `account_sharing_enabled` 的有效值关闭时，对应资源已有 public 和直接用户 Grant 立即停止放行，但不删除数据；重新开启后只恢复仍未过期且主体有效的来源。角色 Grant 还必须同时满足 `role_based_resource_grants_enabled`。
- 总开关或 self-service 有效值关闭时，Owner/ACL 自助放行 fail closed；存量 legacy 管理员治理仍按阶段权威源兼容，不能把这一兼容扩大为普通用户放行。
- 在任务 2.6 完成 group `0` 与平台默认组的 `owner_user_id IS NULL` 隔离前，SIMPLE Mode 必须在 Setting、Policy 和 SQL Scope 边界把 self-service 及两类 sharing 的有效值强制为 false；数据库原始 true 值只保留配置意图，不能产生普通用户允许。解除该临时发布护栏必须重新评审兼容矩阵。
- System Actor 默认不能通过通用 `CheckCapability`、`CanCreate` 或 `Authorize`。需要持久授权写入的 Worker 必须使用 Service Principal；未来只读 Worker 逐项评审并维护显式 allowlist，不提供全局系统旁路。

## Risks / Trade-offs

- RBAC、ACL、旧分组资格并存期间容易形成多个允许源；任何阶段只能有一个权威允许判定源，shadow 只比较不放行。
- 角色 Grant 可影响大量资源；必须限制规模并使用版本、Outbox 和到期协调器。
- 分组分享扩大受众可能使借入帐号越权；扩大分享前必须同步校验完整受众闭包。
- 现有审计队列和部分 Scheduler 写入会 best-effort 丢失；安全关键变更必须改为事务内 durable event/outbox。
- 本地没有 Docker，repository integration 不能用 Testcontainers 严格执行；可使用本地 PostgreSQL 补专项验证，但不能冒充 CI 集成门禁。

## Migration Plan

1. **Expand**：新增 RBAC/ACL 结构和默认关闭开关，存量 Owner 为 NULL、分组为 legacy。
2. **Backfill**：回填系统角色和旧分组等价 Grant，生成数据预检与差异报告。
3. **Shadow**：新旧角色/分组授权并行计算，响应仍使用旧权威源，只记录差异。
4. **Deploy**：所有实例认识新快照、版本和 Outbox 后才允许新租户资源。
5. **Cutover**：按明确批次切换到 rbac/acl，递增版本并事务写入失效事件。
6. **Observe**：至少观察一个发布周期，旧表只读保留，不作为撤权兜底允许源。
7. **Contract**：独立版本停止兼容写并清理旧入口/字段，明确结束旧版本回滚能力。

## Resolved Decisions

- `platform.secret.export` 只定义一次，不因 admin 角色或普通资源 Grant 隐式获得。
- 平台资源由管理员操作时 `owner_user_id=NULL`；`created_by_user_id` 记录实际自然人。机器身份创建时通过 durable audit 的 Service Principal 归因，不能伪造用户 creator。
- 分享目标搜索必须使用精确标识、限频和不可枚举响应；模糊用户目录不属于 V1 分享能力。
- Backend Mode 优先级高于全部资源功能开关；关闭 Backend Mode 不自动授予 hoster。
