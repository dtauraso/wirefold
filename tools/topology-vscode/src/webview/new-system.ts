// new-system.ts — the ONE master switch for the agnostic-content-buffer new path.
//
// When ON, the ENTIRE new system is active together: the binary buffer render
// (USE_BUFFER_RENDER), raw-input forwarding to Go's gesture FSM (USE_RAW_INPUT),
// and all the new Go-owned interactions (edge-create, click-select, handhold orbit,
// ring-move). When OFF (the default), the editor runs byte-for-byte its current
// render + interaction path and this whole path is inert.
//
// RUNTIME toggle — no source edit / rebuild required. The extension reads the
// `wirefold.newSystem` VS Code setting at webview-HTML build time and injects it as
// a global; this module reads that global. David flips the setting and runs
// "Developer: Reload Window" (the same reload the two-process editor already needs
// for extension-host changes) to switch the whole system on or off.
//
// Falls back to false when the global is absent (e.g. unit tests, or an older HTML).

declare global {
  interface Window {
    __WIREFOLD_NEW_SYSTEM__?: boolean;
  }
}

/** Master switch: true = the entire new (buffer + raw-input + new-interaction) path. */
export const USE_NEW_SYSTEM: boolean =
  typeof window !== "undefined" && window.__WIREFOLD_NEW_SYSTEM__ === true;
