import { describe, expect, it } from "vitest";
import { LOG_LIMIT, mergeLogRows, reconcileLogSnapshot } from "./logRows";
const row = (id: string) => ({ id });

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
        [row("b"), row("c")],
      ),
    ).toEqual([row("a"), row("b"), row("c")]);
  });
  it("does not update an unchanged polling snapshot and replaces an empty one", () => {
    const current = [row("a"), row("b")];
    expect(reconcileLogSnapshot(current, [row("a"), row("b")], [])).toBe(
      current,
    );
    expect(reconcileLogSnapshot(current, [], [])).toEqual([]);
  });
});
