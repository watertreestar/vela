# 生产级身份、对象锁与运行环境验收

本文档定义生产 IdP、可选 server-side session/token broker、对象锁 bucket、GitLab source package、Windows runner 和 Nomad cluster 的验收方式。原则是：配置检查不能替代 live 验收；生产验收必须访问真实后端和真实外部系统，并留下可复现命令。

## 0. 设计边界

- 后端生产身份入口统一保持为 `Authorization: Bearer <token>`，业务 API 不直接处理 IdP client secret。
- 企业 IdP token 由后端 OIDC/JWKS 校验；可选 broker 只负责把 Authorization Code + PKCE 兑换成短期平台 session token。
- 启用 `AUTH_SESSION_STORE_ENABLED=true` 后，平台 session 每次请求都会检查后端 store 中的 session 状态，logout 必须能撤销 session。
- 对象锁验收必须对真实 RustFS bucket 写入审计归档对象，并读回 retention mode、retain-until 和 legal hold 证据。
- 生产验收脚本的 `[OK]` 只能代表该命令当时访问的 IdP、后端和 bucket 通过 live check；不能替代证书、密钥轮换、账号生命周期和网络边界评审。

Runner auth E2E is a separate external live gate: its fixture validates protocol shape but is not a Nomad deployment success. Require a real dispatch with `--runner-e2e-live --require-runner-e2e-live --runner-e2e-build-id <id> --runner-e2e-nomad-job-id <job> --nomad-address <url>`. The aggregate passes the human operator/admin bearer and low-privilege Nomad read token only through child-process environment variables, never argv; it cross-checks protected Conductor evidence with the real Nomad job and allocations. The aggregate report redacts headers, JWT/capabilities, tokens, and query secrets.

聚合门禁入口：

```bash
python scripts/production_readiness_cli.py check \
  --full-conductor \
  --require-idp-live \
  --idp-live \
  --api-url https://packaging-api.example.com \
  --oidc-issuer "$OIDC_ISSUER" \
  --oidc-audience "$OIDC_AUDIENCE" \
  --oidc-jwks-url "$OIDC_JWKS_URL" \
  --oidc-discovery-url "$OIDC_ISSUER/.well-known/openid-configuration" \
  --oidc-authorization-url "$OIDC_AUTHORIZATION_URL" \
  --oidc-token-url "$OIDC_TOKEN_URL" \
  --oidc-expected-role "$OIDC_OPERATOR_ROLE" \
  --oidc-token "$OIDC_CHECK_TOKEN" \
  --require-object-lock-live \
  --object-lock-live \
  --rustfs-endpoint rustfs.example.com:9000 \
  --rustfs-access-key "$RUSTFS_ACCESS_KEY" \
  --rustfs-secret-key "$RUSTFS_SECRET_KEY" \
  --rustfs-object-lock-bucket packaging-audit-lock \
  --rustfs-retention-mode COMPLIANCE \
  --rustfs-retention-days 3650 \
  --require-gitlab-live \
  --gitlab-live \
  --gitlab-base-url "$GITLAB_PACKAGE_BASE_URL" \
  --gitlab-token "$GITLAB_PACKAGE_TOKEN" \
  --gitlab-path "42/engine/1.2.3/engine.tar.gz" \
  --gitlab-expected-sha256 "$GITLAB_PACKAGE_EXPECTED_SHA256" \
  --require-nomad-live \
  --nomad-live \
  --nomad-address "$NOMAD_ADDR" \
  --require-nomad-linux-node \
  --require-nomad-windows-node \
  --require-artifact-retention-live \
  --artifact-retention-live \
  --require-notification-routing-live \
  --notification-routing-live \
  --notification-route critical=https://alert-router.internal/packaging/critical \
  --notification-route error=https://alert-router.internal/packaging/default \
  --report-file artifacts/production-readiness.json
```

该命令会先执行本地治理检查：Prometheus 规则、Alertmanager 路由示例、生命周期回收测试和 runner fixture golden artifact 检查；再按参数调用专项 live gate。`--require-idp-live`、`--require-object-lock-live`、`--require-gitlab-live`、`--require-runner-e2e-live`、`--require-windows-live`、`--require-nomad-live`、`--require-artifact-retention-live`、`--require-notification-routing-live` 用于发布前自动化：如果没有同时传入对应的 live 参数，聚合门禁会失败，避免把跳过 live 验收误判为通过。Windows live gate 必须在真实 `raw_exec` allocation 内运行，并要求 current allocation ID 与 Nomad allocation/Job metadata（build、attempt、Runner）一致、非空 bearer/capability 文件、基于声明 task SID 的受限有效 ACL、runner.env 脱敏检查以及受保护的 operator/admin evidence 读取；Linux/fixture 不会被标为 Windows 通过。

发布流水线需要留存机器可读证据时，使用 `--json` 直接输出 JSON，或使用 `--report-file <path>` 在保留确认灯输出的同时写入 JSON 文件。报表 schema 为 `packaging.production_readiness.report/v1`，包含 `generatedAt`、`status`、`exitCode`、`summary` 和 `gates`；`token`、`secret`、`password`、`api_key` 等 key/value 形式的敏感细节会在报表中脱敏。

如果生产环境选择启用 server-side session/token broker，聚合门禁应同时传入 `--idp-live --broker-live --require-broker`。broker live 验收依赖生产 IdP、一次性 authorization code、PKCE verifier 和后端 API，所以 `--require-broker` 不能单独使用；脚本会在缺少 `--idp-live --broker-live` 时失败。

Nomad live 验收由 `scripts/nomad_check_cli.py` 完成，会访问 Nomad leader 和 node API，确认至少存在 ready 节点，并可通过 `--require-linux-node` / `--require-windows-node` 要求 `linux-packager`、`windows-packager` node class 均在线。生产模式默认要求 `NOMAD_ADDR` 使用 HTTPS；如果当前拓扑只在受控内网使用 HTTP，需要在命令中显式增加 `--nomad-allow-http`，这会在专项脚本里留下 warning。

Artifact retention live 验收由 `scripts/artifact_retention_cli.py` 完成，会访问 `/api/uploads` 检查 `sha256`、`downloadUrl`、`downloadPolicy`、`retentionStatus` 元数据契约，统计 expired 数量，并抽样验证 expired 上传的下载入口必须返回 HTTP 410。它验证平台 API 的保留策略行为，不替代 RustFS/MinIO object-lock/WORM 验收。

Notification routing live 验收由 `scripts/notification_routing_cli.py` 完成，会访问 `/api/notifications` 检查 severity 契约，并验证 `critical`、`error` 等高风险 severity 已配置外部 webhook route。默认是 dry-run，只计算会发送多少条通知；只有显式传入 `--notification-send` 时才会向外部 webhook POST `packaging.notification.dispatch/v1` payload。

## 1. 生产 IdP 验收

后端生产身份入口仍是 `Authorization: Bearer <token>`。token 可以是企业 IdP 签发的 RS256 OIDC JWT，也可以是可选 broker 签发的平台 session token。

生产后端至少配置：

```bash
AUTH_ENABLED=true
AUTH_ADMIN_TOKEN=<break-glass-admin-token>
AUTH_OPERATOR_TOKEN=<optional-static-operator-token>
AUTH_VIEWER_TOKEN=<optional-static-viewer-token>
AUTH_RUNNER_TOKEN=<runner-token>
OIDC_ISSUER=https://idp.example.com/realms/packaging
OIDC_AUDIENCE=vela
OIDC_JWKS_URL=https://idp.example.com/realms/packaging/protocol/openid-connect/certs
OIDC_ROLE_CLAIM=roles
OIDC_SUBJECT_CLAIM=sub
OIDC_ADMIN_ROLE=packaging-admin
OIDC_OPERATOR_ROLE=packaging-operator
OIDC_VIEWER_ROLE=packaging-viewer
```

验收命令：

```bash
python scripts/oidc_check_cli.py check \
  --production \
  --issuer "$OIDC_ISSUER" \
  --audience "$OIDC_AUDIENCE" \
  --jwks-url "$OIDC_JWKS_URL" \
  --discovery-url "$OIDC_ISSUER/.well-known/openid-configuration" \
  --authorization-url "$OIDC_AUTHORIZATION_URL" \
  --token-url "$OIDC_TOKEN_URL" \
  --role-claim "$OIDC_ROLE_CLAIM" \
  --subject-claim "$OIDC_SUBJECT_CLAIM" \
  --expected-role "$OIDC_OPERATOR_ROLE" \
  --token "$OIDC_CHECK_TOKEN" \
  --api-url https://packaging-api.example.com
```

通过标准：

- issuer、audience、JWKS URL 均已配置。
- production 模式必须显式提供 discovery、authorization endpoint、token endpoint、expected role、后端 API URL 和真实 JWT。
- production 模式下 issuer、JWKS、authorization、token、API URL 均使用 HTTPS。
- discovery 文档中的 issuer、JWKS、authorization endpoint、token endpoint 与配置一致。
- JWKS 至少有一个可用的 RSA RS256 signing key。
- 检查 token 包含正确 issuer、audience、subject、role、`exp`，且 token 未过期；如包含 `nbf`，则必须已经生效。
- 后端受保护接口接受该 token，返回非 401/403。

失败含义：

- 脚本 claim 检查失败，说明 IdP 配置或角色映射不满足平台规则。
- 后端 live acceptance 返回 401/403，说明后端 OIDC/JWKS 校验、audience/issuer、角色映射或 token 传递存在问题。
- 脚本不输出 token 内容，也不保存 token；token 应通过安全渠道临时注入 `OIDC_CHECK_TOKEN`。

## 2. 可选 Broker 验收

broker 用于让浏览器通过 Authorization Code + PKCE 拿平台短期 session token，而不是直接保存 IdP token。启用条件：

```bash
OIDC_TOKEN_URL=https://idp.example.com/realms/packaging/protocol/openid-connect/token
OIDC_CLIENT_ID=packaging-admin
OIDC_CLIENT_SECRET=<confidential-client-secret-if-required>
AUTH_SESSION_HMAC_KEY=<32+ bytes random secret>
AUTH_SESSION_TTL_SECONDS=3600
AUTH_SESSION_STORE_ENABLED=true
```

broker live 验收需要一个刚从 IdP 登录回调拿到的一次性 `code`，以及前端生成并保存的 PKCE `code_verifier`。这两个值过期很快，只用于本次验收。

验收命令：

```bash
python scripts/oidc_check_cli.py check \
  --issuer "$OIDC_ISSUER" \
  --audience "$OIDC_AUDIENCE" \
  --jwks-url "$OIDC_JWKS_URL" \
  --token "$OIDC_CHECK_TOKEN" \
  --api-url https://packaging-api.example.com \
  --broker-live \
  --broker-code "$OIDC_BROKER_CODE" \
  --broker-code-verifier "$OIDC_BROKER_CODE_VERIFIER" \
  --broker-redirect-uri "$OIDC_REDIRECT_URI" \
  --broker-logout \
  --require-session-store
```

在 `production_readiness_cli.py` 中启用 broker 门禁时，对应参数是 `--idp-live --broker-live --require-broker`，并传入同样的 `--broker-code`、`--broker-code-verifier`、`--broker-redirect-uri` 和 `--broker-logout --require-session-store`。

通过标准：

- `POST /api/auth/oidc/token` 成功用 code + verifier 换取平台 session token。
- 返回 payload 包含 `token`、`role`、`subject`；启用 session store 时包含 `sessionId`。
- 用平台 session token 调用后端受保护接口成功。
- `--broker-logout --require-session-store` 下，`POST /api/auth/logout` 返回 `revoked=true`。
- logout 后再次使用同一个平台 session token 调用受保护接口，必须返回 401/403，证明服务端 session store 撤销生效。

约束：

- broker 是可选能力；不使用 broker 时，生产 IdP token 仍可直接作为 bearer token 使用。
- 当前平台 session token 是 bearer token，不是 HttpOnly Cookie。若后续需要长期会话，应在现有 session store 上增加 refresh token 轮换。
- 业务 API 不接触 IdP client secret；secret 只在后端 broker 与 IdP token endpoint 通信时使用。

## 3. 对象锁 Bucket Live 验收

对象锁用于审计归档 WORM 留存。当前 `ARTIFACT_STORE=minio` 是既有 S3 适配器名称；本项目测试环境实际使用 RustFS bucket 写入 retention/legal hold 证据。

生产后端配置示例：

```bash
ARTIFACT_STORE=minio
MINIO_ENDPOINT=rustfs.example.com:9000
MINIO_ACCESS_KEY=<access-key>
MINIO_SECRET_KEY=<secret-key>
MINIO_BUCKET=packaging-artifacts
MINIO_USE_SSL=true
AUDIT_ARCHIVE_RETENTION_MODE=COMPLIANCE
AUDIT_ARCHIVE_RETENTION_DAYS=3650
AUDIT_ARCHIVE_LEGAL_HOLD=true
```

验收命令：

```bash
python scripts/rustfs_check.py \
  --production \
  --skip-container-check \
  --endpoint rustfs.example.com:9000 \
  --access-key "$MINIO_ACCESS_KEY" \
  --secret-key "$MINIO_SECRET_KEY" \
  --use-ssl \
  --object-lock \
  --object-lock-bucket packaging-audit-lock \
  --retention-mode COMPLIANCE \
  --retention-days 3650 \
  --legal-hold
```

本地 RustFS 容器验收可以保留容器检查：

```bash
python scripts/rustfs_check.py \
  --endpoint 127.0.0.1:9000 \
  --access-key rustfsadmin \
  --secret-key rustfsadmin \
  --object-lock \
  --object-lock-bucket packaging-audit-lock
```

通过标准：

- production 模式必须显式传入 `--object-lock`。
- production 模式必须显式指定 `--object-lock-bucket` 或 `--bucket`，不能依赖临时随机 bucket。
- production 模式必须指定有效 retention mode：`GOVERNANCE` 或 `COMPLIANCE`。
- production 模式必须指定大于 0 的 retention days。
- 脚本能连接 S3 endpoint。
- RustFS/MinIO resolver live test 能解析通配符组件并读取 `.sha256` sidecar。
- object-lock live test 能写入 `audit-archives/sha256/<digest>.json`，并通过 S3 API 读回指定 retention mode、retain-until 和 legal hold。

注意事项：

- RustFS/MinIO bucket 必须在创建时启用 Object Lock。已有 bucket 如果没有启用对象锁，写 retention/legal hold 会被对象存储拒绝。
- 生产 retention mode/days 需要由合规或运维团队确认；平台只按配置写入和返回证据。
- 验收 bucket 建议使用生产同配置的临时 bucket，避免污染正式审计归档 bucket。

## 4. 发布制品下载权限验收

构建下载入口 `/api/builds/{id}/download` 会复用其 linked upload 的下载策略：

- `public`：允许匿名下载。
- `platform`：`AUTH_ENABLED=true` 时要求 viewer/operator/admin。
- `restricted`：`AUTH_ENABLED=true` 时要求 operator/admin。

当 build 的 `downloadUrl` 指向本平台 `/api/uploads/{uploadId}/download` 时，build 下载入口会先读取 upload metadata，检查 retention，执行 upload download policy，再记录 `build.download` 审计事件。实际跳转后的 upload 下载仍会记录 `upload.download`。

验收建议：

1. 上传 `downloadPolicy=restricted` 的制品。
2. 创建或完成一个 build，使其 `downloadUrl` 指向该 upload。
3. 使用 viewer token 访问 `/api/builds/{id}/download`，应返回 403。
4. 使用 operator token 访问同一地址，应返回 302 到 upload download URL。
5. 查询 `/api/audit-events?buildId=<id>`，应看到 actor 为 operator 的 `build.download`。

## 5. GitLab Generic Package Registry Live 验收

如果生产 Manifest 的组件源使用 `repository=gitlab` 或 `repository=gitlab:<project>`，发布前需要验证 GitLab Package Registry 能按精确文件下载，并能计算出 Build Lock 所需 sha256。

验收命令：

```bash
python scripts/gitlab_package_check_cli.py check \
  --production \
  --base-url "$GITLAB_PACKAGE_BASE_URL" \
  --token "$GITLAB_PACKAGE_TOKEN" \
  --path "42/engine/1.2.3/engine.tar.gz" \
  --expected-sha256 "$GITLAB_PACKAGE_EXPECTED_SHA256"
```

也可以使用 `repository=gitlab:<project>` 风格：

```bash
python scripts/gitlab_package_check_cli.py check \
  --production \
  --base-url "$GITLAB_PACKAGE_BASE_URL" \
  --token "$GITLAB_PACKAGE_TOKEN" \
  --repository "gitlab:42" \
  --path "engine/1.2.3/engine.tar.gz" \
  --expected-sha256 "$GITLAB_PACKAGE_EXPECTED_SHA256"
```

通过标准：

- production 模式下 GitLab base URL 使用 HTTPS。
- path 必须是精确文件，不能包含 `*`、`?` 或 `[` 通配符。
- 脚本能从 `/api/v4/projects/<project>/packages/generic/<package>/<version>/<file>` 下载文件。
- 下载内容的 sha256 与 `--expected-sha256` 匹配；如果不提供 expected sha，脚本会输出计算值并给出 warning，不能作为强发布门禁。
- 如果生产 GitLab package 是私有项目，发布门禁应加 `--require-token`，确认 `GITLAB_PACKAGE_TOKEN` 已配置。
