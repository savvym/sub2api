# Phase 0 Exit Record

阶段状态：

- Phase 0 设计决策：**Decision Accepted（单维护者、无部署范围）**
- Phase 1 dark-foundation 退出：**Approved（无部署范围）**
- Phase 2 实施/发布：**Not Ready（尚未 Release Accepted）**

日期：2026-09-02

维护者：Savin Zhang（GitHub `savvym`）

评审基线：`674a5387e8e112553e8d5188441d3edaf427296c`，Draft PR [#1](https://github.com/savvym/sub2api/pull/1)

## 适用事实

- 当前没有 production 或 staging 环境，也没有需要升级的真实数据库、运行中的旧 Worker 或目标环境日志系统。
- 当前项目只有一名维护者。维护者同时承担产品、平台、认证、安全、迁移和风险 owner，但本记录不声称存在独立三方安全评审。
- 当前批准范围只包含默认关闭的权限基础设施。所有新增 Feature Flag 保持关闭，角色权威保持 `legacy`，不启用普通用户自助托管、资源分享、ACL 或 RBAC enforcement。
- 当前自助 `(platform, auth_type, product_version)` allowlist 为空。所有 API Key 组合暂缓，OAuth、setup token、Cookie/密码导入、自定义 upstream、云凭证和复合平台继续禁止。

## Phase 0 决策

### 0.4 凭据设计

维护者接受 `credential-inventory.md` 的分类和迁移约束，当前设计结论为：

- 未注册键默认按 Secret 处理，`header_overrides` 整体按 Secret 处理。
- 普通 DTO、日志、缓存和常规导出不得采用未知字段 fail-open。
- 普通资源权限不得导出上游凭据原文；管理员明文导出保持关闭。未来 break-glass 必须作为独立能力实现审批、时限、durable audit、水印和撤销。
- PostgreSQL、Redis、备份和导出的具体 KMS/key hierarchy 选择推迟到首次部署设计，但版本化 envelope、独立密钥责任、轮换、恢复和灾备验证仍是启用前硬门禁。
- 第 6 节迁移批次和 CAS/查询/缓存不变量作为未来实现约束；改变这些约束必须重新打开本决策。

该结论仅达到 `Decision Accepted`，不达到 `Release Accepted`。

### 0.5 出站设计

维护者接受 `outbound-security.md` 的固定目标、direct transport、SSRF、限频、DTO 和灰度要求，但当前批准的启用集合为空：

- OpenAI、Anthropic、Gemini API Key 仅保留为未来候选，当前全部暂缓。
- 所有 OAuth 组合继续禁止；未来开放必须重新批准 Actor/Owner 绑定、PKCE、redirect URI、TTL 和单次消费契约。
- 任何 future candidate 都必须先完成固定 host/path、dial-time IP 绑定、TLS SNI/证书校验、禁重定向、禁代理、响应上限、Redis fail-closed、多维限频和泄漏 canary。
- 当前不存在可灰度或可发布的普通用户自助组合。

该结论仅达到 `Decision Accepted`，不达到 `Release Accepted`。

## 单维护者风险接受

维护者确认当前范围没有阻止默认关闭 dark foundation 合入或继续维护的设计级安全阻塞，并接受缺少独立平台、认证和安全评审的治理风险。该接受仅适用于没有部署环境、没有真实数据、没有启用自助组合且所有新授权 consumer 保持关闭的状态。

下列任一事件会自动重新打开 0.4、0.5、0.8 和对应发布门禁：

- 创建首个 production 或 staging 环境；
- 导入任何现有数据库、帐号凭据或历史幂等记录；
- 启用任一自助平台、OAuth、凭据导出、`shadow`/`rbac`、ACL 或分享开关；
- 允许客户端控制 host、path、proxy、Header 或未知凭据字段；
- 改变凭据分类、加密迁移不变量、限频上限或出站安全契约。

## 延后但未豁免的激活门禁

| 门禁 | Owner | 触发时点 | 验收方法 |
| --- | --- | --- | --- |
| `data-preflight.sql` | sole maintainer / future operator | 首次导入或升级现有数据库前 | 在批准的 read-only 连接执行；provenance 全部 `resolved`，terminal inventory 零行 |
| `credential-key-preflight.sql` | sole maintainer / future operator | 首次处理真实帐号数据前 | 只归档键名、shape 和聚合计数；异常键完成分类或保持阻断 |
| migration 243 maintenance/drain | sole maintainer / future operator | 仅升级运行过旧 auto-reset Worker 的环境时 | auto-reset 关闭、旧 Worker 停止排空、同版本切换、unresolved=0、回滚证据 |
| role-mode shadow observation | sole maintainer | 任一环境首次从 `legacy` 进入 `shadow` 前后 | readiness、差异量、日志量、sink `dropped_count`、观察窗口和回滚结果 |
| credentials `Release Accepted` | sole maintainer，生产场景建议增加独立安全 reviewer | 首次启用自助或凭据导出前 | schema registry、加密、DTO/cache/export allowlist、canary、恢复和灾备演练 |
| outbound `Release Accepted` | sole maintainer，生产场景建议增加独立安全 reviewer | 首次启用任一自助组合前 | 固定产品矩阵、SSRF/重放/OAuth/限频测试、目标环境灰度和全关回滚 |

新建空数据库并从当前版本 fresh setup 不存在旧 Worker 或历史记录时，migration 243 的旧 fleet drain 和历史 inventory 为不适用；fresh/upgrade/reapply 自动化仍必须保持通过。任何现有数据导入都会取消该不适用结论。

## Phase 1 退出结论

Phase 1 的默认关闭权限基础设施工程门禁已由本地 PostgreSQL 验证、完整 push/PR CI、Testcontainers、lint、Security Scan 和 Draft PR 评审入口满足。对当前无部署环境的 dark-foundation 范围，任务 1.12 可以结束。

该结论不批准 production/staging 发布，不批准任何自助组合，不批准凭据明文导出，不允许切换到 `shadow`/`rbac` 或 ACL enforcement，也不自动开始 Phase 2。Draft PR 保持 Draft，直到维护者另行决定进入代码评审或合并流程。
