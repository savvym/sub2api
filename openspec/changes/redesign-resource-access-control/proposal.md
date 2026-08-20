## Why

sub2api 当前只有 `admin` 与 `user` 两种平台角色。上游帐号和分组没有 Owner，管理入口依赖管理员整体放行。随着托管帐号、分组和 API 数量增长，为朋友开放管理员权限会同时暴露全站资源和敏感凭证，既难管理也无法形成可对外提供的托管能力。

需要在保留现有管理员、API Key、调度和计费行为的基础上，引入平台能力 RBAC 与资源 ACL：有资格的用户可以管理自己默认私有的帐号和分组，并按访问级别分享给指定用户、指定角色或全站已登录用户。

## What Changes

- 新增平台能力、角色、用户多角色和 Service Principal 模型，保留旧 `users.role` 作为渐进迁移兼容字段。
- 为帐号和分组增加 Owner、创建者、公开级别、授权版本；分组增加 `legacy/shadow/acl` 权威源状态。
- 为帐号和分组分别增加具有真实外键的 Grant，支持用户、角色、有效期和固定访问级别。
- 引入可信 Actor、统一 PolicyService、SQL 层可访问范围过滤、字段投影和不可枚举的 IDOR 语义。
- 允许获得 hoster 资格的用户创建私有帐号、私有默认分组和自己的 API Key；Backend Mode 始终优先。
- 支持分组和帐号按 viewer/consumer/maintainer/manager 分享；分享不级联暴露底层资源或凭证。
- 为跨 Owner 帐号-分组关系增加受众闭包、Owner 批准、授权版本和 Scheduler 失效约束。
- 把授权变更、审计、Auth Cache 与 Scheduler Outbox 纳入同一事务，并定义撤权/到期传播窗口。
- 使用默认关闭的 Feature Flag 和逐资源授权模式完成 expand、backfill、shadow、cutover、contract。

## Capabilities

### New Capabilities

- `resource-authorization`：平台能力 RBAC、资源 Owner/ACL、Actor、Policy、范围查询和凭证投影。
- `self-service-resource-hosting`：有资格用户的私有帐号、分组和 API Key 托管流程。
- `resource-sharing`：帐号/分组对用户、角色或全站的分级分享、撤权和受众闭包。
- `runtime-authorization-consistency`：API Key/调度运行时授权、缓存失效、到期协调和渐进迁移。

### Modified Capabilities

无。仓库当前没有已发布的资源权限 OpenSpec capability；现有管理员与普通用户行为作为兼容基线。

## Impact

- **数据库**：新增 RBAC、Service Principal、资源 Grant 和安全审计结构；扩展 users/accounts/groups；后续扩展 account_groups。
- **后端**：新增 authz 领域模块；帐号、分组、API Key、管理员身份、Repository 查询和调度链路逐步接入 Actor/Policy。
- **缓存与异步**：扩展 Auth Cache 失效，强化 Scheduler Outbox，并新增授权到期/闭包重算协调。
- **前端**：后续新增“我的帐号”“我的分组”“分享管理”，管理员增加角色、能力、配额和治理入口。
- **兼容性**：第一阶段所有新开关默认关闭，存量资源保持 `owner_user_id=NULL`，分组保持 `authorization_mode=legacy`。
- **安全**：普通分享永不返回凭证原文；未授权读取返回 404；所有引用 ID 和派生读面必须按 Actor 范围校验。
