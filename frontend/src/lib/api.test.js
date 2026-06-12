import { afterEach, describe, expect, it, vi } from "vitest";
import { listProducts } from "./api";

describe("api error contract", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("keeps structured i18n and correlation fields from backend errors", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 422,
      json: async () => ({
        error: {
          code: "BUILD_RELEASE_GATE_BLOCKED",
          status: 422,
          messageKey: "errors.build.releaseGateBlocked",
          message: "release gate blocked",
          severity: "warning",
          category: "business",
          retryable: false,
          userActionKey: "actions.fixReleaseGate",
          userAction: "fix release gate blockers before publishing",
          requestId: "req-test",
          correlationId: "corr-test",
          details: [{ code: "release.fixed-version", message: "release requires fixed version" }],
        },
      }),
    });

    await expect(listProducts()).rejects.toMatchObject({
      name: "ApiError",
      status: 422,
      code: "BUILD_RELEASE_GATE_BLOCKED",
      messageKey: "errors.build.releaseGateBlocked",
      userActionKey: "actions.fixReleaseGate",
      requestId: "req-test",
      correlationId: "corr-test",
      details: [{ code: "release.fixed-version" }],
    });
  });

  it("supports legacy error arrays", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 400,
      json: async () => ({ error: "bad request", errors: ["field is required"] }),
    });

    await expect(listProducts()).rejects.toMatchObject({
      name: "ApiError",
      status: 400,
      code: "REQUEST_FAILED",
      message: "bad request：field is required",
      details: [{ message: "field is required" }],
    });
  });

  it("falls back when the response is not JSON", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      json: async () => {
        throw new Error("not json");
      },
    });

    await expect(listProducts()).rejects.toMatchObject({
      name: "ApiError",
      status: 500,
      code: "REQUEST_FAILED",
      message: "Request failed: 500",
      severity: "critical",
      category: "system",
    });
  });

  it("maps network failures to a retryable API unavailable error", async () => {
    global.fetch = vi.fn().mockRejectedValue(new TypeError("Failed to fetch"));

    await expect(listProducts()).rejects.toMatchObject({
      name: "ApiError",
      status: 0,
      code: "API_UNAVAILABLE",
      messageKey: "errors.api.unavailable",
      message: "API unavailable",
      severity: "critical",
      category: "network",
      retryable: true,
      userActionKey: "actions.checkApiService",
      details: [{ code: "api.baseUrl", title: "API 地址", message: "http://localhost:8080" }],
    });
  });
});
