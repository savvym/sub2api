# Compatibility Matrix

状态：Phase 0 兼容基线已建立，Phase 2 任务 2.1 已补充普通用户创建的窄 shadow 例外。矩阵定义有效行为，组合测试必须以此为数据源或保持机械同步。

## 全局优先级

1. Backend Mode 拒绝非管理员自助控制面。
2. `resource_access_control_enabled=false` 时，所有新增自助/分享有效值均为 false。
3. Phase 1 中 SIMPLE Mode 无论数据库原始值如何，都把 self-service、group sharing 和 account sharing 的有效值固定为 false；ACL 总开关和角色基础设施只保留 dark/shadow 能力。
4. `self_service_hosting_enabled` 只开放私有托管，不隐含分享。
5. group/account sharing 分别要求总 ACL + self-service（Owner 流程）+ 对应 sharing flag。
6. role based Grant 还要求 `role_based_resource_grants_enabled`；关闭时已有角色 Grant 不得作为新允许源。
7. `role_authorization_mode` 和每组 `authorization_mode` 决定唯一权威源，不使用 OR 放行。

## 模式矩阵

| Backend | SIMPLE | ACL 总开关 | Self-service | 结果 |
| --- | --- | --- | --- | --- |
| on | 任意 | 任意 | 任意 | 非管理员控制面拒绝；存量 Key 数据面语义不因 Backend Mode 自动改变 |
| off | 任意 | off | 任意 | 完全保持 legacy 行为；不注册新普通用户资源路由 |
| off | off | on | off | 可运行 shadow/管理员基础设施；普通用户无托管入口 |
| off | off | on | on | 仅有能力/配额的 hoster 可创建私有资源 |
| off | on | on | on | Phase 1 有效 self-service/sharing 强制关闭；完成 group 0 `owner_user_id IS NULL` 隔离并重新评审后才可解除 |

SIMPLE Mode 的 Phase 1 限制是发布护栏，不修改最终产品语义。解除前必须完成任务 2.6 的 group `0`/平台默认组隔离、对应生产规模查询测试和兼容矩阵复审；仅修改数据库中的原始开关不能绕过该护栏。

## 分享开关

| ACL | Self-service | Group sharing | Account sharing | Role grants | 有效能力 |
| --- | --- | --- | --- | --- | --- |
| off | * | * | * | * | 全部关闭 |
| on | off | * | * | * | 普通用户分享关闭；可做管理员 shadow |
| on | on | off | off | * | 仅私有托管 |
| on | on | on | off | off | 用户/全站分组 Grant；角色 Grant 不放行 |
| on | on | on | off | on | 增加角色分组 Grant |
| on | on | on | on | on/off | 帐号分享按 role flag 决定是否接受角色主体 |

`group_sharing_enabled/account_sharing_enabled=true` 但前置开关 false 属于配置存在但有效值 false；管理 API 应显示依赖未满足，不能隐式打开前置开关。

## 权威源

| 模式 | 响应依据 | 新模型作用 | 回切条件 |
| --- | --- | --- | --- |
| role legacy | `users.role` | seed/backfill/观察 | 默认 |
| role shadow | 默认由 `users.role` 决定；普通 JWT 用户的 Account/Group `CanCreate` 唯一改用 RBAC 结果 | 全部入口继续记录差异；创建例外仍受总开关、self-service、主体状态、hoster 资格和事务内配额约束 | 差异不可接受可回 legacy；回切后普通用户创建重新拒绝 |
| role rbac | user_roles/permissions | 唯一允许源；旧 role 只读投影 | 无 RBAC 独有配置且版本一致 |
| group legacy | is_exclusive/user_allowed_groups/订阅旧逻辑 | ACL 不影响允许 | 默认 |
| group shadow | legacy | ACL 计算差异，不放行 | 可直接回 legacy |
| group acl | ACL + 现有业务资格 | 唯一权限源 | 仅旧模型能表达完全相同受众时 |

任务 2.1 后，role shadow 只有一个明确例外：普通 JWT User 调用 Account/Group `CanCreate` 时采用 RBAC 结果，以便后续自助创建链在 RBAC 全量切换前按 hoster 能力受控接入。该例外不是 legacy/RBAC 的 OR 放行；管理员 JWT、Service Principal、`CheckCapability`、`Authorize`、`AccessibleScope` 和其他资源类型仍使用 legacy 响应。总开关或 self-service 有效值关闭、主体失效、缺少对应创建能力或事务内配额耗尽时仍必须拒绝。

## group 0 不变量

| 场景 | 可进入 group 0 的帐号 |
| --- | --- |
| legacy 普通模式 | 现有平台未分组帐号；新增过滤后必须 owner=NULL |
| SIMPLE Mode | 只允许 owner=NULL 的平台帐号 |
| 租户帐号无显式分组 | 0 个；绑定私有默认组失败则不可调度 |
| 分享/Grant 关闭或撤销 | 不得把租户帐号降级放入 group 0 |

## 必测非法组合

- 非法/缺失 `role_authorization_mode` 回退 legacy 并告警。
- sharing=true、ACL=false 或 self-service=false 的有效值保持 false。
- role grant flag=false 时，角色 Grant 即使存在也不能产生允许。
- Backend Mode 中误注册的普通用户资源路由仍被后端拒绝。
- SIMPLE Mode + self-service 的任何查询都不返回 owner!=NULL 的 group 0 候选。
