import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";

export interface VirtualLogRange {
  start: number;
  end: number;
}

export function calculateVirtualLogRange(
  count: number,
  scrollTop: number,
  viewportHeight: number,
  rowHeight: number,
  overscan: number,
): VirtualLogRange {
  if (count <= 0 || rowHeight <= 0) return { start: 0, end: 0 };
  const maxScrollTop = Math.max(0, count * rowHeight - viewportHeight);
  const firstVisible = Math.floor(Math.min(maxScrollTop, Math.max(0, scrollTop)) / rowHeight);
  const visibleCount = Math.ceil(Math.max(0, viewportHeight) / rowHeight);
  const start = Math.max(0, firstVisible - overscan);
  const end = Math.min(count, firstVisible + visibleCount + overscan);
  return { start, end: Math.max(start, end) };
}

export function useVirtualLogRows({
  count,
  rowHeight,
  overscan = 10,
}: {
  count: number;
  rowHeight: number;
  overscan?: number;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [viewport, setViewport] = useState({ scrollTop: 0, height: 400 });
  // Derive indices from the current count during render, before layout effects run.
  const range = calculateVirtualLogRange(count, viewport.scrollTop, viewport.height, rowHeight, overscan);

  const measure = useCallback(() => {
    const container = containerRef.current;
    if (!container) return;
    const next = { scrollTop: container.scrollTop, height: container.clientHeight };
    setViewport((current) =>
      current.scrollTop === next.scrollTop && current.height === next.height ? current : next,
    );
  }, []);

  useLayoutEffect(() => {
    measure();
    const container = containerRef.current;
    if (!container) return;
    const observer = new ResizeObserver(measure);
    observer.observe(container);
    return () => observer.disconnect();
  }, [count, measure]);

  const virtualRows = useMemo(
    () => Array.from({ length: range.end - range.start }, (_, offset) => range.start + offset),
    [range.end, range.start],
  );

  return {
    containerRef,
    measure,
    virtualRows,
    totalHeight: count * rowHeight,
  };
}
