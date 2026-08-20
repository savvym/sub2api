## ADDED Requirements

### Requirement: 资源必须支持用户、角色与全站分级分享
帐号和分组 SHALL 支持向一个用户、一个角色或所有已登录且状态正常的用户授予访问。用户/角色 Grant 必须且只能选择一种 grantee，Grant 必须记录访问级别、授予者、有效期和资源外键。

#### Scenario: Owner 分享给指定用户
- **WHEN** Owner 拥有 `resource.share` 并向目标用户授予 consumer
- **THEN** 系统 MUST 创建具有真实资源与用户外键的 Grant
- **THEN** 授权事件、资源 access_version 和所需 Outbox MUST 在同一事务提交

#### Scenario: 分享给角色
- **WHEN** 角色成员发生变化
- **THEN** 新成员的访问 MUST 只来自当前有效角色和 Grant
- **THEN** 受影响资源版本与缓存 MUST 被更新或失效

### Requirement: 分享必须由管理访问权与平台能力双重门禁
分享、公开、撤权 SHALL 同时要求 `resource.share` 平台能力和资源 `manage_access`。非 Owner manager 不得授予高于自身当前有效级别的权限；资源转移必须使用独立能力和 step-up。

#### Scenario: 只有创建能力的用户尝试公开
- **WHEN** Actor 拥有 group.create 但没有 resource.share
- **THEN** 创建请求中的公开级别或 Grant MUST 被拒绝
- **THEN** 系统 MUST 不留下已创建的半成品资源

### Requirement: 分享不得级联暴露关联资源或凭证
分享分组 MUST NOT 授予底层帐号 view；分享帐号 MUST NOT 分享包含它的其他分组。任何普通分享级别 MUST NOT 返回帐号凭证、代理、内部错误、消费者 API Key 或请求正文。

#### Scenario: consumer 查看共享分组
- **WHEN** consumer 拥有 group.use 但没有底层 account.view
- **THEN** 分组响应 MUST 只返回消费所需的安全元数据
- **THEN** 响应 MUST 不包含帐号列表、邮箱、凭证或内部运行错误

### Requirement: 帐号加入分组必须满足完整受众闭包
建立或保留 account_groups 关系 SHALL 要求 Actor 同时具备 `account.use` 与 `group.edit`，并且分组全部 group.use 受众是帐号 account.use 受众的子集，或存在匹配当前分组 access_version 的帐号 Owner 批准。

#### Scenario: 借入帐号加入公开分组
- **WHEN** 帐号仅分享给一个用户但目标分组向全站 consumer 开放
- **THEN** 系统 MUST 拒绝建立关系
- **THEN** 单个链接人的权限 MUST NOT 被当作完整受众覆盖

#### Scenario: 分组扩大分享范围
- **WHEN** 分组新增 consumer 或从私有改为全站可用
- **THEN** 系统 MUST 在事务内重新校验所有借入帐号
- **THEN** 任一帐号不满足闭包时扩大分享 MUST 被拒绝

### Requirement: 撤权和到期必须按当前全部来源重算
用户 Grant、角色 Grant、公开级别、Owner 或 Owner 批准被撤销/到期时，系统 SHALL 使用剩余有效来源重新计算访问和跨 Owner 关系。没有替代来源时 MUST 拒绝访问并失效关系；存在替代来源时 MUST 保持有效且更新归因/版本。

#### Scenario: Account Grant 到期且无替代来源
- **WHEN** expires_at 等于当前权威时钟
- **THEN** 新授权判定 MUST 立即拒绝
- **THEN** 相关 account_groups MUST 进入不可调度状态并触发 Scheduler 刷新

#### Scenario: 撤销一个重复来源
- **WHEN** Actor 仍通过另一个有效 Grant 拥有相同动作
- **THEN** 资源访问 MAY 保持允许
- **THEN** 审计 MUST 记录被撤销来源和最终重算结果

### Requirement: 分享目标和错误响应不得形成目录枚举
分享对象搜索 SHALL 使用精确标识、限频和受控分页。不存在、无权查看或不可分享的用户/角色/资源 MUST 使用不可区分的对外错误语义；total、耗时和自动完成不得泄露私有目录。

#### Scenario: 搜索不存在或不可见用户
- **WHEN** Actor 使用相同接口搜索两种目标
- **THEN** 响应结构与稳定错误语义 MUST 不允许区分目标是否存在
- **THEN** durable audit MAY 保存内部拒绝原因
