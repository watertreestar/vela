---
id: runner-integrity-01
package: conductor, runner, infra
module: runner-auth-nomad-secrets
status: done
depends-on: []
---

# PR-1：Runner 身份契约与安全秘密投递

## Objective

在 `AUTH_ENABLED=true && DRY_RUN=false` 下，真实 Nomad Job 将 Runner static bearer 以 task-owned Nomad Variable + template 渲染到 allocation secrets 文件；Runner 从 `$NOMAD_SECRETS_DIR/$PACKAGING_API_TOKEN_FILE` 读取并在所有控制面请求中附加 `Authorization: Bearer`。严禁将 secret 放入 `Task.Env`、日志或持久化 Build/Audit 数据。

本 PR 不放宽通用 `/api/uploads` 给 Runner，也不实现 output binding。

## Context

- [Phase 7 执行契约](../../phase7-execution-contract.md)
- 基线：`6ec7a6f1e8f425e1d7b51c3cdfc016c688b01140`
- 当前漏洞：Runner 请求未携带 token，动态 Job 所有配置都直接进入 Env。
- Nomad task workload identity 对 `nomad/jobs/{jobId}/builder/package/...` 变量具隐式访问；template 以 `nomadVar` 渲染到 `secrets/`，且不得 `env=true`。

## Path

- `conductor/internal/config/config.go`
- `conductor/internal/nomad/scheduler.go`, `conductor/internal/nomad/scheduler_test.go`
- `runner/package-runner.js`, `runner/package.json`, 新增对应 Runner JS 测试
- `infra/nomad/packaging-runner.nomad.hcl`
- `infra/windows/prepare-runner.ps1`
- `runner/README.md`, `docs/nomad-runner-lifecycle.md`, `.env.example`, `README.md`

## Required implementation

1. 为 auth-enabled real scheduling 添加明确配置验证：需要 `AUTH_RUNNER_TOKEN`，并为后续 capability 预留独立 key/TTL 配置；配置无效时 fail closed。
2. 扩展 Scheduler 的 dispatch 生命周期，使其可在 Job register 前在 job-owned path 写 Variable，失败时删除；template 将它渲染为 `secrets/vela-runner-auth`。第一版可同时生成 capability placeholder 文件，但不可把 raw static token 放 Env。
3. Job 使用 task `WorkloadIdentity{File:true, Env:false}`（或等效已验证的 Nomad API 输出）；Template 使用 `destination=secrets/...`、`env=false`，Linux `0400`。Windows 不能承诺 POSIX mode，文档描述 ACL/secret dir。
4. Runner 添加纯函数/可测 helper：安全解析 secret file path（拒绝 traversal、绝不把 content 打印），并让 JSON、multipart、platform upload download 请求统一加 bearer。没有 token file 时维持 auth-disabled 可用；当显式配置 auth-required runner mode 时 fail fast。
5. 同步示例、Windows 准备说明及文档，禁止 `runner.env` 写 token。
6. 新增 unit tests：Job Env/JSON 中没有 bearer；template/workload identity/path 正确；Runner 对每个请求包含 bearer、无 token 不额外带 Authorization、文件空/不存在时安全失败。

## Verification

```bash
cd conductor && go test ./internal/nomad ./internal/config ./internal/api
cd ../runner && bun test && bun run check
```

并运行开发者选择的相关全量回归。返回实际命令/结果、修改文件、消融检查和尚未完成项。

## 实际结果

- 开发实现：`73a0e13`。
- 独立验收：pass。
- 已通过：`cd conductor && go test ./...`、`cd runner && bun test`、`cd runner && bun run check`、`git diff --check`。
- 消融结论：不能移除 task-owned Variable/template/workload identity，否则 bearer 会回落到 Env 或需要过宽 ACL；不能只给 Runner 加 bearer header，否则 auth-enabled Job 仍无法安全取得凭据。通用 `/api/uploads` 权限未变。
- 外部 live 未完成：真实 Nomad Variable/workload-identity/template ACL 与 Windows allocation secrets ACL，交给 PR-4 的 require/live gate。
