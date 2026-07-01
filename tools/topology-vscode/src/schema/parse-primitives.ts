// JSON parser primitives: type-narrowing helpers + ParseError class.
// Used by parse-nodes-edges.ts, parse-meta.ts, and parse-spec.ts.

import type { StateValue } from "./types";

export class ParseError extends Error {}

export const fail = (path: string, msg: string): never => {
  throw new ParseError(`${path}: ${msg}`);
};

export const isObj = (v: unknown): v is Record<string, unknown> =>
  v !== null && typeof v === "object" && !Array.isArray(v);

export const str = (v: unknown, path: string): string =>
  typeof v === "string" ? v : fail(path, `expected string, got ${typeof v}`);

export const num = (v: unknown, path: string): number =>
  typeof v === "number" ? v : fail(path, `expected number, got ${typeof v}`);

export const bool = (v: unknown, path: string): boolean =>
  typeof v === "boolean" ? v : fail(path, `expected boolean, got ${typeof v}`);

export const obj = (v: unknown, path: string): Record<string, unknown> =>
  isObj(v)
    ? v
    : fail(path, `expected object, got ${Array.isArray(v) ? "array" : typeof v}`);

export const arr = (v: unknown, path: string): unknown[] =>
  Array.isArray(v) ? v : fail(path, `expected array, got ${typeof v}`);

export const opt = <T>(v: unknown, fn: (v: unknown) => T): T | undefined =>
  v === undefined ? undefined : fn(v);

export const oneOf = <T extends string>(
  v: unknown,
  allowed: readonly T[],
  path: string,
): T =>
  typeof v === "string" && (allowed as readonly string[]).includes(v)
    ? (v as T)
    : fail(path, `expected one of ${allowed.join("|")}, got ${JSON.stringify(v)}`);

const stateValue = (v: unknown, path: string): StateValue =>
  typeof v === "string" || typeof v === "number"
    ? v
    : fail(path, `expected string|number, got ${typeof v}`);

export const stateMap = (
  v: unknown,
  path: string,
): Record<string, StateValue> => {
  const o = obj(v, path);
  const out: Record<string, StateValue> = {};
  for (const k of Object.keys(o)) out[k] = stateValue(o[k], `${path}.${k}`);
  return out;
};

export const numMap = (
  v: unknown,
  path: string,
): Record<string, number> => {
  const o = obj(v, path);
  const out: Record<string, number> = {};
  for (const k of Object.keys(o)) out[k] = num(o[k], `${path}.${k}`);
  return out;
};
