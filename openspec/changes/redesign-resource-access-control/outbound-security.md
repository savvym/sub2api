# Self-Service Outbound Security

状态：Proposed。Phase 2 开放前必须 Accepted；管理员现有高级能力不在本文件中被静默收紧。

## V1 Allowlist

第一版自助托管只开放固定官方端点的 API Key/OAuth 类型；具体平台按实现成熟度逐个启用，不提供任意 upstream、Bedrock、Service Account、代理或 Header override。

建议首批：

| 平台 | 类型 | 端点策略 | 状态 |
| --- | --- | --- | --- |
| OpenAI | API Key | `https://api.openai.com` 固定 host/HTTPS | 待评审 |
| Anthropic | API Key | `https://api.anthropic.com` 固定 host/HTTPS | 待评审 |
| Gemini | API Key | Google 官方 Gemini API host/HTTPS | 待评审 |
| Grok | API Key | `https://api.x.ai` 固定 host/HTTPS | 待评审 |
| Kimi/Zhipu/DeepSeek | API Key | 代码内已定义官方 host 与协议组合 | 待评审 |
| 任意 upstream/custom base URL | 全部 | V1 禁止 | Proposed |
| Bedrock/Service Account/Cookie/Setup Token | 全部 | V1 禁止，单独安全评审后开放 | Proposed |

## 网络约束

- 只允许 HTTPS，禁止 URL userinfo、fragment 和凭证 query。
- 保存时规范化 scheme/host/port/path；实际请求前再次验证，防止存储后绕过。
- 官方 allowlist 使用精确 host/受控子域，不使用字符串后缀匹配。
- DNS 解析后的每个地址都拒绝 loopback、private、link-local、multicast、unspecified、保留网段和云元数据地址。
- HTTP client 在每次 redirect 和重新拨号时复验目标；禁止降级到 HTTP，限制 redirect 次数。
- 使用受控 resolver/dialer 防 DNS rebinding；连接的实际 IP 必须属于已验证集合。
- 自助帐号禁止选择平台代理、自定义代理、环境代理和 Unix socket。
- 请求 Header 使用代码内 allowlist；禁止覆盖 Host、Authorization、Cookie、Proxy-*、Forwarded/X-Forwarded-* 和 hop-by-hop headers。
- 响应 body 有硬大小上限；错误日志只记录状态、稳定 code、耗时和脱敏 host。

## 配额与限频

- 创建/导入/OAuth 发起：按 user + IP + platform 限频，并限制同时 pending flow。
- 帐号测试/刷新/quota：按 user + account 限频，共享全局 provider bulkhead。
- 每用户帐号、分组、Grant、跨 Owner link 设置硬配额；并发创建在数据库中原子准入。
- 失败认证使用指数退避；不得通过批量入口绕过单项限频。
- 后台任务携带 Owner/Actor/authorization version，执行前复查用户与资源状态。

## 验收

- SSRF 测试覆盖 IPv4/IPv6、整数/八进制/混合编码、重定向、DNS rebinding、CNAME 和元数据地址。
- Header 注入测试覆盖大小写、重复头、CRLF 和 proxy 环境变量。
- 限频测试覆盖多实例并发、批量接口和 OAuth callback 重放。
- 凭证 canary 不出现在日志、错误、审计、Redis、前端状态或响应体。
