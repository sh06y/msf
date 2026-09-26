export const LOG_LIMIT = 1000;

export function mergeLogRows<T extends { id: string }>(
  current: T[],
  incoming: T[],
): T[] {
  if (incoming.length === 0) return current;
  const ids = new Set(current.map((row) => row.id));
  const added: T[] = [];
  for (const row of incoming) {
    if (ids.has(row.id)) continue;
    ids.add(row.id);
    added.push(row);
  }
  return added.length === 0
    ? current
    : [...current, ...added].slice(-LOG_LIMIT);
}

export function reconcileLogSnapshot<T extends { id: string }>(
  current: T[],
  snapshot: T[],
  pending: T[],
): T[] {
  const next = mergeLogRows(mergeLogRows<T>([], snapshot), pending);
  return current.length === next.length &&
    current.every((row, index) => row.id === next[index].id)
    ? current
    : next;
}
