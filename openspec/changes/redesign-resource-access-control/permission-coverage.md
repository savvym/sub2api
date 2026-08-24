# Permission Coverage

状态：Phase 0 覆盖基线已建立。1.9 已完成管理员帐号/分组入口及直接资源引用的 Actor 显式接线；1.10 已将下表明确标记的核心写命令接入事务内 Policy、版本、durable resource event 与适用 Outbox；1.11 新增受限到期 coordinator、传播健康门和可恢复 Outbox Worker。读取、普通用户入口、其余专用 probe/worker 及未标记写路径仍仅为 `Actor 已接线；Policy 待接入`，当前 legacy/shadow 兼容不代表 ACL/RBAC 已启用。

## 动作约定

| 资源 | 读取 | 使用/运维 | 修改 | 高影响 |
| --- | --- | --- | --- | --- |
| Account | `account.view` | `account.use` / `account.operate` | `account.edit` | `account.manage_access` / `delete` / `transfer` |
| Group | `group.view` | `group.use` | `group.edit` | `group.manage_access` / `delete` / `transfer` |

创建动作使用 `account.create` / `group.create` 平台能力；平台策略设置继续要求平台管理能力。

## 帐号 HTTP 入口

事实源：`backend/internal/server/routes/admin.go`、`backend/internal/handler/admin/account_*.go`。

| 入口族 | 代表路由/操作 | 资源/引用 ID | 目标检查 | 事务与失效 | 状态 |
| --- | --- | --- | --- | --- | --- |
| 列表与详情 | list、get、models、stats、usage、today stats | account、group filter | scoped list / `account.view` + 字段投影 | 无写入 | Actor 已接线；Policy 待接入 |
| 创建 | create、batch create | proxy、groups、parent account、routing refs | `account.create`；引用分别授权；Owner 强制绑定 | create + group links + audit + Scheduler | 单项 Create 已接入事务 Policy；batch create 仍为逐项命令 |
| 复制 | duplicate、Spark shadow | source account、parent、groups | source `account.view`；新资源 create；禁止复制不可见凭证 | 同创建；凭证复制需 durable audit | Duplicate/Shadow 已接入；replay 为无写 no-op |
| 更新 | update、bulk update、batch credentials | account、proxy、groups、routing refs | `account.edit`；凭证替换额外审计 | resource/version + audit + Scheduler | Update/Bulk 已接入；batch credentials 复用单一原子 Bulk 命令 |
| 删除 | delete、batch delete | accounts、account_groups | `account.delete`；批量原子 IDOR | delete/soft-delete + Auth/Scheduler | 单资源与 batch delete 均已接入；batch 任一失败整体回滚 |
| 运维 | test、recover、refresh、clear error/rate limit/temp state、schedulable、refresh tier/quota | account | `account.operate` | 状态写 + Scheduler；敏感刷新审计 | error/schedulable/proxy/quota 核心命令已接入；test/recover/refresh 待收口 |
| OAuth | auth URL、exchange、cookie auth、apply credentials、OpenAI/Grok refresh | account、flow state、proxy | 创建资格或 `account.edit`；Owner 来自服务端 flow | 凭证写 + audit + Scheduler/cache | 最终 Create/Update/Privacy DB 写已接入；网络副作用不可分布式回滚 |
| 导入导出 | generic data、Codex session、CRS sync | accounts、groups、proxies | 每项 scoped；导出凭证走 break-glass | 批次原子策略、逐项归因 | 经 AdminService facade 的 Create/Update 已接入；CRS 仍直接使用 repository，后台 Actor/Policy 与整批原子性待收口 |
| 上游探测 | billing probe、models sync、Ollama usage | account | `account.operate`；平台设置仍管理员 | extra/status + Scheduler（如相关） | Actor 已接线；Policy 待接入 |
| 代理反向引用 | proxy accounts、proxy fallback | proxy、accounts | proxy 权限 + 每个 account scope | bulk account event | proxy fallback Account 命令已接入；Proxy 自身 Policy 待设计 |
| 定时测试 | scheduled test plans/results | account | `account.view` / `operate` | 计划写与结果投影 | Actor 已接线；Policy 待接入 |

## 分组 HTTP 入口

| 入口族 | 代表路由/操作 | 资源/引用 ID | 目标检查 | 事务与失效 | 状态 |
| --- | --- | --- | --- | --- | --- |
| 列表与详情 | list、all、get、usage/capacity/stats | group | scoped list / `group.view`；聚合按 Actor 过滤 | 无写入 | Actor 已接线；Policy 待接入 |
| 创建/复制 | create、duplicate | source group、accounts、fallback、routing | `group.create`；源 view；引用逐项校验 | create + links + audit + Scheduler/Auth | 已接入事务 Policy；duplicate replay 为无写 no-op |
| 更新 | update、sort order | group、accounts、fallback groups、model routes | `group.edit`；平台策略字段另行限制 | version + audit + 双 Outbox | 已接入事务 Policy、版本与适用双 Outbox |
| 删除 | delete | group、API Keys、subscriptions、routes、links | `group.delete`；级联影响显式确认 | 原子级联 + 双 Outbox | 已接入事务 Policy 与原子级联 |
| Composite | list/create/update/delete/preview routes | source group、target groups/models | source `group.edit`；所有目标 `group.use/view` | route + version + Auth/Scheduler | 写命令已接入；list/preview 读取仍待 Policy/Scope |
| 用户覆盖 | rate multipliers、RPM overrides | group、users | 平台治理能力；不下放给普通 manager | durable audit + auth invalidation | 写命令已接入；普通 manager 下放未实现 |
| API Key 反向读 | group API Keys | group、users/keys | 平台治理；Owner 不得看到消费者 Key 原文 | 字段投影 | Actor 已接线；字段 Policy 待设计 |
| 订阅反向读 | group subscriptions | group、users/subscriptions | 平台治理或严格聚合投影 | scoped aggregate | Actor 已接线；Scope/投影待设计 |

## 关联管理员直接入口

| 入口族 | 直接资源引用 | 当前状态 | 后续责任 |
| --- | --- | --- | --- |
| User / Admin API Key | user、group、API Key | API Key Group 变更与 ReplaceUserGroup 已接入 1.10；其余 Actor 已接线 | 其余用户/Key Policy 与事务内重授权 |
| Channel CRUD | channel | Actor 已接线 | 平台能力 Policy |
| Payment Plan / Redeem | plan、redeem code、user、subscription | Actor 已接线 | 履约事务、retry/refund 与审计 |
| Settings 默认订阅 | group、subscription plan | Actor 已接线 | 平台 Policy 与原子配置写 |
| Content Moderation | groups、proxy、API Key 测试 | Actor 已接线 | 引用 Policy 与出站审计 |
| Channel Monitor / Template | monitor、template、account、`monitor_ids` | Actor 已接线（Run/History 延期） | Policy、worker System Actor 与历史投影 |
| Ops alert rule / silence | rule、group、account、platform | Actor 已接线（events/evaluator 延期） | Policy、派生 scope 与 System Actor |

## 普通用户与数据面

| 链路 | 当前入口 | 目标约束 | 状态 |
| --- | --- | --- | --- |
| 可用分组 | `GET /groups/available` | legacy/shadow/acl 单一权威源 + 业务资格 | 待接入 |
| API Key CRUD | user API Key handlers | Key Owner；绑定时和运行时检查 `group.use` | 待接入 |
| 网关 HTTP/SSE | Chat/Responses/Claude/Gemini/Images/媒体 | Auth snapshot 当前版本；fallback/routing 最终资源 ACL | `group.use`/ACL consumer 待接入；1.11 已完成 v22 allow snapshot、首次写入 30 秒 monotonic deadline、rewrite 不续期及正向 L2 不提升 L1 |
| Responses WS | 每个 `response.create` | 每 turn 重新检查版本，撤权后不复用帐号 | 待接入 |
| 异步任务 | batch image/media jobs | 入队与执行前检查，结果按 Owner 投影 | 待接入 |
| 派生读取 | usage/error/dashboard/search/export/monitor | user/group/account scope 一致，total 不泄漏 | 待接入 |

## 后台写路径

以下路径没有 HTTP Actor 时必须使用受限 System Actor，并记录原因；不能伪装首个管理员：

- OAuth/token refresh、quota refresh、rate-limit/error/autopause 状态更新。
- Scheduler snapshot rebuild、group lifecycle、关系重算与到期协调。
- upstream billing probe、channel monitor、scheduled tests、CRS/background sync。
- proxy expiry fallback、凭证 CAS 更新、Spark shadow 同步和模型观测。

后台运行态更新通常只需要受限 system capability，不得自动获得 `manage_access/delete/transfer/secret.export`。每条实现需在接线 PR 中补充具体 Actor、事务和测试责任文件。

| 后台链路 | Durable Actor / 权限边界 | 事务与传播责任 | 当前状态 |
| --- | --- | --- | --- |
| 授权到期协调 | 固定 `authorization_expiry_coordinator` Service Principal；必须 active 且零角色，仅用于审计归因 | 四类 role/Grant 来源按数据库时间收敛版本与 durable audit/event；Grant 到期 enqueue Scheduler | 1.11 已接入；coordinator 不就绪 fail closed |
| Auth Cache Invalidation | 无业务资源授权；只消费 hashed cache key，不持久化 API Key 明文 | stage 0 primary + stage 1 safety、claim/retry/release 与多实例恢复；相对 delay 由数据库时间落到 `available_at` | 1.11 已接入；传播门观测两个 stage |
| Scheduler Outbox | 不冒充管理员；只消费事务内既有 Scheduler event | PostgreSQL lease/token fencing/ack/retry；commit-order、lease recovery 与 durable delivery strict lock-busy retry | 1.11 已接入 Worker 恢复语义；完整关系重算待 4.2/4.4 |
| 传播扩大权限门 | 无独立 Actor；基于数据库统计、Worker 状态和 coordinator readiness 作 fail-closed 判定 | 5 秒目标、30 秒安全线；只阻止扩权，不阻止关闭功能或撤权 | Settings 已接入；健康入口在 Ops disabled 时仍可读；ResourceMutation 提供 `ExpandsAccess` 契约，当前无 Grant 管理生产命令 |

1.11 没有宣称上述其他后台路径已全部迁移。核心 HTTP 管理命令的事务协调和三个传播 Worker 不能替代 token refresh、quota/rate-limit、probe、CRS、monitor 等路径逐项定义受限 Actor、Policy、版本和 Outbox 责任。

Account/Group Grant 到期在 1.11 只产生 Scheduler 事件并收敛资源版本。完整 `account_groups` 链接人、授权来源、Owner 批准、状态和验证版本仍属于任务 4.2；撤权、到期和角色变化后的关系闭包重算仍属于任务 4.4，不能把事件入队记录成关系重算已完成。

## 覆盖门禁

- 新增帐号/分组路由必须更新本表并有结构测试检查 Actor/Policy 接线。
- 批量入口必须先解析全部 ID，在事务内重授权，禁止成功一部分后发现越权。
- 任意 `*_id`、JSON routing ID、fallback ID、proxy ID 和 callback state 都视为资源引用。
- Repository list/search/count/aggregate 必须接受 scope/predicate，不允许 Handler 内存过滤。
