#!/usr/bin/env node
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import net from "node:net";
import { chromium } from "playwright";

const repoRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);
let vite;
let browser;
async function startServer() {
  if (process.env.MSF_E2E_BASE_URL) return process.env.MSF_E2E_BASE_URL;
  const port = await new Promise((resolve, reject) => {
    const socket = net.createServer();
    socket.once("error", reject);
    socket.listen(0, "127.0.0.1", () => {
      const port = socket.address().port;
      socket.close(() => resolve(port));
    });
  });
  const baseURL = `http://127.0.0.1:${port}`;
  vite = spawn(
    process.execPath,
    [
      path.join(repoRoot, "web/node_modules/vite/bin/vite.js"),
      "--host",
      "127.0.0.1",
      "--port",
      String(port),
      "--strictPort",
    ],
    { cwd: path.join(repoRoot, "web"), stdio: ["ignore", "pipe", "pipe"] },
  );
  vite.stderr.pipe(process.stderr);
  for (let i = 0; i < 300; i++) {
    if (vite.exitCode !== null) throw new Error("Vite exited before startup");
    try {
      if ((await fetch(baseURL)).ok) return baseURL;
    } catch {}
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("Vite startup timed out");
}

async function checkLogHooks(page, baseURL) {
  await page.route("**/__logs_probe.html", (route) =>
    route.fulfill({
      contentType: "text/html",
      body: '<!doctype html><div id="probe"></div><script type="module" src="/src/features/logs/testing/logsProbe.ts"></script>',
    }),
  );
  await page.goto(`${baseURL}/__logs_probe.html`);
  await page.waitForFunction(() => window.probe?.requests.length === 1);
  return await page.evaluate(async () => {
    const p = window.probe;
    const check = (ok, msg) => {
      if (!ok) throw new Error(msg);
    };
    const tick = () => new Promise((r) => setTimeout(r, 30));
    const row = (id) => ({ id });
    const summary = {};
    p.requests[0].resolve([row("a"), row("b")]);
    await tick();
    check(p.feed.logs.length === 2, "initial snapshot");
    const before = p.commits;
    p.streams[0].emit([row("a"), row("b")]);
    await tick();
    summary.duplicateBatchCommits = p.commits - before;
    check(p.commits === before, "duplicate batch committed");
    p.poll();
    const slowCount = p.requests.length;
    p.poll();
    check(p.requests.length === slowCount, "overlapping polling request");
    p.streams[0].emit([row("b"), row("c")]);
    await tick();
    p.requests[1].resolve([row("a"), row("b")]);
    await tick();
    check(
      p.feed.logs.map((x) => x.id).join(",") === "a,b,c",
      "snapshot overwrote SSE",
    );
    p.change({ query: "a" });
    p.change({ query: "ab" });
    p.change({ query: "abc" });
    check(p.requests.length === 2, "request sent before debounce");
    await new Promise((r) => setTimeout(r, 250));
    check(
      p.requests.length === 3 && p.requests[2].url.includes("q=abc"),
      "debounce request count",
    );
    summary.requestsForThreeKeystrokes = 1;
    p.change({ query: "new" });
    check(p.requests[2].signal.aborted, "old query not aborted");
    const enter = p.feed.load();
    check(p.requests.length === 4, "Enter not immediate");
    p.requests[3].resolve([row("new")]);
    await enter;
    await tick();
    p.requests[2].resolve([row("old")]);
    await tick();
    p.streams[1].emit([row("old-stream")]);
    await tick();
    check(
      p.feed.logs.map((x) => x.id).join(",") === "new",
      "stale response/stream accepted",
    );
    await new Promise((r) => setTimeout(r, 250));
    check(p.requests.length === 4, "Enter left duplicate debounce");
    const pausedLoad = p.feed.load();
    p.change({ paused: true });
    check(
      p.requests[4].signal.aborted && p.streams.at(-1).closed,
      "pause did not cancel",
    );
    p.requests[4].resolve([row("paused")]);
    await pausedLoad;
    await tick();
    check(p.feed.logs[0].id === "new", "paused response accepted");
    p.change({ paused: false, service: "mosdns", query: "" });
    check(p.feed.logs.length === 0, "service retained previous logs");
    const switchLoad = p.feed.load();
    p.requests.at(-1).resolve([row("mosdns")]);
    await switchLoad;
    await tick();
    check(p.feed.logs[0].id === "mosdns", "service switch snapshot");
    const batch = Array.from({ length: 100 }, (_, i) => row("batch-" + i));
    const batchBefore = p.commits;
    p.streams.at(-1).emit(batch);
    await tick();
    summary.commitsFor100Rows = p.commits - batchBefore;
    check(summary.commitsFor100Rows === 1, "batch committed more than once");
    const observed = p.observers;
    p.scroll(2400);
    await tick();
    const scrollBefore = p.scrollCommits;
    for (let i = 1; i <= 20; i++) {
      p.scroll(2400 + i);
      await tick();
    }
    summary.commitsFor20SameRowScrolls = p.scrollCommits - scrollBefore;
    check(summary.commitsFor20SameRowScrolls === 0, "pixel scroll committed");
    p.scroll(2424);
    await tick();
    check(p.scrollCommits === scrollBefore + 1, "row crossing did not update");
    p.resizeCount(3);
    await tick();
    check(p.virtual.virtualRows.join(",") === "0,1,2", "shrunk indices");
    p.resizeCount(0);
    await tick();
    check(p.virtual.virtualRows.length === 0, "zero indices");
    p.resizeCount(1000);
    await tick();
    check(p.virtual.virtualRows.length > 0, "restored rows");
    check(p.observers === observed, "observer recreated on count");
    // A delayed initial/replayed SSE tail must not evict a newer HTTP window.
    const highLoad = p.feed.load();
    const highRequest = p.requests.at(-1);
    const full = Array.from({ length: 1000 }, (_, i) => row(String(500 + i)));
    p.streams.at(-1).emit(
      Array.from({ length: 80 }, (_, i) => row(String(480 + i))),
      { epoch: "server-a", revision: 199 },
    );
    p.streams.at(-1).emit([row("1500")], { epoch: "server-a", revision: 201 });
    highRequest.resolve(full, { epoch: "server-a", revision: 200 });
    await highLoad;
    await tick();
    check(
      p.feed.logs.length === 1000 &&
        p.feed.logs[0].id === "501" &&
        p.feed.logs.at(-1).id === "1500",
      "old SSE rows evicted snapshot rows",
    );
    p.streams.at(-1).emit([row("480")], { epoch: "server-a", revision: 198 });
    await tick();
    check(
      p.feed.logs.at(-1).id === "1500",
      "late stale SSE accepted after snapshot",
    );

    // A server restart during a request invalidates the previous process response.
    const oldLoad = p.feed.load();
    const oldRequest = p.requests.at(-1);
    p.streams
      .at(-1)
      .emit([row("restarted")], { epoch: "server-b", revision: 1 });
    check(oldRequest.signal.aborted, "restart did not retire old request");
    p.requests
      .at(-1)
      .resolve([row("restarted")], { epoch: "server-b", revision: 2 });
    await tick();
    oldRequest.resolve([row("old-process")], {
      epoch: "server-a",
      revision: 202,
    });
    await oldLoad;
    await tick();
    check(
      p.feed.logs.map((x) => x.id).join(",") === "restarted",
      "old process snapshot accepted",
    );
    const oldStream = p.streams.at(-1);
    const restartLoad = p.feed.load();
    p.requests
      .at(-1)
      .resolve([row("third-process")], { epoch: "server-c", revision: 1 });
    await restartLoad;
    await tick();
    oldStream.emit([row("old-process")], { epoch: "server-b", revision: 3 });
    await tick();
    check(
      oldStream.closed && p.feed.logs[0].id === "third-process",
      "old process stream accepted",
    );
    summary.observationBoundary =
      "stale overlapping/non-overlapping observations and both restart orders passed";
    const unmountLoad = p.feed.load();
    const last = p.requests.at(-1);
    p.unmount();
    check(
      last.signal.aborted && p.streams.at(-1).closed,
      "unmount did not cancel",
    );
    last.resolve([row("unmounted")]);
    await unmountLoad;
    await tick();
    check(p.errors.length === 0, "unexpected request errors");
    summary.concurrentScenarios =
      "snapshot+SSE, debounce, stale response/stream, Enter, pause, service switch, unmount passed";
    return summary;
  });
}

async function checkLogPage(page, baseURL) {
  page.setDefaultTimeout(10000);
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const requests = [];
  const entry = (
    i,
    message = `fixture-log-${i}${i % 100 === 0 ? " needle" : ""}`,
  ) => ({
    time: "2026-09-26 10:00:00",
    level: i % 2 === 0 ? "INFO" : "WARN",
    message,
    source: "mihomo.out",
  });
  let rows = Array.from({ length: 1000 }, (_, i) => entry(i));
  await page.addInitScript(() => {
    localStorage.setItem("msf_token", "test");
    window.logStreams = [];
    window.logRevision = 0;
    window.EventSource = class {
      closed = false;
      constructor(url) {
        this.url = url;
        window.logStreams.push(this);
      }
      addEventListener(_, callback) {
        this.callback = callback;
      }
      close() {
        this.closed = true;
      }
      emit(logs) {
        this.callback(
          new MessageEvent("logs", {
            data: JSON.stringify({
              logs,
              cursor: { epoch: "page", revision: ++window.logRevision },
            }),
          }),
        );
      }
    };
  });
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    let body = { success: true, data: {} };
    if (url.pathname === "/api/v1/setup/check") body = { is_initialized: true };
    if (url.pathname === "/api/v1/auth/me")
      body = { user: { username: "root", role: "admin" } };
    if (url.pathname === "/api/v1/logs/mihomo") {
      requests.push({
        method: route.request().method(),
        q: url.searchParams.get("q") || "",
      });
      if (route.request().method() === "DELETE") rows = [];
      const query = url.searchParams.get("q") || "";
      const level = url.searchParams.get("level");
      const cursor = await page.evaluate(() => ({
        epoch: "page",
        revision: ++window.logRevision,
      }));
      body = {
        cursor,
        logs: rows.filter(
          (row) =>
            row.message.includes(query) &&
            (!level || row.level.toLowerCase() === level),
        ),
      };
    }
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify(body),
    });
  });
  await page.goto(`${baseURL}/mihomo/logs`);
  const search = page.getByRole("textbox", { name: "搜索日志..." });
  const list = page.getByLabel("虚拟化日志列表");
  await page.getByText("fixture-log-999", { exact: true }).waitFor();
  await search.pressSequentially("needle", { delay: 25 });
  await page.waitForResponse(
    (r) =>
      r.url().includes("/api/v1/logs/mihomo?") && r.url().includes("q=needle"),
  );
  await page.getByText("fixture-log-900 needle", { exact: true }).waitFor();
  assert.equal(await list.locator(".absolute").count(), 10);
  assert.deepEqual(
    requests.filter((r) => r.q).map((r) => r.q),
    ["needle"],
  );
  await search.fill("fixture-log-999");
  await search.press("Enter");
  await page.getByText("fixture-log-999", { exact: true }).waitFor();
  await page.waitForFunction(
    () =>
      document
        .querySelector('[aria-label="虚拟化日志列表"]')
        .querySelectorAll(".absolute").length === 1,
  );
  await search.fill("no-match-xyz");
  await page.getByText("暂无日志", { exact: true }).waitFor();
  const restored = page.waitForResponse(
    (r) =>
      r.url().includes("/api/v1/logs/mihomo?") &&
      !new URL(r.url()).searchParams.has("q"),
  );
  await search.fill("");
  await restored;
  await page.waitForFunction(
    () =>
      document.querySelector('[aria-label="虚拟化日志列表"]').scrollHeight >
      20000,
  );
  await page.getByText("fixture-log-999", { exact: true }).waitFor();
  await list.evaluate((el) => {
    el.scrollTop = 12000;
    el.dispatchEvent(new Event("scroll", { bubbles: true }));
  });
  await page.getByRole("button", { name: "回到最新" }).waitFor();
  const scrollTop = await list.evaluate((el) => el.scrollTop);
  await page.evaluate(
    (row) => window.logStreams.at(-1).emit([row]),
    entry(1000),
  );
  assert.equal(await list.evaluate((el) => el.scrollTop), scrollTop);
  await page.getByRole("button", { name: "回到最新" }).click();
  await page.getByText("fixture-log-1000 needle", { exact: true }).waitFor();
  await page.getByRole("button", { name: "暂停", exact: true }).click();
  await page.evaluate(
    (row) => window.logStreams.at(-1).emit([row]),
    entry(1001),
  );
  assert.equal(
    await page.getByText("fixture-log-1001", { exact: true }).count(),
    0,
  );
  rows.push(entry(1000));
  await page.getByRole("button", { name: "继续", exact: true }).click();
  await page.getByRole("combobox").selectOption("WARN");
  await page.getByText("fixture-log-999", { exact: true }).waitFor();
  await page.getByRole("button", { name: "清空", exact: true }).click();
  await page.getByText("暂无日志", { exact: true }).waitFor();
  assert.equal(requests.filter((r) => r.method === "DELETE").length, 1);
  assert.deepEqual(errors, []);
  return "search, Enter, no matches, restore, manual scroll, follow, pause/resume, level, clear passed";
}

async function checkLogDeadline(browser, baseURL) {
  for (const phase of ["headers", "body"]) {
    const page = await browser.newPage();
    await page.clock.install();
    await page.route("**/__logs_probe.html", (route) =>
      route.fulfill({
        contentType: "text/html",
        body: '<!doctype html><div id="probe"></div><script type="module" src="/src/features/logs/testing/logsProbe.ts"></script>',
      }),
    );
    await page.goto(`${baseURL}/__logs_probe.html`);
    await page.waitForFunction(() => window.probe?.requests.length === 1);
    if (phase === "body")
      await page.evaluate(() => window.probe.requests[0].stallBody());
    await page.clock.runFor(6100);
    assert.deepEqual(
      await page.evaluate(() => ({
        aborted: window.probe.requests[0].signal.aborted,
        loading: window.probe.feed.loading,
        errors: window.probe.errors.length,
      })),
      { aborted: true, loading: false, errors: 1 },
    );
    await page.clock.runFor(2000);
    assert.equal(await page.evaluate(() => window.probe.requests.length), 2);
    await page.evaluate(() =>
      window.probe.requests[1].resolve([{ id: "recovered" }]),
    );
    await page.clock.runFor(50);
    await page.waitForFunction(
      () =>
        window.probe.feed.logs[0]?.id === "recovered" &&
        !window.probe.feed.loading,
    );
    if (phase === "headers") {
      await page.evaluate(() =>
        window.probe.requests[0].resolve([{ id: "late" }]),
      );
      await page.clock.runFor(50);
      assert.equal(
        await page.evaluate(() => window.probe.feed.logs[0].id),
        "recovered",
      );
    }
    await page.evaluate(() => window.probe.unmount());
    await page.close();
  }
  return "stalled headers/body expire; next scheduled poll recovers; late response rejected";
}

try {
  const baseURL = await startServer();
  browser = await chromium.launch({
    headless: process.env.HEADED !== "1",
    channel: process.env.PLAYWRIGHT_CHANNEL || undefined,
  });
  const hooks = await checkLogHooks(await browser.newPage(), baseURL);
  console.log("Hook checks:", JSON.stringify(hooks));
  const page = await checkLogPage(await browser.newPage(), baseURL);
  const deadline = await checkLogDeadline(browser, baseURL);
  console.log(JSON.stringify({ hooks, page, deadline }, null, 2));
} finally {
  await browser?.close();
  vite?.kill("SIGTERM");
}
