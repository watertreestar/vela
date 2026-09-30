---
id: runner-integrity-02
package: conductor, runner
module: dispatch-capability-build-binding
status: completed
depends-on: [runner-integrity-01]
---

# PR-2：Build/attempt 绑定 claim、日志、heartbeat、complete 与 locked input download

## Objective

以 Build attempt + execution capability 替换 target-wide queue claim，并阻断跨 Build 回写。Runner 仅能 claim 环境中指定的 Build，且只能访问该 Build Lock 内的 platform-hosted inputs。

## Context

- [Phase 7 执行契约](../../phase7-execution-contract.md)
- 依赖 PR-1 落地的 secrets file 与 Nomad job-owned Variable 机制。

## Required implementation

1. 定义独立 HMAC capability，带 typ/aud/build/attempt/target/runner/jti/iat/exp；可注入 clock 的签发/验证测试。
2. 引入 `dispatching`、attempt-based Job ID、expected runner/job/capability metadata、独立 heartbeat timestamp；Store 以条件原子操作完成 prepare → submit → activate/fail。
3. `POST /api/runner/tasks/{buildId}/claim` 替代全局 target claim；log/heartbeat/complete 都用统一 execution authorization helper，不能信任 body source/runner/build。
4. cancel/timeout/terminal/retry fence 旧 attempt；lifecycle 使用 heartbeat timestamp。
5. Runner 读取 exact Build ID/execution file，验证 claim response；Runner 下载 API-hosted locked upload 时带 capability，并对 resolved components 验 sha256。
6. 负例和并发/重试测试覆盖跨 Build、错误 target、错误 runner、错误/过期 token、cancel/timeout/old retry capability。

## Actual result

- Auth-disabled Runner compatibility remains on the legacy target-wide claim and legacy heartbeat paths, without `BUILD_ID`, bearer, or execution capability.
- Auth-required Runner operation requires `BUILD_ID` and an execution capability, then uses build-scoped claim and heartbeat paths.
- The server rejects the legacy target-wide claim whenever API authentication is enabled, including requests without runner authorization.

## Verification

```bash
cd conductor && go test ./...
cd ../runner && bun test && bun run check
git diff --check
```
