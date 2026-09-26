import { describe, expect, it } from "vitest";
import { calculateVirtualLogRange } from "./useVirtualLogRows";

describe("calculateVirtualLogRange", () => {
  it("mounts only the visible log rows plus overscan", () => {
    expect(calculateVirtualLogRange(1000, 4800, 480, 24, 10)).toEqual({
      start: 190,
      end: 230,
    });
  });

  it("clamps the range at the beginning and end", () => {
    expect(calculateVirtualLogRange(12, 0, 240, 24, 10)).toEqual({ start: 0, end: 12 });
    expect(calculateVirtualLogRange(100, 2300, 240, 24, 10)).toEqual({ start: 80, end: 100 });
  });

  it("uses the new result count while the scroll position still belongs to the full list", () => {
    expect(calculateVirtualLogRange(3, 23520, 480, 24, 10)).toEqual({ start: 0, end: 3 });
    expect(calculateVirtualLogRange(100, 23520, 480, 24, 10)).toEqual({ start: 70, end: 100 });
  });

  it("handles no matches and results returning after clearing the query", () => {
    expect(calculateVirtualLogRange(0, 23520, 480, 24, 10)).toEqual({ start: 0, end: 0 });
    expect(calculateVirtualLogRange(1000, 0, 480, 24, 10)).toEqual({ start: 0, end: 30 });
  });
});
