## ADDED Requirements

### Requirement: 系统必须分离平台能力与资源授权
系统 SHALL 使用平台能力判断 Actor 是否可以创建某类资源或执行平台级命令，并使用资源 ACL 判断 Actor 能否对具体帐号或分组执行动作。拥有创建能力 MUST NOT 获得该类型全局列表或全局管理权限。

#### Scenario: hoster 创建帐号
- **WHEN** 正常用户拥有 `account.create`
- **THEN** 系统 MUST 允许其进入私有帐号创建流程
- **THEN** 系统 MUST NOT 因该能力返回其他用户或平台的全部帐号

#### Scenario: 创建时同时请求分享
- **WHEN** Actor 在创建请求中设置公开级别或附带 Grant
- **THEN** 系统 MUST 把操作视为私有创建加分享并额外检查 `resource.share` 与 `manage_access`
- **THEN** 任一步失败时整个事务 MUST 回滚

### Requirement: 新资源必须默认私有且授权默认拒绝
用户自助创建的帐号和分组 SHALL 绑定当前可信 Actor 为 Owner，并 MUST 以无公开级别、无 Grant 的私有状态创建。缺少 Owner、公开级别或有效 Grant 时，普通用户访问 MUST 被拒绝；`owner_user_id=NULL` MUST 只表示平台资源而非公开资源。

#### Scenario: 用户创建资源
- **WHEN** 有资格用户成功创建帐号或分组
- **THEN** owner_user_id 与 created_by_user_id MUST 等于当前 Actor user ID
- **THEN** public_access_level MUST 为 NULL 且不得隐式创建 Grant

#### Scenario: 普通用户猜测平台或私有资源 ID
- **WHEN** Actor 没有资源 view 权限
- **THEN** 单项读取 MUST 返回 404
- **THEN** 日志 MAY 记录真实拒绝原因但响应 MUST 不泄露资源是否存在

### Requirement: 资源动作与访问级别必须由后端固定映射
系统 SHALL 为帐号和分组定义稳定的 view/use/operate/edit/manage_access/delete/transfer 动作，并把 viewer/consumer/maintainer/manager 映射为固定动作集合。Owner 与管理员旁路 MAY 获得更高资源动作，但 manager MUST NOT 隐含 delete 或 transfer。

#### Scenario: viewer 访问分组
- **WHEN** Actor 的最高有效 Grant 为 viewer
- **THEN** Actor MUST 只能读取安全投影
- **THEN** Actor MUST NOT 绑定 API Key、编辑或分享该分组

#### Scenario: 多个授权来源并存
- **WHEN** Actor 同时拥有用户 Grant、角色 Grant或公开级别
- **THEN** Policy MUST 使用当前有效来源中的最高访问级别
- **THEN** 过期或已失效来源 MUST 不参与结果

### Requirement: 所有资源读取必须在 SQL 层限制范围
Repository SHALL 在数据库查询中合并 Owner、有效用户 Grant、有效角色 Grant、公开级别和平台能力范围。搜索、排序、分页、total、聚合和自动完成 MUST 使用相同范围，不得先加载全表再在 Go 中过滤。

#### Scenario: 用户分页查询私有资源
- **WHEN** 数据库存在其他 Owner 的私有资源
- **THEN** 列表结果和 total MUST 不包含这些资源
- **THEN** 搜索词、排序字段或分页边界 MUST 不改变隔离结果

### Requirement: 资源引用必须防止 IDOR 与并发授权变化
所有写入、批量、导入、复制、OAuth、测试、刷新、路由和回退引用 SHALL 使用同一 Policy 判定。安全关键写入 MUST 在事务内重新校验 Actor、Owner、角色版本和资源 access_version；授权在预检查后变化时不得产生部分写入。

#### Scenario: 批量请求混入无权 ID
- **WHEN** 一个批量命令同时包含有权和无权资源 ID
- **THEN** 系统 MUST 拒绝整个命令
- **THEN** 系统 MUST 不修改任何目标

#### Scenario: 预检查后授权被撤销
- **WHEN** 写事务提交前 Grant、Owner 或 access_version 已变化
- **THEN** 事务内重校验 MUST 拒绝旧 Actor 决策
- **THEN** 业务写、审计和 Outbox MUST 一起回滚

### Requirement: 常规资源接口不得返回凭证原文
上游 token、API Key、Cookie、私钥和 OAuth refresh token SHALL 被视为 write-only secret。Owner、maintainer、manager 和管理员的常规 DTO、日志、错误、审计、缓存和前端状态 MUST 不包含凭证原文。

#### Scenario: Owner 查看自己的帐号
- **WHEN** Owner 请求帐号详情
- **THEN** 响应 MUST 只返回凭证是否配置、类型和允许的安全状态
- **THEN** 响应 MUST NOT 返回任何凭证子键原文

#### Scenario: 管理员需要导出凭证
- **WHEN** 管理员执行受控导出
- **THEN** 系统 MUST 要求独立 `platform.secret.export`、step-up 和完整 durable audit
- **THEN** 普通 admin 资源列表接口 MUST NOT 因管理员身份回显凭证
