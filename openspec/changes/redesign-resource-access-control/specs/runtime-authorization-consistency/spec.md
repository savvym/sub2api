## ADDED Requirements

### Requirement: API Key 运行时必须再次确认分组使用权
API Key 创建或更新时 SHALL 检查 Owner 当前的 `group.use` 与业务资格；网关每次鉴权或有界授权快照 SHALL 再次确认该权限。创建时通过 MUST NOT 成为永久允许。

#### Scenario: 已绑定 Key 的 group.use 被撤销
- **WHEN** Grant 或公开级别不再提供 group.use
- **THEN** 既有 Key MUST 在传播窗口内停止调用
- **THEN** Key 的 group_id MAY 保留供用户改绑，但旧授权表 MUST NOT 作为兜底放行

### Requirement: Auth Cache 与 Scheduler 必须独立失效并最终一致
影响 API Key 可用性的变更 SHALL 写入 Auth Cache 失效链；影响帐号候选或关系的变更 SHALL 写入 Scheduler Outbox。安全关键业务写、durable audit 和适用 Outbox MUST 在同一数据库事务中提交，任一写失败必须回滚业务变更。

#### Scenario: 撤销跨 Owner 帐号授权
- **WHEN** 撤权使 account_groups 闭包不再成立
- **THEN** 事务 MUST 同时持久化撤权、关系失效、审计和 Scheduler 事件
- **THEN** 任何一项失败 MUST 不留下仍可调度的半状态

#### Scenario: 重复消费 Outbox
- **WHEN** Worker 重试或多个实例看到同一事件
- **THEN** 消费 MUST 幂等并最终生成同一授权/调度状态

### Requirement: 撤权和到期必须满足有界传播
主动撤权 SHALL 以 99.9% 在 5 秒内收敛为目标；任何 ACL allow cache TTL MUST 不超过 30 秒。API Key 正向 allow snapshot 的 30 秒窗口 MUST 从首次权威快照创建起算，rewrite、缓存层迁移或跨实例读取 MUST NOT 重置或延长该窗口；pre-v22、缺失创建时间、已到期或相对读取实例处于未来的 snapshot MUST 视为 miss 并重新检查权威源。`expires_at <= now` 必须在同步 Policy 判定中立即视为无效，协调器只负责版本、审计、缓存和关系的最终收敛。

#### Scenario: 协调器延迟处理到期 Grant
- **WHEN** 当前时间已经到达 expires_at 但后台任务尚未运行
- **THEN** 新请求 MUST 被同步拒绝
- **THEN** 运行态 MUST 暴露协调延迟而不是继续允许

#### Scenario: Outbox 严重积压
- **WHEN** 失效延迟超过安全阈值
- **THEN** 系统 MUST 禁止扩大分享并对无法确认的新允许 fail closed
- **THEN** 恢复后 MUST 重放和对账而不是丢弃事件

#### Scenario: 跨实例读取或重写正向 API Key 快照
- **WHEN** v22 allow snapshot 已写入 L1/L2，随后同一 snapshot 被重写或由另一实例从 L2 读取
- **THEN** 总允许生存期 MUST 仍受首次创建起 30 秒绝对窗口约束，rewrite MUST NOT 续期
- **THEN** 接收实例 MUST NOT 依据本机墙钟把正向 L2 命中提升为新的 L1 deadline，跨实例期限必须保留 Redis 相对 TTL 的权威性
- **THEN** 无效 L2 旧读 MUST 只触发权威回源，MUST NOT 无条件删除 L2 或广播失效而覆盖可能已并发刷新的新值

### Requirement: 长连接、重试和异步任务不得跨越撤权边界
Responses WebSocket 的每个新 turn、粘性会话 retry/fallback 和 queued 异步任务 SHALL 在选择/复用帐号前检查当前授权版本。连接建立时的允许 MUST NOT 永久延续到后续工作。

#### Scenario: WebSocket 连接期间撤权
- **WHEN** 客户端准备发送撤权后的下一 turn
- **THEN** 系统 MUST 在调用上游前拒绝该 turn
- **THEN** 系统 MUST 不复用旧快照选择已无权使用的帐号

### Requirement: 授权权威源必须按状态机渐进切换
角色授权 SHALL 使用 `legacy/shadow/rbac`；分组消费授权 SHALL 使用 `legacy/shadow/acl`。legacy/shadow 阶段默认由旧来源决定并记录差异；唯一例外是普通 JWT 用户的 Account/Group `CanCreate` 在 shadow 下采用 RBAC 结果。rbac/acl 阶段旧字段和旧表 MUST 不再参与允许判定。

#### Scenario: shadow 下普通用户创建采用窄 RBAC 结果
- **WHEN** 普通 JWT 用户在 role shadow 下请求 Account 或 Group `CanCreate`
- **THEN** 系统 MUST 使用 RBAC 创建能力结果，并继续要求总开关、self-service、主体状态、hoster 资格和事务内配额全部满足
- **THEN** 管理员、Service Principal、其他 Policy API 和其他资源类型 MUST 继续使用 legacy 响应

#### Scenario: shadow 比较发现差异
- **WHEN** 上述普通用户 Account/Group `CanCreate` 例外之外的新旧授权结果不同
- **THEN** 系统 MUST 继续使用旧权威源响应并记录脱敏差异
- **THEN** 该批次 MUST NOT 切换到新权威源

#### Scenario: acl 模式撤权
- **WHEN** ACL 拒绝但 user_allowed_groups 仍有旧记录
- **THEN** 系统 MUST 拒绝
- **THEN** 旧记录 MUST NOT 通过 OR 条件重新放行

### Requirement: 新权限基础设施必须默认关闭并保持存量行为
系统 SHALL 提供 `resource_access_control_enabled`、`self_service_hosting_enabled`、`group_sharing_enabled`、`account_sharing_enabled` 和 `role_based_resource_grants_enabled`，全部默认 false；`role_authorization_mode` 默认 legacy。设置缺失、非法或读取失败时 MUST 使用安全默认值。

#### Scenario: 仅部署 dark schema foundation
- **WHEN** 所有新增开关保持默认值
- **THEN** 系统 MUST 不增加普通用户帐号、分组或 Grant 路由
- **THEN** 存量管理员、普通用户、API Key 和 Scheduler 行为 MUST 与升级前等价

#### Scenario: 紧急关闭资源分享
- **WHEN** 对应帐号或分组 sharing 开关的有效值从 true 变为 false
- **THEN** 已有 public 和直接用户 Grant MUST 立即停止参与新请求的允许判定，但记录 MUST NOT 被隐式删除
- **THEN** 角色 Grant MUST 同时要求对应 sharing 开关和 `role_based_resource_grants_enabled`，任一关闭即停止放行

### Requirement: 数据迁移必须可重复、可验证且按资源切换
新增 Schema SHALL 通过 forward-only additive SQL migration 提供，Ent 仅同步模型。回填脚本 MUST 可重复执行并记录源、目标和差异；切换必须按明确资源或批次递增 access_version 并写失效事件。

#### Scenario: 升级存量数据库
- **WHEN** migration 首次或重复应用
- **THEN** 不得重复系统角色、权限或 user_roles
- **THEN** 存量帐号/分组 Owner 与 public level MUST 为 NULL，access_version MUST 为 1，分组 mode MUST 为 legacy

#### Scenario: 旧实例与新租户资源短暂并存
- **WHEN** 新租户分组已创建但仍有旧实例
- **THEN** 新分组 MUST 使用 acl 且 is_exclusive=true 使旧实例 fail closed
- **THEN** 高级 Grant MUST 等全部实例升级后才能开放
