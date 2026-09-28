# Phase 7 交付分析：构建执行闭环、Runner 鉴权与真实 Release

- 状态：in-progress
- 交付分支：`phase7/runner-integrity`
- 基线：`6ec7a6f1e8f425e1d7b51c3cdfc016c688b01140`
- 设计依据：[Phase 7 执行契约](../../phase7-execution-contract.md)

## 任务与依赖

| ID | 状态 | 负责人 | 工作区 | 依赖 | 交付 |
|---|---|---|---|---|---|
| runner-integrity-01 | ready | 待派发 | `C:\Users\Administrator\gearlic\vela-phase7` | 无 | PR-1：Runner bearer 与 Nomad secrets template |
| runner-integrity-02 | pending | 待派发 | `C:\Users\Administrator\gearlic\vela-phase7` | runner-integrity-01 的接口基线 | PR-2：dispatch/capability/build binding |
| runner-integrity-03 | pending | 待派发 | `C:\Users\Administrator\gearlic\vela-phase7` | runner-integrity-02 | PR-3：可信 output upload/complete |
| runner-integrity-04 | pending | 待派发 | `C:\Users\Administrator\gearlic\vela-phase7` | runner-integrity-03 | PR-4：auth E2E/readiness/docs |
| runner-integrity-review | pending | 独立验收 | `C:\Users\Administrator\gearlic\vela-phase7` | 各 PR 开发完成 | 每个 PR 的独立验收及最终集成验证 |

## 调度记录

1. 2026-09-28：用户授权按四个 PR 实施。主 agent 创建隔离交付分支，未触碰主工作区的 Studio 未提交修改。
2. 2026-09-28：已固定 capability、Nomad task-owned Variable、attempt、可信 output 与 legacy migration 约定；可以开始 PR-1。

## 交付规则

- 同一交付基线的写入由主 agent 协调；PR-1→PR-4 串行修改共享 API/Runner/Store。
- 每个 PR 开发完成后，必须交给未参与该 PR 开发的独立验收者。
- 开发与修复交接中需记录消融检查：是否可移除新增 abstraction/state/branch；若不可移除，说明破坏的安全约束及测试证据。
- 不提交、覆盖、暂存或清理 `C:\Users\Administrator\gearlic\vela` 主工作区已有 Studio 改动。
