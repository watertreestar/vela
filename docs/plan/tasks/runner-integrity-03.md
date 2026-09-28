---
id: runner-integrity-03
package: conductor, runner
module: trusted-runner-output
status: done
depends-on: [runner-integrity-02]
---

# PR-3：Runner 专用上传、制品归属与可信成功状态

## Objective

提供 capability-bound final/checksum upload，并只允许服务端验证的绑定 output 让 Build 转为 `succeeded`；Preview/Release/evidence 均以权威 upload binding 为准。

## Context

- [Phase 7 执行契约](../../phase7-execution-contract.md)
- 依赖 PR-2 的 execution authorization 和 Build attempt fencing。

## Required implementation

1. Build/Upload 增加 structured ownership，Postgres unique/index/migration 与 JSON Store 等价约束。
2. 增加 final/checksum Runner endpoints；保存时计算 hash，checksum 格式与 final hash/name 强验证，禁止 client metadata/URI/policy override。
3. successful complete 从 verified bindings 回填 URI/URL/hash/size；不接收 URI/URL/digest。object readback 失败/篡改/缺 checksum 必须阻断。
4. 让 preview/release/download/report/evidence 以 `ArtifactUploadID` 工作；legacy 历史保留下载并标 `legacy/unverified`，但不能走新 verification。
5. 收紧 `/api/uploads` list，保持 common upload 仅 operator/admin。
6. 更新 Runner 调用与完整正/负测试。

## Verification

```bash
cd conductor && go test ./...
cd ../runner && bun test && bun run check
```

## 实际结果

- 最终实现由主集成者在前两轮未完成基础上收敛，并经独立验收通过。
- `final` / `checksum` 只接受 current Build attempt 的 execution capability，绑定 Build、attempt、Runner 与 parent final record；服务端对 final/checksum readback 重新计算/验证 SHA-256、size、文件名和 checksum 内容。
- auth-enabled successful complete 只使用服务端绑定记录；Runner 自报 URI/URL 无效，publish body 提供 URI/URL 会被拒绝。
- common `POST /api/uploads` 仍为 operator/admin；`GET /api/uploads` 在 auth 模式要求 viewer+。legacy Build 保留下载且 report 返回 `legacy-unverified`，但 Preview/Release verification 被阻断。
- 已通过：`go test ./...`、`go test -race ./internal/api ./internal/store ./internal/artifact`、`bun test`、`bun run check`、`python -m unittest discover -s scripts -p '*_test.py'`、`git diff --check`。
- 消融结论：不能删除 final/checksum 分离与 readback，否则 Runner 能通过自报 URL/metadata 伪造成功；不能复用 common uploads 权限，否则 runner 会获得跨 Build 写入能力；不能自动把 legacy URL 转为 binding，否则历史数据会被伪造性信任。
- 外部剩余项：真实 MinIO/Postgres/Nomad/Windows live 运行留给 PR-4，不影响本地代码验收。
