// Stable structural fingerprint of a topology.json document.
//
// The `view` key holds positions + fades (diagram view state). A change
// there does NOT require rebuilding the graph or restarting Go. Everything
// else (`nodes`, `edges`, …) is structural. `structuralKey` returns a
// deterministic string that is equal for two texts with identical structure
// regardless of `view` contents or incidental key order.
//
// Fail-soft: if the text is not valid JSON, the raw text is returned so a
// malformed external edit still produces a (different) key and triggers a
// reload.

function stableStringify(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return "[" + value.map(stableStringify).join(",") + "]";
  }
  const obj = value as Record<string, unknown>;
  const keys = Object.keys(obj).sort();
  return "{" + keys.map((k) => JSON.stringify(k) + ":" + stableStringify(obj[k])).join(",") + "}";
}

export function structuralKey(text: string): string {
  try {
    const parsed = JSON.parse(text);
    if (parsed !== null && typeof parsed === "object" && !Array.isArray(parsed)) {
      const { view: _view, ...rest } = parsed as Record<string, unknown>;
      return stableStringify(rest);
    }
    return stableStringify(parsed);
  } catch {
    return text;
  }
}
