# Phase 7 交付分析：构建执行闭环、Runner 鉴权与真实 Release

- 状态：implementation-complete; controlled-live-acceptance-passed; production-rollout-gates-pending
- 交付分支：`phase7/runner-integrity`
- 基线：`6ec7a6f1e8f425e1d7b51c3cdfc016c688b01140`
- 设计依据：[Phase 7 执行契约](../../phase7-execution-contract.md)

## 任务与依赖

| ID | 状态 | 负责人 | 工作区 | 依赖 | 交付 |
|---|---|---|---|---|---|
| runner-integrity-01 | done | 刘洋（开发）/马强（独立验收） | `C:\Users\Administrator\gearlic\vela-phase7` | 无 | PR-1：Runner bearer 与 Nomad secrets template；验收通过 commit `73a0e13` |
| runner-integrity-02 | done | 何杰（开发）/吴刚（独立验收） | `C:\Users\Administrator\gearlic\vela-phase7` | runner-integrity-01 的接口基线 `73a0e13` | PR-2：dispatch/capability/build binding；验收通过 commit `e74dc3e` |
| runner-integrity-03 | done | 丁磊（开发）/主集成（修复）/郭涛（独立验收） | `C:\Users\Administrator\gearlic\vela-phase7` | runner-integrity-02 的接口基线 `e74dc3e` | PR-3：可信 output upload/complete；验收通过 commit `bce5f1e` |
| runner-integrity-04 | controlled-live-accepted | 主集成 | `C:\Users\Administrator\gearlic\vela-phase7` | runner-integrity-03 提交基线 `bce5f1e` | PR-4：auth E2E/readiness/docs；Ubuntu-a Linux+RustFS 和本机 Windows raw_exec 受控验收通过，生产 rollout gate 待执行 |
| runner-integrity-review | done | 郭涛（独立验收）/马强（文档复验） | `C:\Users\Administrator\gearlic\vela-phase7` | 各 PR 开发完成 | 受控 live acceptance 与文档复验通过；生产 rollout 边界已记录 |

## 调度记录

1. 2026-09-28：用户授权按四个 PR 实施。主 agent 创建隔离交付分支，未触碰主工作区的 Studio 未提交修改。
2. 2026-09-28：已固定 capability、Nomad task-owned Variable、attempt、可信 output 与 legacy migration 约定；可以开始 PR-1。
3. 2026-09-28：PR-1 开发完成并由独立验收通过，提交 `73a0e13`。验证：`go test ./...`、`bun test`、`bun run check`、`git diff --check` 均通过。真实 Nomad Variable ACL/template 与 Windows allocation ACL 属于 PR-4 外部 live gate，尚未声称通过。
4. 2026-09-28：PR-2 首轮独立验收发现 auth-disabled Runner 回归；开发修复后复验通过。提交 `e74dc3e`。验证：`go test ./...`、`bun test`（7 pass）、`bun run check`、`git diff --check`。auth-enabled runner 已要求 exact Build capability；auth-disabled 仅保留显式 legacy local path。
5. 2026-09-28：PR-3 经两轮未完成开发后由主集成收敛，独立验收通过。增加 capability-bound final/checksum/readback、legacy-unverified 语义、output list RBAC 和 publish immutability；验证：Go 全量、race focused API/store/artifact、Runner tests/check、Python scripts、diff check。提交 `bce5f1e`。
6. 2026-09-28：PR-4 初轮独立验收发现 live E2E、Windows readiness 与 auth-enabled local acceptance 的 false-pass 路径。主集成已收紧为：受保护的 non-secret runner E2E evidence、Conductor/immutable Nomad Job metadata/Job summary/Allocation 交叉比对、子进程环境而非 argv 传递 human/Nomad token、Windows allocation ID + Job/build binding + 双 secrets 文件 + 有效 ACL + protected evidence hard gate，并禁止 local acceptance 假称 Nomad 模式。Python targeted tests、Go API/Nomad tests、fixture warn 与 require/live failure semantics 已复验。
7. 2026-09-29：受控 live acceptance 在 Ubuntu-a 和本机完成。Ubuntu-a 的 Docker Nomad Runner 使用 task-owned bearer/capability 文件，跑通 exact claim、RustFS final/checksum 上传、server readback、Preview/Release evidence 与 live E2E cross-check；本机 Windows raw_exec 亦跑通 exact claim、双 secrets 文件、受限 allocation-root ACL、final/checksum/readback、Nomad Job/build binding 与 Windows live readiness。完整证据和未覆盖的生产 rollout 条件见 [live acceptance review](../reviews/phase7-live-acceptance-2026-09-29.md)。本轮受控 cluster 未启用 Nomad ACL/mTLS，Linux 打包未跑 Dagger，Windows 未使用真实签名工具链，均保留为生产 rollout gate。
8. 2026-09-29：独立终验初轮发现 `runner/README.md` 有已废弃的 `--operator-token` 示例；已更正为 `PACKAGING_OPERATOR_TOKEN` / `NOMAD_TOKEN` 环境注入和 `--nomad-url`，并补齐 Windows live gate 描述。独立复验判定通过；受控验收与文档交付完成，生产 rollout gate 保持未通过状态。

## 交付规则

- 同一交付基线的写入由主 agent 协调；PR-1→PR-4 串行修改共享 API/Runner/Store。
- 每个 PR 开发完成后，必须交给未参与该 PR 开发的独立验收者。
- 开发与修复交接中需记录消融检查：是否可移除新增 abstraction/state/branch；若不可移除，说明破坏的安全约束及测试证据。
- 不提交、覆盖、暂存或清理 `C:\Users\Administrator\gearlic\vela` 主工作区已有 Studio 改动。
