# Credential Inventory

状态：**Review Ready**（2026-08-20）。本清单基于仓库静态审计建立，供平台、认证类型和安全负责人复核；尚未执行生产只读键名统计，因此不代表 Accepted，也不解除 Phase 2 自助托管开放前的安全门禁。

审计范围包括帐号 `credentials` / `extra` 的 schema、DTO、导入导出、OAuth 刷新、探测、调度快照、Redis 缓存、备份、日志脱敏、直接 SQL、migration 和并发更新路径。主要事实源包括 `backend/ent/schema/account.go`、`backend/internal/repository/account_repo*.go`、`backend/internal/repository/gemini_token_cache.go`、`backend/internal/repository/scheduler_cache.go`、`backend/internal/repository/backup_pg_dumper.go`、`backend/internal/service/account_data.go`、各平台认证 service、帐号 DTO、日志脱敏实现和 `backend/migrations/*.sql`。

## 1. 分类与默认规则

| 等级 | 含义 | API / 日志 / 导出默认规则 |
| --- | --- | --- |
| Secret | 可直接认证、签名、恢复会话或接管上游身份 | write-only；永不进入普通 DTO、日志或常规导出 |
| Sensitive | 可关联身份、扩大攻击面或辅助恢复认证 | 默认不返回；仅按 Actor、动作和最小字段投影 |
| Operational | 调度、刷新、quota 或并发控制所需，但不能单独认证 | 只投影归一化状态；不得顺带返回原始容器 |
| Public config | 协议、模型映射等非秘密配置 | 通过出站策略和字段校验后才可返回 |

必须采用 fail-closed 规则：未知键默认按 Secret 处理，只有注册到平台/认证类型 schema 且明确分类的键才能进入响应、日志、缓存或导出。不能继续依赖“已知敏感键黑名单”。同一键在不同平台含义不同时，按更高敏感等级处理。

## 2. 当前存储与暴露面

以下是当前实现事实，不是目标态：

| 位置/路径 | 当前事实 | 风险或迁移要求 |
| --- | --- | --- |
| PostgreSQL `accounts.credentials` | JSONB 明文存储 | 包含 OAuth token、API key、AWS、Service Account 和 agent signing material |
| PostgreSQL `accounts.extra` | JSONB 明文存储 | 可包含 session、fingerprint seed、上游响应快照和租户标识；不能视为安全元数据 |
| OAuth Redis `oauth:token:*` | 明文缓存 access token；供 Claude、OpenAI、Gemini、Antigravity、Grok、Vertex 等路径共用 | 必须限定 TTL、ACL、日志与运维读取面；迁移时单独设计缓存格式 |
| Scheduler Redis 快照 | 白名单保存 `model_mapping`、`compact_model_mapping`、`api_key`、`project_id`、`oauth_type`、`plan_type` 等，并保存部分 `extra` | 当前明确存在明文 `api_key`，不能声明“Redis 不保存明文 credentials” |
| 普通帐号 DTO | 对 `credentials` 采用已知键黑名单脱敏 | 未知键默认返回，属于 fail-open；`header_overrides` 等容器可绕过键清单 |
| 普通帐号 DTO 的 `extra` | 仅移除少数 Ollama managed 字段，其余默认返回 | 新增敏感键会自动暴露，必须改为安全投影/allowlist |
| 管理员帐号导出 | 导出完整 `Credentials`、`Extra` 和代理密码 | 明文秘密外流面；需要 break-glass、强审计和加密导出策略 |
| PostgreSQL 备份 | `pg_dump` 全库后 gzip/分卷上传 | 应用层未加密备份内容；由备份介质/KMS、访问控制和保留策略补齐 |
| Audit body | 递归、归一化键名脱敏，部分纯凭据路由省略 body | 保护较强，但仍需 canary 验证未知容器和非标准键名 |
| 通用 system log | 默认脱敏键少于帐号 DTO | 未完整覆盖 API key、cookie、AWS key、Service Account 和 header override |
| Ops 错误日志 | JSON body 脱敏较完整；非 JSON body 主要依赖截断 | 非 JSON 上游错误仍可能携带 token、cookie 或签名材料 |

当前少数例外是 Ollama usage session：其持久化使用 AES-256-GCM，并复用显式配置的 `TOTP_ENCRYPTION_KEY`；未配置固定 key 时拒绝保存。这不能推导为整个 `credentials` 或 `extra` 已加密，也不代表该 key 复用方式是后续通用 envelope 的最终方案。

## 3. `credentials` 键族清单

| 键/键族 | 等级 | 典型平台/类型 | 已确认运行依赖 | V1 目标处理 |
| --- | --- | --- | --- | --- |
| `access_token`, `refresh_token`, `mobile_refresh_token`, `id_token` | Secret | OAuth/PAT | 请求认证、刷新、quota | 加密存储；write-only；禁止常规导出 |
| `api_key` | Secret | API key、Bedrock、Ollama/upstream | 网关、探测、模型同步、Scheduler | 加密存储；调度侧最小生命周期解密 |
| `session_key`, `session_token`, `cookie`, `cookies`, `password`, `clearTextPassword`, `sso_token`, `sso`, `sso-rw` | Secret | setup/cookie/SSO | 上游认证与导入 | 加密存储；禁止响应与日志 |
| `agent_private_key` | Secret | OpenAI agent identity | 每请求动态签名 | 整体密文；不可导出 |
| `aws_secret_access_key`, `aws_session_token` | Secret | Bedrock SigV4 | 请求签名 | 加密存储；只在签名边界解密 |
| `service_account_json`, `service_account`, `private_key` | Secret | Gemini/Vertex Service Account | JWT 签名、token exchange | 整体密文；嵌套内容不得单独序列化 |
| `aws_access_key_id` | Sensitive | Bedrock | 凭据标识、签名 | 默认不返回；与 secret 同生命周期管理 |
| `client_id`, `email`, `sub`, `chatgpt_account_id`, `chatgpt_user_id`, `organization_id`, `team_id`, `client_email` | Sensitive | OAuth/Service Account | header、去重、quota、展示 | Owner/maintainer 最小投影；consumer 不见 |
| `project_id`, `antigravity_project_id`, `auto_detect_project_id`, `tier_id`, `location`, `vertex_location`, `vertex_model_locations` | Sensitive/Operational | Gemini/Vertex | 路由、quota、区域选择 | 平台 schema 显式分类后投影 |
| `_token_version`, `expires_at`, `expires_in`, `token_type`, `scope` | Operational/Sensitive | OAuth | 刷新、CAS、状态 | 返回归一化状态，不返回原始凭据对象 |
| `auth_mode`, `openai_auth_mode`, `oauth_type`, `account_mode`, `api_protocol` | Operational/Public config | 多平台 | 协议与认证分支 | 枚举校验后投影 |
| `agent_runtime_id`, `task_id`, `chatgpt_account_is_fedramp` | Sensitive/Operational | OpenAI agent identity | 签名上下文、任务路由 | 仅服务端使用或最小投影 |
| `base_url` | Public config/Sensitive | upstream/API key/Grok | 出站目标、探测 | 仅自助 allowlist 类型开放；清除 userinfo/query |
| `model_mapping`, `compact_model_mapping`, `openai_capabilities` | Operational/Public config | 多平台 | 路由、模型列表、migration | 按帐号 view 投影；保持 JSON 子路径可更新 |
| `pool_mode*`, `custom_error_codes*`, `temp_unschedulable*`, `intercept_warmup_requests` | Operational | 多平台 | 调度与失败策略 | 服务端校验；consumer 不见帐号级细节 |
| `plan_type`, `subscription_tier`, `subscription_expires_at`, `entitlement_status` | Sensitive/Operational | 多平台 | quota、调度、展示 | 返回归一化套餐状态，不返回完整 credentials |
| `header_override_enabled`, `header_overrides` | Secret/Sensitive 混合 | upstream/Grok | 上游 header 注入 | 自助 V1 禁用；管理员读取也必须递归脱敏 |

`header_overrides` 可承载 `x-relay-key` 或任意自定义秘密，即使内部键名不在敏感清单中，也必须把整个容器按 Secret 处理。

### 3.1 平台/认证类型覆盖

| 平台/类型 | 主要 Secret | 主要 Sensitive/Operational | 特殊依赖 |
| --- | --- | --- | --- |
| OpenAI/Claude OAuth | access/refresh/id token | account/user/org ID、scope、token version、expiry | 刷新候选查询与 CAS |
| Gemini/Antigravity/Grok OAuth | access/refresh token | project/tier/account ID、expiry | 共用 OAuth Redis；quota/probe 快照 |
| Vertex Service Account | service account JSON/private key | project、client email、location | JWT 签名与区域路由 |
| AWS Bedrock | secret/session key 或 API key | access key ID、region、auth mode | SigV4 签名 |
| API key/upstream/Ollama | API key、可选 header override/session | base URL、protocol、model mapping | SSRF 策略、探测、Scheduler Redis |
| OpenAI agent identity | agent private key、OAuth token | runtime/task/account ID | 动态签名；私钥不可导出 |
| Spark shadow | 不持有自身上游凭据 | 自身仅允许 model/compact model mapping | 其余凭据透传母帐号，权限与加密读取必须沿引用解析 |

该矩阵覆盖仓库中已发现的键族，不替代各平台负责人对“必需/可选/废弃”字段的确认。

## 4. `extra` 键族清单

`extra` 是开放 JSONB 容器，当前没有统一 schema。任何新增键在注册分类前必须按 Secret 处理。

| 键/键族 | 等级 | 已确认用途 | V1 目标处理 |
| --- | --- | --- | --- |
| `ollama_cloud_usage_session` | Secret | Ollama usage session | 保持加密；不得进入 DTO/调度快照 |
| `codex_fingerprint_seed` | Secret/Sensitive | 稳定客户端指纹 | 加密或不可逆派生；禁止响应 |
| `session_token_present`, `access_token_sha256` | Sensitive | Codex 导入/凭据存在性与关联 | 仅服务端；必要时返回布尔状态 |
| `import_source`, `imported_at`, `auth_provider`, `session_expires_at` | Operational/Sensitive | Codex 导入状态 | 最小状态投影 |
| 用户图像、驻留、组织/租户信息 | Sensitive | Codex/OAuth 身份元数据 | 默认不返回 |
| `ollama_cloud_usage_auto_refresh`, `ollama_cloud_usage_snapshot` | Sensitive/Operational | usage/quota | 只返回归一化聚合 |
| `upstream_billing_probe*` | Sensitive/Operational | 上游账单探测、同步 | 原始响应仅服务端；状态可聚合 |
| `grok_billing_snapshot`, `grok_usage_snapshot`, `grok_observed_models`, `grok_needs_reauth*` | Sensitive/Operational | 账单、usage、模型与重认证 | 不返回原始快照；按用途投影 |
| Grok media/scheduler 状态 | Operational | 媒体与调度能力 | 仅必要字段进入调度快照 |
| `crs_account_id`, `crs_kind`, `crs_synced_at`, `crs_email`, `org_uuid`, `account_uuid` | Sensitive/Operational | CRS 同步与身份映射 | consumer 不见；管理员最小投影 |
| quota/window/passive usage、WebSocket、RPM、协议状态 | Operational | 调度与容量 | 允许白名单快照；禁止返回整个 `extra` |
| TLS/client fingerprint、自定义 base URL | Sensitive | 上游兼容、出站连接 | 仅服务端；受出站策略约束 |

CRS 同步会复制远端 `Extra` 后补充本地标识；`sanitizeCredentialsMap` 当前只过滤 `nil`，不是安全脱敏器，不能用于响应、日志或导出边界。

## 5. 读写、查询与并发依赖

### 5.1 写入语义

- 常规 `UpdateCredentials` 整份替换 JSONB，并与 Scheduler Outbox 同事务。
- `BulkUpdate` 对 `credentials` / `extra` 使用 JSONB merge，不能在加密后静默变成覆盖整个 envelope。
- OAuth refresh 更新 `_token_version`，该逻辑版本用于防止并发刷新覆盖。
- Grok 成功/失败路径使用“完整 credentials JSONB + proxy”比较的 CAS。
- Probe/Ollama 路径使用完整 credentials、`api_key` / `base_url` / proxy 和 `extra` 快照做身份比较或 CAS。
- Spark shadow 的凭据从母帐号解析，不能把 shadow 当作独立凭据拥有者迁移。

### 5.2 SQL 与 migration 依赖

现有代码和历史 migration 直接读取或修改 JSON 子路径：

- OAuth refresh 候选查询依赖 `credentials->>'refresh_token'`。
- Ollama/probe/repository 查询依赖 `api_key`、`base_url`、CRS ID、quota/reset 等子键。
- `002` 按 `credentials.scope` 推导帐号类型。
- `024` 使用 `tier_id`、`oauth_type`、`project_id`。
- `049`、`050`、`051`、`058`、`059`、`060`、`071`、`075`、`144` 修改 `credentials.model_mapping`。
- `045` 为 `extra` 建立 GIN 索引；`105` 迁移 `extra.web_search_emulation`。
- `175` 的触发器维护并传播 `extra.openai_long_context_billing_enabled`。
- `225` 回填 `extra.codex_fingerprint_seed`。

因此，不能直接把整个 JSONB 替换为不透明随机 nonce envelope 后上线。否则会同时破坏候选查询、索引、子路径 migration、JSONB merge、完整对象 equality CAS 和运维查询。

## 6. 加密迁移约束与批次

加密方案必须先解决以下不变量：

1. 逻辑凭据版本与密文版本分离。重加密产生的新 nonce 不能被判断为凭据轮换，也不能触发无意义调度失效。
2. CAS 比较逻辑值、稳定摘要或显式业务版本，不能比较随机化后的完整 ciphertext。
3. 可查询字段迁到独立类型化列、受控索引投影或稳定盲索引；不能为了查询保留秘密明文副本。
4. Scheduler 与 OAuth Redis 分别定义最小字段、TTL、ACL、轮换和失效语义。
5. 双读/迁移任务与 OAuth refresh、probe、bulk merge 和 Scheduler Outbox 并发安全。
6. 旧 migration 与回滚工具先改为通过版本化访问层读取，禁止继续直接操作已加密子路径。

建议批次：

1. 建立平台 schema registry、fail-closed DTO 和固定 canary 泄漏测试。
2. 把需要查询、索引或 CAS 的非秘密投影迁到类型化字段，并消除直接 JSON 子路径依赖。
3. 引入版本化 envelope（key ID、algorithm、nonce、ciphertext）和独立 KMS/keyring；支持旧明文与新密文双读。
4. 新写入切到密文，后台按帐号 CAS 分批回填；每批校验数量、解密和上游 canary。
5. 改造 OAuth/Scheduler 缓存，限制明文驻留时间和运维读取面；完成跨实例失效验证。
6. 停止明文读并清理旧格式；密钥轮换只更新 envelope key version，不改变资源 `access_version`。

凭据创建、替换、读取、导出和 break-glass 解密必须写 durable audit，但 audit payload 只能记录资源、动作、Actor、字段类别、版本和结果，不能记录秘密值。

## 7. 已有保护与明确缺口

已有保护：

- 普通帐号 DTO 已对一批已知 Secret 做黑名单删除。
- Audit body 具备递归和键名归一化脱敏，部分凭据路由直接省略 body。
- Ollama usage session 已有 AES-GCM 持久化保护。
- Scheduler 快照对 `credentials` / `extra` 使用字段白名单，而非复制整个对象。

明确缺口：

- PostgreSQL、OAuth Redis、Scheduler Redis、管理员导出和全库备份仍存在明文秘密面。
- DTO 对 `credentials` 与 `extra` 的默认行为是 fail-open。
- 通用日志、非 JSON 上游错误和任意非敏感键名中的秘密可能绕过脱敏。
- `header_overrides`、CRS 远端 `Extra` 和开放 JSONB 允许未来新增未分类秘密。
- 目前没有统一 platform/account-type schema，也没有覆盖所有响应、日志、缓存、导出和备份的 canary 泄漏测试。
- 通用 envelope 的 KMS、key hierarchy、轮换、恢复、break-glass 和灾备解密流程尚未决策。

## 8. Review Ready 待确认项

以下项目完成后，负责人才能把本清单从 Review Ready 标为 Accepted：

- [ ] 在生产只读副本运行 [`credential-key-preflight.sql`](credential-key-preflight.sql)，聚合 `jsonb_object_keys(credentials)` 与 `jsonb_object_keys(extra)`；只输出键名、平台/类型、软删除状态、帐号 status、JSON shape 和计数，绝不输出值或帐号 ID，并将结果作为受限安全评审材料归档。
- [ ] 各平台/认证类型负责人确认必需、可选、废弃键及刷新、CAS、probe 和缓存依赖。
- [ ] 安全负责人确认未知键 fail-closed、DTO allowlist 和 `header_overrides` 整体 Secret 策略。
- [ ] 平台负责人确认 PostgreSQL、Redis、导出和备份的加密边界、KMS/key hierarchy、轮换与灾备方案。
- [ ] 产品/安全负责人确认管理员明文导出的 break-glass、审批、审计、有效期和水印要求。
- [ ] 为 API response、audit log、ops log、system log、Redis、export 和 backup 建立固定 canary 泄漏测试。
- [ ] 证明加密迁移不破坏 JSONB 查询、历史 migration、bulk merge、OAuth refresh CAS、Grok/probe CAS 和 Scheduler Outbox。

Phase 2 自助托管开放前，以上未决项及 `outbound-security.md` 的平台/认证类型 allowlist 必须完成评审。
