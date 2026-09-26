import { useCallback, useLayoutEffect, useRef, useState } from "react";
import { api, apiList } from "@/lib/api";
import { LOG_LIMIT, mergeLogRows, reconcileLogSnapshot } from "./logRows";

export function useLogFeed<E, T extends { id: string }>({
  service,
  level,
  query,
  paused,
  normalize,
  onError,
}: {
  service: string;
  level: string;
  query: string;
  paused: boolean;
  normalize: (entry: E) => T;
  onError: (message: string) => void;
}) {
  const [logs, setLogs] = useState<T[]>([]);
  const rowsRef = useRef<T[]>(logs);
  const [stats, setStats] = useState({ total: 0, error: 0, warn: 0 });
  const [loading, setLoading] = useState(false);
  const previous = useRef({ service, query });
  const refreshRef = useRef<() => Promise<void>>(async () => {});
  const load = useCallback(() => refreshRef.current(), []);

  // Retire the old request/stream at commit, before queued callbacks can write
  // results for a previous service, filter, or pause state.
  useLayoutEffect(() => {
    const queryChanged = previous.current.query !== query;
    if (previous.current.service !== service) {
      rowsRef.current = [];
      setLogs([]);
      setStats({ total: 0, error: 0, warn: 0 });
    }
    previous.current = { service, query };
    setLoading(false);
    if (paused) {
      refreshRef.current = async () => {};
      return;
    }

    let active = true;
    let request: AbortController | null = null;
    let pending: T[] = [];
    let source: EventSource | null = null;
    let debounce: number | undefined;
    let poll: number | undefined;
    const search = new URLSearchParams();
    if (level !== "all") search.set("level", level.toLowerCase());
    if (query.trim()) search.set("q", query.trim());

    const publish = (next: T[]) => {
      if (next === rowsRef.current) return;
      rowsRef.current = next;
      setLogs(next);
    };

    const connect = () => {
      if (source) return;
      const streamSearch = new URLSearchParams(search);
      streamSearch.set("lines", "80");
      const token = window.localStorage.getItem("msf_token");
      if (token) streamSearch.set("token", token);
      source = new EventSource(
        `/api/v1/events/logs/${service}?${streamSearch}`,
      );
      source.addEventListener("logs", ((event: MessageEvent) => {
        if (!active) return;
        let incoming: T[];
        try {
          incoming = apiList<E>(JSON.parse(event.data), ["logs"]).map(
            normalize,
          );
        } catch {
          return;
        }
        if (request) pending = mergeLogRows(pending, incoming);
        publish(mergeLogRows(rowsRef.current, incoming));
      }) as EventListener);
    };

    const refresh = async () => {
      if (!active) return;
      window.clearTimeout(debounce);
      request?.abort();
      const controller = new AbortController();
      request = controller;
      pending = [];
      setLoading(true);
      connect();
      try {
        const snapshotSearch = new URLSearchParams(search);
        snapshotSearch.set("lines", String(LOG_LIMIT));
        const payload = await api<{ logs: E[]; stats?: Partial<typeof stats> }>(
          `/api/v1/logs/${service}?${snapshotSearch}`,
          { signal: controller.signal },
        );
        if (!active || request !== controller) return;
        const snapshot = apiList<E>(payload, ["logs"]).map(normalize);
        const receivedDuringRequest = pending;
        publish(
          reconcileLogSnapshot(
            rowsRef.current,
            snapshot,
            receivedDuringRequest,
          ),
        );
        setStats({
          total: Number(payload.stats?.total ?? snapshot.length),
          error: Number(payload.stats?.error ?? 0),
          warn: Number(payload.stats?.warn ?? 0),
        });
      } catch (error) {
        if (active && request === controller && !controller.signal.aborted) {
          onError(error instanceof Error ? error.message : String(error));
        }
      } finally {
        if (active && request === controller) {
          request = null;
          pending = [];
          setLoading(false);
        }
      }
    };
    const start = () => {
      if (!active) return Promise.resolve();
      if (!poll)
        poll = window.setInterval(() => {
          if (!request) void refresh();
        }, 8000);
      return refresh();
    };
    refreshRef.current = start;
    if (queryChanged) debounce = window.setTimeout(() => void start(), 200);
    else void start();
    return () => {
      active = false;
      request?.abort();
      source?.close();
      window.clearTimeout(debounce);
      window.clearInterval(poll);
      pending = [];
    };
  }, [service, level, query, paused, normalize, onError]);

  return { logs, stats, loading, load };
}
