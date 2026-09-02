## ADDED Requirements

### Requirement: 自助托管必须同时受资格、功能开关和 Backend Mode 控制
系统 SHALL 仅允许拥有对应创建能力、状态正常且未超配额的用户进入自助托管。`self_service_hosting_enabled` MUST 显式开启；Backend Mode MUST 具有更高优先级并拒绝所有非管理员自助控制面请求。

#### Scenario: 普通注册用户没有 hoster 资格
- **WHEN** 用户只有基础 user 角色
- **THEN** 用户 MUST 可以继续管理自己的 API Key
- **THEN** 用户 MUST NOT 创建上游帐号或分组

#### Scenario: Backend Mode 开启
- **WHEN** self-service 开关为 true 但 Backend Mode 为 true
- **THEN** 非管理员自助资源 API MUST 被后端拒绝
- **THEN** 关闭 Backend Mode 后系统 MUST NOT 自动授予 hoster 资格

### Requirement: 自助帐号与分组必须使用受限产品表面
系统 SHALL 为普通用户提供自己的帐号和分组 CRUD，但 MUST 只开放经安全评审的字段和动作。订阅、平台价格、利润、帐号成本倍率及其他平台治理策略 MUST 仅由具备平台能力的 Actor 修改。

#### Scenario: hoster 创建分组
- **WHEN** hoster 提交分组创建请求
- **THEN** 系统 MUST 强制私有 Owner、允许的平台和安全默认值
- **THEN** 用户输入的管理员专属定价或订阅字段 MUST 被拒绝而不是静默采用

### Requirement: 租户帐号必须绑定真实私有分组
系统 SHALL 为 hoster 按需创建具有真实全局 ID 的私有默认分组。自助帐号创建与至少一个有效分组绑定 MUST 位于同一事务；无法绑定时帐号 MUST 不可调度。

#### Scenario: 创建帐号未选择分组
- **WHEN** hoster 创建帐号且没有显式选择分组
- **THEN** 系统 MUST 绑定该 Owner 的私有默认分组
- **THEN** 帐号 MUST NOT 进入全局未分组桶

#### Scenario: 删除最后一个绑定关系
- **WHEN** 租户帐号将失去最后一个有效分组
- **THEN** 系统 MUST 先绑定回 Owner 默认分组或把帐号设为不可调度
- **THEN** Scheduler MUST NOT 把它作为 group 0 候选

### Requirement: group 0 与平台默认组必须隔离租户帐号
所有 group 0、未分组、平台默认组和 SIMPLE Mode 查询 SHALL 强制 `accounts.owner_user_id IS NULL`。名称放宽后，平台默认组查找 SHALL 同时要求 `groups.owner_user_id IS NULL`。

#### Scenario: SIMPLE Mode 开启
- **WHEN** 数据库同时存在平台帐号和租户帐号
- **THEN** SIMPLE Mode 的 group 0 候选 MUST 只包含平台帐号
- **THEN** 租户帐号 MUST 只通过显式私有或共享分组调度

### Requirement: 所有帐号创建入口必须绑定可信 Owner
基础创建、OAuth、导入、复制、批量和 callback SHALL 使用服务端保存的 Actor/flow state 绑定 Owner。请求参数、OAuth callback 或导入文件 MUST NOT 替换 Owner。

#### Scenario: OAuth callback 被篡改
- **WHEN** callback 携带与发起用户不同的 owner 参数
- **THEN** 系统 MUST 忽略或拒绝该参数
- **THEN** 创建帐号的 Owner MUST 来自服务端验证的原始 flow state

### Requirement: 自助帐号出站必须执行平台与网络安全策略
系统 SHALL 仅允许 V1 allowlist 中的平台、认证类型和官方端点，并对重定向、DNS/IP、代理、Base URL、自定义 Header 和异步任务实施经评审的出站限制与限频。

#### Scenario: 用户提交不允许的自定义端点
- **WHEN** hoster 尝试设置私网、元数据地址或不在 allowlist 的 Base URL
- **THEN** 系统 MUST 在保存和实际请求前拒绝
- **THEN** 错误和审计 MUST 不包含凭证原文
