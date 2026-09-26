# IPv6 UI end-to-end test

The Playwright script starts the Vite development server, mocks only the HTTP API boundary, and verifies the browser-visible IPv6 settings flow:

- disabled/enabled summary text;
- the FakeIP cache troubleshooting hint;
- one setup save request and canonical FakeIPv6 prefix after refresh;
- automatic, IPv4-first, and IPv6-first UI state;
- one atomic priority request, pending-state protection, refresh persistence, and optimistic rollback on failure.

Run it from the repository root:

```bash
npm ci
npm --prefix web ci
npx playwright install chromium
npm run test:e2e:ipv6
```

Set `HEADED=1` to see the browser. Set `MSF_E2E_BASE_URL` to test an already running frontend instead of starting Vite locally.

# Log performance and concurrency regression test

Run `npm run test:e2e:logs` after the same dependency/browser setup above.
The test starts Vite, exercises the real log page with HTTP/SSE fixtures, and
measures React commits in an isolated hook fixture. It checks query debounce,
Enter, request cancellation and stale responses, pause/service changes,
SSE/snapshot overlap, duplicate batches, bounded virtual rows, observer lifetime,
scrolling, following the latest logs, and clearing logs. `PLAYWRIGHT_CHANNEL=chrome`
can select an installed Chrome instead of bundled Chromium.

The concurrency checks also cover stale SSE observations outside the snapshot
window, server restarts in either response order, and query deadlines with both
stalled headers and stalled response bodies. The browser clock verifies that the
next scheduled poll recovers and ignores late responses after a timeout.
