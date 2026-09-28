import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import {
  loadRunnerBearer,
  request,
  resolveRunnerTokenPath,
  downloadToFile,
  setRunnerBearerForTest,
  upload,
} from "./package-runner.js";

const originalFetch = globalThis.fetch;
const originalAPIURL = process.env.PACKAGING_API_URL;

afterEach(() => {
  globalThis.fetch = originalFetch;
  process.env.PACKAGING_API_URL = originalAPIURL;
  setRunnerBearerForTest(null);
});

describe("runner bearer delivery", () => {
  test("accepts only safe relative token paths", () => {
    expect(resolveRunnerTokenPath("/alloc/secrets", "vela-runner-auth")).toBe(
      path.resolve("/alloc/secrets", "vela-runner-auth"),
    );
    expect(() => resolveRunnerTokenPath("/alloc/secrets", "../token")).toThrow("safe relative");
    expect(() => resolveRunnerTokenPath("/alloc/secrets", "/token")).toThrow("safe relative");
  });

  test("reads a non-empty bearer without exposing it", async () => {
    const directory = await mkdtemp(path.join(os.tmpdir(), "runner-token-"));
    try {
      await writeFile(path.join(directory, "vela-runner-auth"), "runner-test-token\n");
      expect(await loadRunnerBearer({ secretsDir: directory, tokenFile: "vela-runner-auth" })).toBe(
        "runner-test-token",
      );
      await writeFile(path.join(directory, "vela-runner-auth"), "\n");
      await expect(
        loadRunnerBearer({ secretsDir: directory, tokenFile: "vela-runner-auth", required: true }),
      ).rejects.toThrow("empty");
      await expect(
        loadRunnerBearer({ secretsDir: directory, tokenFile: "missing", required: true }),
      ).rejects.toThrow("unavailable");
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });

  test("attaches bearer to JSON and multipart API requests", async () => {
    const calls = [];
    globalThis.fetch = async (url, options = {}) => {
      calls.push({ url: String(url), options });
      return new Response(JSON.stringify({ uri: "local-upload://output" }), { status: 200 });
    };
    setRunnerBearerForTest("runner-test-token");
    await request("/api/runner/tasks/claim", { method: "POST", body: "{}" });

    const directory = await mkdtemp(path.join(os.tmpdir(), "runner-upload-"));
    try {
      const file = path.join(directory, "output.bin");
      await writeFile(file, "output");
      await upload(file);
      await downloadToFile(
        "http://localhost:8080/api/uploads/input/download",
        path.join(directory, "input.bin"),
      );
    } finally {
      await rm(directory, { recursive: true, force: true });
    }

    expect(calls).toHaveLength(3);
    for (const call of calls) {
      expect(new Headers(call.options.headers).get("Authorization")).toBe("Bearer runner-test-token");
    }
  });

  test("does not add Authorization without a bearer", async () => {
    let headers;
    globalThis.fetch = async (_url, options = {}) => {
      headers = new Headers(options.headers);
      return new Response(JSON.stringify({}), { status: 200 });
    };
    await request("/api/runner/tasks/claim", { method: "POST", body: "{}" });
    expect(headers.get("Authorization")).toBeNull();
  });
});
