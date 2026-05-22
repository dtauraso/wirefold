// Pure router: subscribes to host→webview messages, posts {type:"ready"}
// once on install, and dispatches typed messages to injected handlers.
// Extracted so contracts.md C1/C3 can assert behavior without a DOM.

import { parseHostToWebview } from "../../../messages";
import { postLog } from "../../log/post";
import type { TraceEvent, WebviewToHostMsg } from "../../../messages";

export type HostMessageHandlers = {
  load: (text: string) => void;
  viewLoad: (text: string | undefined) => void;
  traceEvent?: (event: TraceEvent) => void;
};

export type RouterDeps = {
  addEventListener: (
    type: "message",
    handler: (e: MessageEvent<unknown>) => void,
  ) => void;
  removeEventListener: (
    type: "message",
    handler: (e: MessageEvent<unknown>) => void,
  ) => void;
  postMessage: (msg: WebviewToHostMsg) => void;
};

export function installHostMessageRouter(
  deps: RouterDeps,
  handlers: HostMessageHandlers,
): () => void {
  const handler = (e: MessageEvent<unknown>) => {
    const msg = parseHostToWebview(e.data);
    if (!msg) return;
    if (msg.type === "load") handlers.load(msg.text);
    else if (msg.type === "view-load") handlers.viewLoad(msg.text);
    else if (msg.type === "trace-event") {
      const evNode = 'node' in msg.event ? msg.event.node : msg.event.nodeId;
      console.log(`[webview-msg] trace-event step=${msg.event.step} kind=${msg.event.kind} node=${evNode} port=${msg.event.port ?? "-"}`);
      postLog("phase4.webview-msg", { layer: "webview-msg", step: msg.event.step, kind: msg.event.kind, node: evNode, port: msg.event.port ?? null });
      handlers.traceEvent?.(msg.event);
    }
  };
  deps.addEventListener("message", handler);
  deps.postMessage({ type: "ready" });
  return () => deps.removeEventListener("message", handler);
}
