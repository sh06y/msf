// Browser-only fixture loaded by tests/e2e/logs.mjs.
import React from "react";
import { createRoot } from "react-dom/client";
import { flushSync } from "react-dom";
import { useLogFeed } from "../useLogFeed";
import { useVirtualLogRows } from "../useVirtualLogRows";
const w = window as any;
const requests: any[] = [],
  streams: any[] = [],
  errors: string[] = [];
window.fetch = ((url: string, options: any) =>
  new Promise((resolve) => {
    // Deliberately ignore abort when completing responses, to exercise generation guards.
    requests.push({
      url,
      signal: options.signal,
      resolve: (logs: any[]) =>
        resolve(
          new Response(JSON.stringify({ logs }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
    });
  })) as any;
class Stream {
  closed = false;
  handler: any;
  constructor(public url: string) {
    streams.push(this);
  }
  addEventListener(_: string, handler: any) {
    this.handler = handler;
  }
  close() {
    this.closed = true;
  }
  emit(logs: any[]) {
    this.handler(new MessageEvent("logs", { data: JSON.stringify({ logs }) }));
  }
}
w.EventSource = Stream;
let pollCallback: (() => void) | undefined;
const interval = window.setInterval.bind(window);
window.setInterval = ((callback: () => void, ms: number) => {
  if (ms === 8000) pollCallback = callback;
  return interval(callback, ms);
}) as any;
let observers = 0;
const NativeObserver = window.ResizeObserver;
window.ResizeObserver = class extends NativeObserver {
  constructor(cb: ResizeObserverCallback) {
    super(cb);
    observers++;
  }
};
const normalize = (x: { id: string }) => x;
const onError = (message: string) => errors.push(message);
let props = { service: "mihomo", level: "all", query: "", paused: false };
let feed: any,
  virtual: any,
  commits = 0,
  scrollCommits = 0,
  count = 1000;
function Feed() {
  feed = useLogFeed({ ...props, normalize, onError });
  return null;
}
function Virtual() {
  virtual = useVirtualLogRows({ count, rowHeight: 24 });
  return React.createElement(
    "div",
    {
      ref: virtual.containerRef,
      onScroll: virtual.measure,
      style: { height: 480, overflow: "auto" },
    },
    React.createElement(
      "div",
      { style: { height: virtual.totalHeight } },
      virtual.virtualRows.map((i: number) => {
        if (i < 0 || i >= count) throw new Error("invalid virtual index");
        return React.createElement("span", { key: i }, String(i));
      }),
    ),
  );
}
const root = createRoot(document.getElementById("probe")!);
function render() {
  flushSync(() =>
    root.render(
      React.createElement(
        React.Fragment,
        null,
        React.createElement(
          React.Profiler,
          { id: "feed", onRender: () => commits++ },
          React.createElement(Feed),
        ),
        React.createElement(
          React.Profiler,
          { id: "virtual", onRender: () => scrollCommits++ },
          React.createElement(Virtual),
        ),
      ),
    ),
  );
}
w.probe = {
  requests,
  streams,
  errors,
  poll: () => pollCallback?.(),
  get feed() {
    return feed;
  },
  get virtual() {
    return virtual;
  },
  get commits() {
    return commits;
  },
  get scrollCommits() {
    return scrollCommits;
  },
  get observers() {
    return observers;
  },
  change: (next: any) => {
    props = { ...props, ...next };
    render();
  },
  resizeCount: (n: number) => {
    count = n;
    render();
  },
  scroll: (top: number) =>
    flushSync(() => {
      virtual.containerRef.current.scrollTop = top;
      virtual.measure();
    }),
  unmount: () => flushSync(() => root.unmount()),
};
render();
