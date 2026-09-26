import { describe, expect, it } from "vitest";
import { LOG_LIMIT, mergeLogRows, reconcileLogSnapshot } from "./logRows";
const row = (id: string) => ({ id });
const cursor = (revision: number, epoch = "server-a") => ({ epoch, revision });
const pending = (ids: string[], revision: number, epoch = "server-a") =>
  ids.map((id) => ({ id, row: row(id), cursor: cursor(revision, epoch) }));

describe("log batches", () => {
  it("retains the array for duplicate/empty batches without mutating inputs", () => {
    const current = Object.freeze([row("a"), row("b")]) as unknown as {
      id: string;
    }[];
    expect(mergeLogRows(current, [row("b"), row("a")])).toBe(current);
    expect(mergeLogRows(current, [])).toBe(current);
    expect(mergeLogRows(current, [row("c"), row("c")])).toEqual([
      row("a"),
      row("b"),
      row("c"),
    ]);
  });
  it("keeps only the latest 1000 distinct entries for both the list and pending buffer", () => {
    const rows = Array.from({ length: 1500 }, (_, i) => row(String(i)));
    expect(mergeLogRows([], rows)).toEqual(rows.slice(-LOG_LIMIT));
  });
  it("merges events arriving during a snapshot without duplicates or losing new rows", () => {
    expect(
      reconcileLogSnapshot(
        [row("a"), row("c")],
        [row("a"), row("b")],
        pending(["b", "c"], 2),
        cursor(1),
      ),
    ).toEqual([row("a"), row("b"), row("c")]);
  });
  it("does not update an unchanged polling snapshot and replaces an empty one", () => {
    const current = [row("a"), row("b")];
    expect(
      reconcileLogSnapshot(current, [row("a"), row("b")], [], cursor(1)),
    ).toBe(current);
    expect(reconcileLogSnapshot(current, [], [], cursor(1))).toEqual([]);
  });
});

describe("snapshot observation boundary", () => {
  it("does not resurrect an older SSE tail or evict snapshot rows", () => {
    const snapshot = Array.from({ length: 1000 }, (_, i) =>
      row(String(500 + i)),
    );
    const buffered = pending(
      Array.from({ length: 80 }, (_, i) => String(480 + i)),
      1,
    );
    expect(reconcileLogSnapshot([], snapshot, buffered, cursor(2))).toEqual(
      snapshot,
    );
  });
  it("keeps only observations newer than the snapshot, including when no IDs overlap", () => {
    expect(
      reconcileLogSnapshot(
        [],
        [row("b")],
        [...pending(["a"], 1), ...pending(["c"], 3)],
        cursor(2),
      ),
    ).toEqual([row("b"), row("c")]);
    expect(reconcileLogSnapshot([], [], pending(["c"], 3), cursor(2))).toEqual([
      row("c"),
    ]);
  });
  it("does not merge events from a previous server process", () => {
    expect(
      reconcileLogSnapshot(
        [],
        [row("b")],
        pending(["a"], 100, "old-server"),
        cursor(1),
      ),
    ).toEqual([row("b")]);
  });
});
