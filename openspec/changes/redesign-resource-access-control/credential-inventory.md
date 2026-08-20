# Credential Inventory

状态：Draft，开放 Phase 2 前必须由平台/认证类型负责人复核全部实际子键。事实源包括 `backend/ent/schema/account.go`、`backend/internal/service/*token*`、`frontend/src/components/account/credentialsBuilder.ts` 和帐号表单。

## 分类规则

| 等级 | 含义 | 常规 API |
| --- | --- | --- |
| Secret | 可直接认证、签名或接管上游身份 | 永不返回；write-only |
| Sensitive | 可用于关联身份、扩大攻击面或恢复认证 | 默认不返回；按最小投影 |
| Operational | 调度/刷新需要但不直接认证 | 按访问级别投影 |
| Public config | 官方端点、协议、模型映射等非秘密配置 | 经出站策略验证后可返回 |

## credentials 已确认键

| 键/键族 | 等级 | 典型类型 | 运行依赖 | V1 处理 |
| --- | --- | --- | --- | --- |
| `api_key` | Secret | apikey/upstream/bedrock API-key | 网关、探测、模型同步 | write-only，加密迁移 |
| `access_token` | Secret | OAuth/PAT | 网关、刷新、quota | write-only，加密迁移 |
| `refresh_token` / `mobile_refresh_token` | Secret | OAuth/setup | token refresh | write-only，加密迁移 |
| `id_token` | Secret | OAuth | 身份/刷新兼容 | write-only；评估是否需长期保存 |
| `session_key` / `session_token` / `cookie` / `cookies` | Secret | setup/cookie | 上游认证 | write-only，加密迁移 |
| `aws_secret_access_key` / `aws_session_token` | Secret | bedrock SigV4 | 请求签名 | write-only，加密迁移 |
| `service_account_json` / `service_account` / `private_key` | Secret | service_account/Vertex | JWT 签名、token exchange | 整体密文；禁止子键日志 |
| agent identity 签名材料 | Secret | OpenAI Codex agentIdentity | 每请求动态签名 | 整体密文；禁止导出 |
| `chatgpt_account_id` / provider user/account IDs | Sensitive | OpenAI/Grok OAuth | header、quota、去重 | Owner/maintainer 最小投影，consumer 不见 |
| email/username/org/project identifiers | Sensitive | OAuth/service account | 显示、路由、quota | 安全 DTO 可掩码，分享分组不见 |
| `expires_at` / `_token_version` / scopes | Operational | OAuth | 刷新与 CAS | 可返回归一化状态，不返回 token |
| `base_url` | Public config 或 Sensitive | upstream/API key/Grok | 出站目标 | 仅 allowlist 模式可下放；移除 query/userinfo |
| `auth_mode` / `account_mode` / `api_protocol` | Public config | bedrock/CN providers | 协议选择 | 可投影，服务端校验枚举 |
| model mapping/whitelist | Public config | 多平台 | 路由与模型列表 | 可按帐号 view 投影 |
| header overrides | Secret/Sensitive 混合 | upstream/Grok | 上游请求 | 禁止自助 V1；管理员也需键级脱敏 |

## extra 风险键族

`extra` 不是天然安全字段。以下内容必须按 Secret/Sensitive 处理，即使名称不含 token：

- OAuth refresh error、reauth 状态中的上游响应片段或身份信息。
- 自定义 Base URL、proxy/header 配置、CRS 账号标识和 session 数据。
- Ollama Cloud usage session、provider cookie、设备/客户端身份材料。
- `custom_base_url*`、agent runtime identity、组织/项目/租户标识。

调度状态、quota 时间窗、模型能力、价格观测等可作为 Operational，但 consumer 只能看到分组级安全聚合。

## 加密迁移批次

1. 建立 canary 扫描和键级清单，覆盖数据库、日志、Redis、导出和前端响应。
2. 引入版本化 envelope（key ID、algorithm、nonce、ciphertext），支持旧明文只读与新密文双读。
3. 新写入先仅写密文；后台按帐号批次迁移，使用 CAS 防止覆盖并发 token refresh。
4. 调度/刷新缓存只持有最短生命周期的解密对象，Redis 不保存明文 credentials。
5. 完成数量/解密/上游 canary 后停止明文读，最后用独立 migration 清理旧格式。
6. 轮换时按 key version 重加密，不改变资源 access_version；凭证替换本身必须记录 durable audit。

## 待复核

- [ ] 从生产只读样本聚合 `jsonb_object_keys(credentials/extra)`，只输出键名与计数，绝不输出值。
- [ ] 按 platform × account type 确认必需/可选键、刷新 CAS 和缓存依赖。
- [ ] 确认 API/export/backup/diagnostic 的所有序列化路径使用同一递归脱敏器。
- [ ] 确认日志错误不会包含完整上游 body、URL query、Header 或 JSON credentials。
- [ ] 为每类 secret 建立固定 canary 泄漏测试。
