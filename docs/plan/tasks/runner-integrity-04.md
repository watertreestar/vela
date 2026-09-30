---
id: runner-integrity-04
package: scripts, docs, runner, conductor
module: auth-enabled-runner-e2e
status: implemented-live-accepted-with-production-rollout-gates
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

本地已验证 Python unit tests、Go API/Nomad tests、fixture protocol report、require/live skip semantics 与 diff whitespace。2026-09-29 已完成受控 live acceptance：Ubuntu-a 的 Docker/Nomad/Postgres/RustFS 跑通 auth-enabled Linux Runner final/checksum/readback 和 `runner_e2e_cli.py --live`；本机 Windows raw_exec allocation 跑通 secrets 文件、exact claim、final/checksum/readback、Nomad Job/build binding 与 Windows live readiness ACL gate。完整记录见 [phase7-live-acceptance-2026-09-29.md](../reviews/phase7-live-acceptance-2026-09-29.md)。

仍需生产 rollout gate：Nomad ACL/mTLS 的 task-owned Variable policy、生产 Dagger Runner image、真实 Windows Inno/WiX+签名证书、非默认 RustFS credentials/durable storage 与正式 retention policy。受控 live cluster 未启用 Nomad ACL/mTLS，Windows acceptance 使用受控 test packager 且 signing disabled，因此不得将这些生产 rollout 项描述为已经通过。
