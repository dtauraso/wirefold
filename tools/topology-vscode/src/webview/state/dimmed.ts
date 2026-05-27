// Imperative bridge for dimmed state.
// main.tsx calls setDimmedImperative; store.ts reads current state via getDimmed().

let _current: Set<string> | null = null;

export function setDimmedImperative(next: Set<string> | null) {
  _current = next;
}

export function getDimmed(): Set<string> | null {
  return _current;
}
