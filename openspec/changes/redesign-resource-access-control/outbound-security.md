# 自助托管出站安全基线

阶段状态：

- Phase 0 设计决策：**Decision Accepted（空 allowlist、单维护者、无部署范围）**。2026-09-02 唯一维护者决定所有自助组合继续暂缓、OAuth 继续关闭；适用范围和自动重开条件见 [`phase-0-exit-record.md`](phase-0-exit-record.md)。
- Phase 2 实施/发布验收：**Not Ready（尚未 Release Accepted）**。

`Decision Accepted` 只冻结自助产品矩阵、固定目标、网络契约、限频基线和延期范围，可用于勾选任务 0.5；它不证明第 6 节已经实现，也不允许开启任何自助平台。只有第 6、7 节的代码与目标环境证据完成并取得 `Release Accepted` 后，候选组合才可逐项启用。两种状态不得简写为含义不明的 `Accepted`。管理员现有高级能力不在本文件中被静默收紧。

## 1. 范围与结论

本文件只约束普通用户自助创建、导入、测试、刷新和 OAuth 绑定帐号产生的控制面与数据面出站请求。管理员帐号、代理、任意上游和调试能力保持现状，但不能被普通用户入口间接复用。

审计结论：现有代码具备部分 URL、Header、代理和 OAuth 防护，但它们是面向管理员高级能力逐步补强的组件，尚未形成租户自助入口所需的统一安全边界。V1 必须采用“服务端固定产品矩阵 + 专用 DTO + 固定官方端点”，不能直接暴露现有管理员 `credentials`、`extra`、`proxy_id` 或通用帐号创建服务。

当前 `Decision Accepted` 的启用 allowlist 为空。下表中的 API Key 项仅保留为未来候选基线，不批准当前启用；任何候选进入实现或灰度前都必须重新打开本决定并完成第 6、7、8.2 节。

## 2. V1 服务端 Allowlist 建议

以下是首批候选矩阵，不表示当前代码已经允许上线。只有第 6 节硬阻塞全部关闭、验收测试通过后，候选项才可逐项启用。

| 平台 | 认证类型 | V1 决策 | 服务端固定目标 | 约束 |
| --- | --- | --- | --- | --- |
| OpenAI | `apikey` | 未来候选（当前暂缓） | `https://api.openai.com:443` | 服务端拼接受支持的 `/v1/*` 路径；请求不得携带 `base_url` |
| Anthropic | `apikey` | 未来候选（当前暂缓） | `https://api.anthropic.com:443` | 服务端拼接受支持的 `/v1/*` 路径；只接受 API Key 本身和非敏感展示字段 |
| Gemini | `apikey` | 未来候选（当前暂缓） | `https://generativelanguage.googleapis.com:443` | 服务端拼接受支持的 `/v1beta/*` 路径；禁止用户提供 Google 项目/OAuth 扩展字段 |
| Grok | `apikey` | V1 暂缓 | 当前存在 API、区域 API、CLI proxy、fallback 和 operator policy 多套目标 | 先收敛为单一 API Key 产品及精确 host/path，再单独评审 |
| Kimi / Zhipu / DeepSeek | `apikey` | V1 暂缓 | 代码中存在 PAYG/Coding 模式、协议组合及可覆写 `base_url` | 先为每个平台冻结唯一模式、协议、host/path 和额度探测目标 |
| 任意平台 | `oauth` | V1 禁止 | 不适用 | 完成用户 Actor 绑定、回调单次消费和平台专项端点评审后再开放 |
| 任意平台 | `setup-token` / Cookie / 密码导入 | V1 禁止 | 不适用 | 高泄漏、高重放或服务条款风险，必须专项评审 |
| 任意平台 | `upstream` / 自定义 `base_url` | V1 禁止 | 不适用 | 属于任意外发能力，不进入普通用户产品面 |
| 任意平台 | `bedrock` / `service_account` | V1 禁止 | 不适用 | 涉及云凭证、签名、项目和区域配置，必须专项评审 |
| `antigravity` / `composite` | 全部 | V1 禁止 | 不适用 | 多端点、多凭证或派生资源关系超出首批边界 |

Allowlist 必须由服务端按照 `(platform, auth_type, product_version)` 选择，客户端传入的字符串不得决定 host、port、scheme、path、代理或 Header。首批三个 host 使用精确匹配，不接受通配子域、非 443 端口、URL userinfo、query、fragment、Unicode/尾点别名或 HTTP 降级。

## 3. 现有代码证据

| 领域 | 现状 | 代码事实源 | 结论 |
| --- | --- | --- | --- |
| 创建入口 | 管理员请求仅约束帐号 `type`；`platform` 只是必填，并允许任意 `credentials`、`extra`、`proxy_id`、`group_ids` | `backend/internal/handler/admin/account_handler.go` | 不能作为自助 DTO |
| 创建服务 | `AdminAccountService.Create` 和通用 `AccountService.Create` 没有统一的平台 × 认证类型 × 端点产品矩阵 | `backend/internal/service/admin_account.go`、`backend/internal/service/account_service.go` | 必须增加自助专用 command/service |
| Base URL | OpenAI、Grok、国产供应商和 Gemini 兼容链路均存在读取或校验帐号 `base_url` 的路径 | `backend/internal/service/account.go`、`grok_upstream_url.go`、`cn_provider_probe_url.go`、`gemini_messages_compat_service.go` | 自助 V1 必须拒绝该字段，而非只做格式校验 |
| URL 校验 | `ValidateHTTPSURL` 支持 scheme、host allowlist 和私网字面量检查；`ValidateResolvedIP` 会预解析并拒绝部分私网 IP | `backend/internal/util/urlvalidator/validator.go` | 部分具备，但不是连接时安全拨号器 |
| 实际拨号 | 通用 HTTP upstream 在全局 URL allowlist 开启时做请求前/重定向 DNS 检查，但随后由普通 `net.Dialer` 再次解析，未绑定已验证 IP | `backend/internal/repository/http_upstream.go` | 存在 DNS TOCTOU/rebinding 风险；全局开关关闭时该检查也关闭 |
| 可复用参考 | Channel Monitor 已实现解析全部地址、拒绝私网并直接拨号已验证 IP 的 `safeDialContext` | `backend/internal/service/channel_monitor_ssrf.go`、`channel_monitor_checker.go` | 可作为实现参考，但需抽成通用组件并覆盖代理/TLS/SNI 语义 |
| 重定向 | 通用 upstream 默认可跟随重定向；只有 URL allowlist 生效时才逐跳预检，部分调用方可显式禁用 | `backend/internal/repository/http_upstream.go` | 自助凭证请求默认应完全禁止重定向 |
| 代理 | 现有管理员能力支持 HTTP、HTTPS、SOCKS5/SOCKS5H，且 SOCKS5 会升级为远端解析 | `backend/internal/pkg/proxyurl/parse.go`、`backend/internal/pkg/proxyutil/dialer.go` | 实现较完整，但自助 V1 一律禁止 `proxy_id` 和用户代理 |
| Header override | 已有大小写、CRLF、数量/长度和敏感 Header 黑名单 | `backend/internal/service/account_header_override.go` | 管理员能力可保留；自助 V1 完全不接收两个 override 字段 |
| 响应大小 | 网关和若干 probe/OAuth 路径分别使用 `LimitReader`，但不是统一的控制面响应策略 | `backend/internal/service/gateway_upstream_response.go` 及各 provider service | 必须对 create-test/quota/refresh/OAuth 全链路统一设限 |
| OAuth | 多个平台已有 state/PKCE，Grok 还具备单次消费语义；现有路由和会话面向管理员帐号流程 | `backend/internal/pkg/{openai,antigravity,geminicli,xai}`、对应 `*_oauth_service.go` | 不能证明未来回调与发起用户/Owner 的强绑定 |
| 限频 | 已有 Panel 全局按用户、Heavy 按用户和登录按 IP/身份限频；管理员通常可豁免 | `backend/internal/server/middleware/panel_rate_limit.go`、`backend/internal/server/routes/auth.go`、`admin.go` | 缺少 user + IP + platform/account 的自助专项限频和 pending flow 配额 |

现有全局 `security.url_allowlist` 是管理员运营配置，不是租户自助产品 allowlist。即使运营配置关闭，自助固定 allowlist 仍必须强制生效且 fail closed。

## 4. 自助入口契约

自助创建请求只允许以下语义字段：显示名称、服务端枚举的平台、服务端枚举的认证产品、该产品所需的最小秘密，以及允许用户调整的非敏感标签。每个平台使用显式 DTO；禁止接收泛型 `map[string]any` 后再删除危险字段。

服务端必须拒绝而不是忽略：

- `base_url`、endpoint、host、port、path、region；
- `proxy_id`、proxy URL、Unix socket、环境代理选择；
- `header_override_enabled`、`header_overrides` 和任意自定义 Header；
- `extra` 中未列入产品 schema 的键；
- setup token、Cookie、用户名/密码、云 Access Key/Secret Key、Service Account JSON；
- 管理员专用的 group 绑定、复制、批量导入和任意上游选项。

请求通过 DTO 后，服务端重新构造 `credentials`，并在持久化前再次验证只包含该产品允许的键。帐号创建、Owner 绑定、私有默认组绑定和配额占用必须在同一事务内完成。

## 5. 出站与限频基线

### 5.1 网络

- 固定 HTTPS、精确 hostname、端口 443 和代码内路径模板；禁止重定向，禁止凭证跨 host 重放。
- 每次新连接在 dial 时解析 A/AAAA，任一候选地址命中 loopback、RFC1918、link-local、CGNAT、multicast、unspecified、文档/保留网段或云元数据范围即拒绝。
- 直接拨号已验证的 IP，同时保留原 hostname 用于 TLS SNI 和证书校验；不得“保存时解析、请求时重新解析”。连接复用键必须包含固定产品目标。
- 自助请求使用专用 direct transport，不继承 `HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY`，不接受帐号代理；OAuth、额度探测和测试请求必须走同一策略。
- 控制面响应体建议上限 1 MiB，错误体 64 KiB；超限返回稳定错误码并关闭连接。流式模型响应继续使用网关自己的独立上限。
- 日志、审计和指标只记录 provider、稳定错误码、状态码、耗时和脱敏 host；不得记录完整 URL query、Authorization、Cookie、API Key、OAuth code/token 或响应体。

### 5.2 初始限频建议

| 操作 | V1 初始限制 | 附加约束 |
| --- | --- | --- |
| 创建/导入帐号 | `10 次/小时/user`，并叠加 `30 次/小时/IP/platform` | 每用户帐号硬配额；数据库原子占位，批量按每项计数 |
| OAuth 发起 | `5 次/10 分钟/user/IP/platform` | 每用户每平台最多 3 个 pending flow，TTL 10 分钟 |
| OAuth callback | 每 session 最多 5 次尝试 | state 单次原子消费；成功或终态失败后立即失效 |
| 帐号测试/额度查询/手动刷新 | `6 次/分钟/user/account`，并叠加 `30 次/分钟/user/platform` | 每用户最多 2 个并发控制面出站；provider 全局 bulkhead |
| 连续认证失败 | 1 秒起指数退避，最大 5 分钟 | 成功只清当前帐号退避，不得清用户/IP 风险计数 |

这些值应成为服务端配置的保守默认值；允许运营者调低。调高、管理员豁免或新增批量入口必须单独审计。Redis 限频异常时，自助凭证出站不能沿用 Panel 的 fail-open 语义，应拒绝或进入严格的进程内降级上限。

## 6. Phase 2 实施/发布硬阻塞

1. **产品矩阵缺失**：实现并测试独立自助 DTO、平台/类型矩阵和凭证键 allowlist；证明管理员请求体无法穿透。
2. **安全拨号缺失**：通用出站组件完成 dial-time IP 校验/绑定、保留 SNI、禁重定向和全 CIDR 测试；不得仅调用现有 `ValidateResolvedIP` 后普通拨号。
3. **调用面未收口**：create-test、quota、refresh、后台任务和所有 provider builder 全部只能从固定产品目标派生 URL，且强制 `proxy_id == nil`、无 Header override。
4. **OAuth Owner 绑定未完成**：每个 flow 的服务端 session 记录 `actor_user_id`、目标 owner、平台、PKCE verifier、redirect URI、TTL 和单次状态；callback 不信任客户端提交的 Owner/account ID。
5. **限频/配额缺失**：实现 user + IP + platform/account 多维限频、pending flow 限额、数据库原子帐号配额和 provider bulkhead；批量与异步任务不可绕过。
6. **秘密边界未验收**：凭证加密、字段投影、审计脱敏、错误脱敏、Redis/任务 payload 和前端状态完成 canary 泄漏测试。
7. **灰度门缺失**：自助开关默认关闭；按平台独立启用，具备拒绝率、出站目标、限频和 SSRF 拦截指标，并可立即回滚到全关。

任一阻塞未关闭时，即使管理员路径在同一平台工作正常，也不能将该组合加入自助 allowlist。

## 7. 验收矩阵

- 产品矩阵：对每个允许组合做正例；对未知平台/类型、大小写变体、额外凭证键、`base_url`、代理和 Header override 做拒绝测试。
- SSRF：覆盖 IPv4/IPv6 字面量、整数/八进制/混合编码、尾点/Unicode host、CNAME、DNS rebinding、连接复用、每次 redirect、CGNAT 和各云元数据地址。
- 凭证重放：官方 host 返回 30x、跨 host Location、HTTP 降级和同 host 非允许路径时，证明认证 Header 不会被发送到第二跳。
- OAuth：覆盖跨用户 callback、过期、重复 callback、并发消费、state 不匹配、PKCE 不匹配、callback 篡改 Owner/account ID 和多实例场景。
- 限频：覆盖多实例并发、Redis 故障、批量接口、后台任务、管理员豁免和 IPv4/IPv6/IP 代理头边界。
- 响应与日志：超大/压缩响应、恶意错误体、CRLF Header、凭证 canary；canary 不得出现在日志、错误、审计、Redis、前端状态或 API 响应。
- 回归：管理员现有代理、自定义上游和 Header override 行为保持不变；所有新开关关闭时，旧用户/管理员行为与现网一致。

## 8. 分阶段批准记录

### 8.1 Phase 0 设计决策批准（任务 0.5）

以下项目已按 [`phase-0-exit-record.md`](phase-0-exit-record.md) 的空 allowlist、单维护者、无部署范围关闭，因此 Phase 0 状态可改为 `Decision Accepted` 并勾选任务 0.5：

- [x] 当前启用 allowlist 冻结为空；OpenAI/Anthropic/Gemini API Key 仅为未来候选，其余组合暂缓或禁止，客户端不能选择网络目标。
- [x] 唯一维护者接受 direct transport 安全契约作为未来候选的启用前硬门禁：dial-time 全地址校验和 IP 绑定、TLS SNI/证书校验、禁重定向、禁环境/帐号代理、响应上限和受控连接复用。
- [x] 唯一维护者接受第 5.2 节限频、pending flow、帐号配额、provider bulkhead、Redis 故障 fail-closed 和调高配置重新审批规则；当前没有启用组合。
- [x] OAuth 继续延期并禁止；任何拟开放组合必须重新批准 Actor/Owner 强绑定、PKCE、redirect URI、TTL 和单次消费契约。
- [x] 自助专用 DTO、未知键拒绝、凭证/日志边界、逐平台灰度指标和全关回滚由未来 Phase 2/首次启用门禁验收。
- [x] 第 6、7 节未决实施项统一由 sole maintainer 负责，目标阶段为 Phase 2 或首次启用前，验收方法保持本文件所列矩阵。
- [x] 已建立版本化设计批准记录，包含空 allowlist、未来候选固定目标、限频基线、OAuth 决策、未决实施清单和自动重开条件。

本项目当前没有独立平台、认证和安全负责人；版本化退出记录透明记录单维护者风险接受，不将其表述为三方独立批准。首次创建部署环境或拟启用任一组合时，本决定自动重开，并应优先引入独立安全评审。新增平台、认证类型、目标 host/path、代理能力或调高限频同样会使对应决策失效。

### 8.2 Phase 2 实施/发布验收

以下项目全部完成后，才能把实施/发布状态改为 `Release Accepted` 并启用获批组合：

- [ ] 第 6 节七项硬阻塞全部关闭，且每项附实现 PR/commit、配置和测试链接。
- [ ] 第 7 节验收矩阵在 CI 与目标环境通过；SSRF、凭证重放、OAuth、多实例限频、泄漏 canary 和旧管理员回归均有可追溯结果。
- [ ] 发布记录列出实际启用组合、精确 host/path、最终限频值、direct transport 版本、OAuth 状态、观察指标、告警阈值、灰度窗口和全关回滚演练结果。
- [ ] `credential-inventory.md` 已取得对应范围的 `Release Accepted`，且无未到期的高风险例外。
- [ ] 安全与平台负责人共同批准发布；每个实际启用组合的认证类型负责人确认实现未偏离第 8.1 节决策。任何豁免都必须有风险 owner、范围、补偿控制、到期时间和批准链接。

Phase 0 的 `Decision Accepted` 不能替代本节。任一发布证据缺失时，即使管理员路径已工作，相关自助平台仍必须保持关闭。
