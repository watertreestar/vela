---
id: runner-integrity-04
package: scripts, docs, runner, conductor
module: auth-enabled-runner-e2e
status: pending
depends-on: [runner-integrity-03]
---

# PR-4：Auth-enabled E2E、Linux fixture、Windows host readiness 与文档

## Objective

提供不泄露 Runner secret 的 auth-enabled E2E/readiness evidence，覆盖 Linux fixture 和 Windows raw_exec host，并更新完整部署、轮换、遗留制品与故障文档。

## Context

- [Phase 7 执行契约](../../phase7-execution-contract.md)
- 依赖 PR-3 的可信 output 链路。

## Required implementation

1. 新增受测试覆盖的 runner execution E2E CLI，只使用人类 operator/admin token，绝不读取/打印 Runner bearer/capability。
2. 接入 production readiness，新增 require/live runner E2E gate 和脱敏 JSON 报告。
3. Linux auth-enabled fixture 使用实际 secrets injection + exact claim + component/output checks。
4. Windows readiness 检查 secrets directory/ACL/无 env leak/API reachability；真实 Windows host 的 live command 不能由 Linux fixture 冒充。
5. 更新 API、Runner、Nomad lifecycle、local/production readiness、README、.env example、plans 等文档。

## Verification

运行 Python unit tests、Go/Runner tests、fixture tests 与可用的 Linux E2E。真实 Windows/Nomad 环境不可用时记录为 external blocker 及可复现命令。
