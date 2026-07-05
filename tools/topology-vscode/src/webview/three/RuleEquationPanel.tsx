// RuleEquationPanel.tsx — symbolic DOM readout of the polar equation the rule-builder is
// currently authoring (gesture.go trySelectSphereRule), PLUS the typed "+ Add equation" entry
// (keyboard authoring on top of the SAME Go-owned builder). Typing REPLACES clicking — but
// unlike a click session it is NOT a separate text box next to a preview: the user types
// DIRECTLY INTO the equation's `_` blanks, rendered in the same `( node , angle )` parens
// notation renderBuilder uses. The active blank holds an inline <input>; resolved blanks show
// their value (a node's label or an angle chip); blanks not yet reached show `_`. Filling is
// strictly left-to-right; each blank's resolution auto-advances to the next blank. The LAST
// blank of an equation (NN_COMP_B / PT_TORUS_NODE) does NOT auto-commit — it holds its parsed
// value for live preview only; pressing ENTER is the explicit commit that fires Go's builder
// action and closes the typed session (see onBlankEnter). Render + forward only: all in-progress
// form state (active blank
// index, current text, the buffered node/comp of the term in progress) is plain local
// useState, never sent to Go until a blank resolves.

import { createPortal } from "react-dom";
import { useState, useEffect, type KeyboardEvent } from "react";
import { useOverlayFlags } from "./overlay-flags";
import {
  useRuleBuilder,
  usePolarLocks,
  type RuleBuilderTerm,
  type RuleBuilderState,
  type PolarLockEntry,
  POLAR_LOCK_KIND_NODE_NODE,
  POLAR_LOCK_KIND_PORT_TORUS,
} from "./rule-builder";
import { postGoRecord } from "../vscode-api";
import {
  encodeClearRule,
  encodeLockToggleActive,
  encodeLockSelect,
  encodeDeleteSelectedLock,
  encodeAuthorBegin,
  encodeAuthorNode,
  encodeAuthorLatch,
  encodeAuthorPort,
  encodeAuthorTorus,
  encodePreviewPort,
} from "../../schema/input-layout";
import { getLatestSnapshot } from "../snapshot-buffer";
import { decodeSnapshot, nodeLabel } from "./buffer-decode";
import {
  parseCompInput,
  compLivePreview,
  resolveNodeRowByLabel,
  listPortsForNode,
  type PortOption,
} from "./equation-form";
import { usePortAutocompleteContext } from "./port-autocomplete-context";

/** Angle-chip glyphs for the packed term code (matches gesture.go's ruleTermCode: 0=θ,
 *  1=φ, 2=−θ, 3=−φ, 4=r — positive θ/φ show no sign). */
const ANGLE_CHIPS = ["θ", "φ", "−θ", "−φ", "r"];

function angleChip(code: number): string {
  return ANGLE_CHIPS[code] ?? "?";
}

// ── Typed "+ Add equation" entry — fill-in-the-blank on the equation itself ────────────────
//
// Typing REPLACES clicking; each blank's resolved token fires the matching Author* wire action
// (fire-and-forget), exactly like a click. Blank order for node=node is
// center, nodeA, compA, nodeB, compB (`active` 0..4); Go's builder latches comp-then-node per
// term, so this form BUFFERS the term's node blank locally (pendingNodeRow/pendingNodeLabel)
// until the following comp blank resolves, then sends encodeAuthorLatch(comp,sign) followed by
// encodeAuthorNode(nodeRow) in that order — center is a lone AuthorNode with nothing buffered.
// Blank order for port∈torus is portNode, portName (autocomplete), torusNode (`active` 0..2);
// torusNode is the final blank — its delimiter no longer auto-commits, ENTER commits the pair
// and closes the typed session (see onBlankEnter).

const NN_CENTER = 0;
const NN_NODE_A = 1;
const NN_COMP_A = 2;
const NN_NODE_B = 3;
const NN_COMP_B = 4;

const PT_PORT_NODE = 0;
const PT_PORT_NAME = 1;
const PT_TORUS_NODE = 2;

interface TypedFormState {
  kind: number; // POLAR_LOCK_KIND_NODE_NODE | POLAR_LOCK_KIND_PORT_TORUS
  active: number; // blank index — semantics depend on kind (NN_* / PT_*)
  text: string;
  // node=node: the node blank of the term currently in progress, buffered until its
  // following comp blank resolves (Go latches comp-then-node, the form fills node-then-comp).
  pendingNodeRow: number;
  pendingNodeLabel: string;
  // port∈torus: the portNode blank's resolved row/label, buffered until AuthorPort is sent
  // (once the portName blank resolves) — same buffering shape as node=node's pendingNode*.
  portOptions: PortOption[];
  portHighlight: number;
}

function beginForm(kind: number): TypedFormState {
  return {
    kind,
    active: 0,
    text: "",
    pendingNodeRow: -1,
    pendingNodeLabel: "",
    portOptions: [],
    portHighlight: 0,
  };
}

function filteredPortOptions(f: TypedFormState): PortOption[] {
  const t = f.text.trim().toLowerCase();
  if (!t) return f.portOptions;
  return f.portOptions.filter((o) => o.name.toLowerCase().includes(t));
}

export function RuleEquationPanel() {
  const overlays = useOverlayFlags();
  const rb = useRuleBuilder();
  const { equations } = usePolarLocks();
  const mount = document.getElementById("rule-eq-mount");
  const portAutoCtx = usePortAutocompleteContext();

  // Typed-equation entry state — LOCAL, transient input only (not sent to Go until a token
  // resolves; cleared immediately after that token's action is sent). addingKind is the brief
  // "choose a kind" step before a session exists.
  const [addingKind, setAddingKind] = useState(false);
  const [form, setForm] = useState<TypedFormState | null>(null);
  const [collapsed, setCollapsed] = useState(false);
  const [centerText, setCenterText] = useState("");
  const [editingCenter, setEditingCenter] = useState(false);

  /** The panel's persistent Center field: type a node name to set the authoring center when
   *  none is selected. Resolves on any non-number char (like the node blanks); sends AuthorNode
   *  with nothing pending, which Go turns into `md.ruleCenter = node`. */
  function resolveCenterText(text: string) {
    if (!/\D/.test(text)) {
      setCenterText(text);
      return;
    }
    const typed = text.replace(/\D.*$/, "");
    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    const row = decoded ? resolveNodeRowByLabel(decoded, typed) : -1;
    if (row < 0) {
      setCenterText(typed);
      return;
    }
    postGoRecord(encodeAuthorNode(row));
    setCenterText("");
    setEditingCenter(false);
  }
  function onCenterKey(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Escape") {
      setCenterText("");
      setEditingCenter(false);
      return;
    }
    if (e.key !== "Enter") return;
    e.preventDefault();
    const typed = centerText.trim();
    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    const row = decoded ? resolveNodeRowByLabel(decoded, typed) : -1;
    if (row < 0) return;
    postGoRecord(encodeAuthorNode(row));
    setCenterText("");
    setEditingCenter(false);
  }

  function onPickKind(kind: number) {
    postGoRecord(encodeAuthorBegin(kind));
    const f = beginForm(kind);
    // The center blank is always typable. If a node is already the authoring center
    // (selected when the panel came up), PRE-FILL it as a default — the user can accept
    // it (Enter) or type a different node in its place.
    if (kind === POLAR_LOCK_KIND_NODE_NODE && rb && rb.centerRow >= 0 && rb.centerLabel) {
      f.text = rb.centerLabel;
    }
    setForm(f);
    setAddingKind(false);
  }

  function cancelForm() {
    postGoRecord(encodeClearRule());
    portAutoCtx.setValue(null);
    setForm(null);
    setAddingKind(false);
  }

  /** node=node kind, a NODE blank (center/nodeA/nodeB): resolves on an exact label match.
   *  Center sends AuthorNode alone; nodeA/nodeB buffer locally (Go latches comp-then-node,
   *  the form fills node-then-comp) until the following comp blank resolves them. */
  function onNodeBlankChange(text: string) {
    if (!form) return;
    // Only resolve a node blank once a NON-NUMBER character is typed, so a multi-digit id
    // like 12 isn't committed on its first digit just because node "1" exists.
    if (!/\D/.test(text)) {
      setForm({ ...form, text });
      return;
    }
    const typed = text.replace(/\D.*$/, ""); // the number typed before the non-number char
    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    if (!decoded) {
      setForm({ ...form, text: typed });
      return;
    }
    const row = resolveNodeRowByLabel(decoded, typed);
    if (row < 0) {
      // no such node — drop the delimiter, keep the digits so the user can keep typing.
      setForm({ ...form, text: typed });
      return;
    }
    if (form.active === NN_CENTER) {
      postGoRecord(encodeAuthorNode(row));
      setForm({ ...form, active: NN_NODE_A, text: "" });
      return;
    }
    const label = nodeLabel(decoded, row);
    const next = form.active === NN_NODE_A ? NN_COMP_A : NN_COMP_B;
    setForm({ ...form, active: next, text: "", pendingNodeRow: row, pendingNodeLabel: label });
  }

  /** node=node kind, a COMP blank (compA/compB): resolves on a full comp word. compA sends
   *  the latch then the buffered node for this term, in that order (matches Go's click
   *  order), and advances to nodeB. compB is the FINAL blank — it only buffers the parsed
   *  text for live preview; ENTER (onBlankEnter) sends the latch+node and closes the form. */
  function onCompBlankChange(text: string) {
    if (!form) return;
    const parsed = parseCompInput(text);
    if (!parsed) {
      setForm({ ...form, text });
      return;
    }
    if (form.active === NN_COMP_A) {
      postGoRecord(encodeAuthorLatch(parsed.comp, parsed.sign));
      postGoRecord(encodeAuthorNode(form.pendingNodeRow));
      setForm({ ...form, active: NN_NODE_B, text: "", pendingNodeRow: -1, pendingNodeLabel: "" });
    } else {
      // NN_COMP_B: last blank — hold the parsed value for live preview only; Enter commits.
      setForm({ ...form, text });
    }
  }

  /** port∈torus kind, portNode/torusNode blanks: both type a node label. portNode resolving
   *  advances to the portName autocomplete blank. torusNode is the FINAL blank — it only
   *  buffers the resolved digits for display; ENTER (onBlankEnter) sends AuthorTorus and
   *  closes the form. */
  function onPortTorusNodeBlankChange(text: string) {
    if (!form) return;
    // Resolve once a NON-NUMBER character is typed, so multi-digit node ids aren't
    // committed on their first digit (same as the node=node node blanks).
    if (!/\D/.test(text)) {
      setForm({ ...form, text });
      return;
    }
    const typed = text.replace(/\D.*$/, "");
    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    if (!decoded) {
      setForm({ ...form, text: typed });
      return;
    }
    const row = resolveNodeRowByLabel(decoded, typed);
    if (row < 0) {
      setForm({ ...form, text: typed });
      return;
    }
    if (form.active === PT_PORT_NODE) {
      const options = listPortsForNode(decoded, row);
      const label = nodeLabel(decoded, row);
      const next: TypedFormState = {
        ...form,
        active: PT_PORT_NAME,
        text: "",
        pendingNodeRow: row,
        pendingNodeLabel: label,
        portOptions: options,
        portHighlight: 0,
      };
      if (options.length > 0) {
        const first = options[0]!;
        postGoRecord(encodePreviewPort(row, first.name, first.isInput));
        portAutoCtx.setValue({ nodeRow: row, highlightedRow: first.row });
      } else {
        portAutoCtx.setValue({ nodeRow: row, highlightedRow: -1 });
      }
      setForm(next);
      return;
    }
    // torusNode blank — last blank; hold the resolved value for display only, Enter commits.
    setForm({ ...form, text: typed });
  }

  function previewPortOption(nodeRow: number, o: PortOption) {
    postGoRecord(encodePreviewPort(nodeRow, o.name, o.isInput));
    portAutoCtx.setValue({ nodeRow, highlightedRow: o.row });
  }

  function onPortNameBlankChange(text: string) {
    if (!form) return;
    const next = { ...form, text, portHighlight: 0 };
    setForm(next);
    const opts = filteredPortOptions(next);
    if (opts.length > 0) previewPortOption(next.pendingNodeRow, opts[0]!);
    else portAutoCtx.setValue({ nodeRow: next.pendingNodeRow, highlightedRow: -1 });
  }

  function selectPortOption(f: TypedFormState, o: PortOption) {
    postGoRecord(encodeAuthorPort(f.pendingNodeRow, o.name, o.isInput));
    portAutoCtx.setValue(null);
    setForm({ ...f, active: PT_TORUS_NODE, text: "", portOptions: [], portHighlight: 0 });
  }

  function onPortNameKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (!form) return;
    const opts = filteredPortOptions(form);
    if (opts.length === 0) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      const h = (form.portHighlight + 1) % opts.length;
      setForm({ ...form, portHighlight: h });
      previewPortOption(form.pendingNodeRow, opts[h]!);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      const h = (form.portHighlight - 1 + opts.length) % opts.length;
      setForm({ ...form, portHighlight: h });
      previewPortOption(form.pendingNodeRow, opts[h]!);
    } else if (e.key === "Enter") {
      e.preventDefault();
      selectPortOption(form, opts[form.portHighlight]!);
    }
  }

  /** ENTER finishes/commits the blank currently being typed. Intermediate blanks still
   *  auto-advance on their own delimiter (non-digit / full comp word); Enter is the explicit
   *  commit for the FINAL blank of each kind (NN_COMP_B, PT_TORUS_NODE), which no longer
   *  auto-commits on that delimiter. For non-final blanks Enter just forces the same
   *  resolution their onChange already does (useful when the field holds only digits and
   *  there's no natural non-digit delimiter to type). */
  function onBlankEnter(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key !== "Enter" || !form) return;
    e.preventDefault();
    if (form.kind === POLAR_LOCK_KIND_NODE_NODE) {
      if (form.active === NN_CENTER || form.active === NN_NODE_A || form.active === NN_NODE_B) {
        onNodeBlankChange(form.text + " ");
        return;
      }
      const parsed = parseCompInput(form.text);
      if (!parsed) return;
      postGoRecord(encodeAuthorLatch(parsed.comp, parsed.sign));
      postGoRecord(encodeAuthorNode(form.pendingNodeRow));
      if (form.active === NN_COMP_A) {
        setForm({ ...form, active: NN_NODE_B, text: "", pendingNodeRow: -1, pendingNodeLabel: "" });
      } else {
        // NN_COMP_B — finish and close.
        setForm(null);
      }
      return;
    }
    // port ∈ torus
    if (form.active === PT_PORT_NODE) {
      onPortTorusNodeBlankChange(form.text + " ");
      return;
    }
    if (form.active === PT_PORT_NAME) {
      const opts = filteredPortOptions(form);
      if (opts.length === 0) return;
      selectPortOption(form, opts[form.portHighlight]!);
      return;
    }
    // PT_TORUS_NODE — finish and close (no looping back to author another pair).
    const typed = form.text.trim();
    if (!typed) return;
    const snap = getLatestSnapshot();
    const decoded = snap ? decodeSnapshot(snap) : null;
    if (!decoded) return;
    const row = resolveNodeRowByLabel(decoded, typed);
    if (row < 0) return;
    postGoRecord(encodeAuthorTorus(row));
    setForm(null);
  }

  /** Renders an inline `<input>` sitting where a blank's value would otherwise be printed —
   *  same rule-eq-node/rule-eq-angle classes as the resolved/awaiting spans, so the active
   *  blank visually matches the rest of the equation. */
  function renderBlankInput(
    text: string,
    placeholder: string,
    cls: string,
    onChange: (t: string) => void,
    onKeyDown?: (e: KeyboardEvent<HTMLInputElement>) => void,
  ) {
    return (
      <input
        autoFocus
        className={"rule-eq-blank-input " + cls}
        size={Math.max(placeholder.length, text.length, 2)}
        value={text}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={onKeyDown}
      />
    );
  }

  /** A dim placeholder shown in a blank that isn't filled yet (and isn't the active input),
   *  so every field advertises what it expects (node… / θ/φ/r / port… / torus…) at all times. */
  const hint = (t: string) => <span className="rule-eq-hint">{t}</span>;

  /** Renders the node=node typed session as the equation itself, in parens notation:
   *  `Center: [center]` then `( [nodeA] , [compA] ) = ( [nodeB] , [compB] )`. Resolved blanks
   *  come from rb (the same Go-streamed state a click drives); the blank currently being typed
   *  is an inline input; a term's node blank shows its buffered (not-yet-sent) value once the
   *  form has moved on to that term's comp blank; unreached blanks show `_`. */
  function renderTypedNodeNode(f: TypedFormState, rb: RuleBuilderState | null) {
    const centerLabel =
      f.active === NN_CENTER
        ? renderBlankInput(f.text, "node…", "rule-eq-node", onNodeBlankChange, onBlankEnter)
        : rb?.centerLabel || hint("node…");
    const termA = rb?.terms[0] ?? null;
    const termB = rb?.terms[1] ?? null;
    const nodeACell =
      f.active === NN_NODE_A
        ? renderBlankInput(f.text, "node…", "rule-eq-node", onNodeBlankChange, onBlankEnter)
        : (termA?.label ?? (f.active > NN_NODE_A ? f.pendingNodeLabel : hint("node…")));
    const compACell =
      f.active === NN_COMP_A
        ? renderBlankInput(compLivePreview(f.text), "θ/φ/r", "rule-eq-angle", onCompBlankChange, onBlankEnter)
        : (termA != null ? angleChip(termA.code) : hint("θ/φ/r"));
    const nodeBCell =
      f.active === NN_NODE_B
        ? renderBlankInput(f.text, "node…", "rule-eq-node", onNodeBlankChange, onBlankEnter)
        : (termB?.label ?? (f.active > NN_NODE_B ? f.pendingNodeLabel : hint("node…")));
    const compBCell =
      f.active === NN_COMP_B
        ? renderBlankInput(compLivePreview(f.text), "θ/φ/r", "rule-eq-angle", onCompBlankChange, onBlankEnter)
        : (termB != null ? angleChip(termB.code) : hint("θ/φ/r"));
    return (
      <>
        <div className="rule-eq-center">Center: {centerLabel}</div>
        <div className="rule-eq-equation">
          <span className="rule-eq-term">
            (<span className="rule-eq-node">{nodeACell}</span>,<span className="rule-eq-angle">{compACell}</span>)
          </span>
          <span className="rule-eq-op"> = </span>
          <span className="rule-eq-term">
            (<span className="rule-eq-node">{nodeBCell}</span>,<span className="rule-eq-angle">{compBCell}</span>)
          </span>
        </div>
      </>
    );
  }

  /** Renders the port∈torus typed session as the equation itself, in the same notation
   *  renderPortTorus uses: `( [portNode] , [portName] ) ∈ ◯ [torusNode]`. The portName blank
   *  keeps its option-list autocomplete under the input. */
  function renderTypedPortTorus(f: TypedFormState, rb: RuleBuilderState | null) {
    const portNodeCell =
      f.active === PT_PORT_NODE
        ? renderBlankInput(f.text, "node…", "rule-eq-node", onPortTorusNodeBlankChange, onBlankEnter)
        : (rb?.pendingPort?.nodeLabel ?? f.pendingNodeLabel) || hint("node…");
    const opts = filteredPortOptions(f);
    const portNameCell =
      f.active === PT_PORT_NAME ? (
        <span className="rule-eq-typed-input-wrap">
          <input
            autoFocus
            className="rule-eq-blank-input rule-eq-angle"
            size={Math.max(4, f.text.length)}
            value={f.text}
            placeholder="port…"
            onChange={(e) => onPortNameBlankChange(e.target.value)}
            onKeyDown={onPortNameKeyDown}
          />
          <span className="rule-eq-form-autocomplete">
            {opts.map((o, oi) => (
              <span
                key={o.row}
                className={"rule-eq-form-option" + (oi === f.portHighlight ? " rule-eq-form-option--hl" : "")}
                onMouseDown={(e) => {
                  e.preventDefault();
                  selectPortOption(f, o);
                }}
              >
                {o.isInput ? "in" : "out"}:{o.name}
              </span>
            ))}
          </span>
        </span>
      ) : rb?.pendingPort ? (
        `${rb.pendingPort.isInput ? "in" : "out"}:${rb.pendingPort.label}`
      ) : (
        hint("port…")
      );
    const torusNodeCell =
      f.active === PT_TORUS_NODE
        ? renderBlankInput(f.text, "torus…", "rule-eq-node", onPortTorusNodeBlankChange, onBlankEnter)
        : rb?.pendingTorus?.label || hint("torus…");
    return (
      <div className="rule-eq-equation">
        <span className="rule-eq-term">
          (<span className="rule-eq-node">{portNodeCell}</span>,<span className="rule-eq-angle">{portNameCell}</span>)
          {" ∈ ◯ "}
          <span className="rule-eq-node">{torusNodeCell}</span>
        </span>
      </div>
    );
  }

  // The committed-equations LIST keys off the rule-builder's STICKY panel Center
  // (rb.centerRow, gesture.go md.ruleCenter) rather than the transient click highlight
  // (Node.Selected / useSelectedNodeRow): it shows whenever the sticky center participates
  // in >=1 committed equation, as ANY participant (center, term A, term B, the port's
  // owning node, or the torus). This keeps the panel showing the last-selected node's
  // equations even after an empty-space click clears the highlight ring. The in-progress
  // builder section stays gated on the overlay, as before.
  const centerRow = rb?.centerRow;
  const rowEquations = equations.filter((eq) =>
    eq.kind === POLAR_LOCK_KIND_PORT_TORUS
      ? eq.torusRow === centerRow || eq.portNodeRow === centerRow
      : eq.centerRow === centerRow || eq.a.row === centerRow || eq.b.row === centerRow,
  );
  // A typed session (form != null) owns the in-progress display exclusively — the click
  // builder's renderBuilder/renderPortTorusBuilder is suppressed while typing so the two
  // never render at once.
  const builderHasContent =
    !!rb &&
    (rb.centerRow >= 0 ||
      rb.pending != null ||
      rb.terms.length > 0 ||
      rb.pendingPort != null ||
      rb.pendingTorus != null);
  const showBuilder = !!overlays?.selSpherePoles && builderHasContent && !form;
  const showList = rowEquations.length > 0;

  // Delete key: fires whenever at least one of THIS center's SELECTED rows is deactivated
  // (multi-select — Go deletes every selected+deactivated lock). Go re-guards regardless.
  // Listens while the list is showing.
  useEffect(() => {
    if (!showList) return;
    const onKeyDown = (e: globalThis.KeyboardEvent) => {
      if (e.key !== "Delete" && e.key !== "Backspace") return;
      const hasDeletable = rowEquations.some((eq) => eq.selected && !eq.active);
      if (!hasDeletable) return;
      postGoRecord(encodeDeleteSelectedLock());
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [showList, rowEquations]);

  if (!mount) return null;
  // The equations panel is a STANDALONE panel — NOT gated on the polar-sphere overlay. It is
  // always available (with the hide/show toggle) so you can set a center and add equations by
  // typing, whether or not the overlay is on.

  return createPortal(
    <div className="rule-eq-panel">
      <button
        className="rule-eq-toggle"
        onClick={() => setCollapsed((c) => !c)}
        title={collapsed ? "Show equations panel" : "Hide equations panel"}
      >
        {collapsed ? "▸ equations" : "▾ hide"}
      </button>
      {!collapsed && (
        <>
      {!form && (
        <div className="rule-eq-center">
          Center:{" "}
          {rb && rb.centerRow >= 0 && !editingCenter ? (
            <span
              className="rule-eq-center-value"
              title="Click to change the center"
              onClick={() => {
                setCenterText(rb.centerLabel);
                setEditingCenter(true);
              }}
            >
              {rb.centerLabel}
            </span>
          ) : (
            <input
              className="rule-eq-blank-input rule-eq-node"
              autoFocus={editingCenter}
              placeholder="node…"
              size={Math.max(5, centerText.length)}
              value={centerText}
              onFocus={(e) => e.target.select()}
              onChange={(e) => resolveCenterText(e.target.value)}
              onKeyDown={onCenterKey}
              onBlur={() => setEditingCenter(false)}
            />
          )}
        </div>
      )}
      <div className="rule-eq-add-row">
        {!addingKind && !form && (
          <button className="rule-eq-add-btn" onClick={() => setAddingKind(true)}>
            +
          </button>
        )}
        {addingKind && !form && (
          <div className="rule-eq-kind-menu">
            <span className="rule-eq-kind-option" onClick={() => onPickKind(POLAR_LOCK_KIND_NODE_NODE)}>
              node = node
            </span>
            <span className="rule-eq-kind-option" onClick={() => onPickKind(POLAR_LOCK_KIND_PORT_TORUS)}>
              port ∈ torus
            </span>
          </div>
        )}
      </div>
      {form && (
        <>
          {form.kind === POLAR_LOCK_KIND_PORT_TORUS ? renderTypedPortTorus(form, rb) : renderTypedNodeNode(form, rb)}
          <button className="rule-eq-clear" onClick={cancelForm}>
            Cancel
          </button>
        </>
      )}
      {showBuilder && rb && renderBuilder(rb)}
      {showList && (
        <div className="rule-eq-list">
          {rowEquations.map((eq) => renderLockRow(eq, eq.selected))}
        </div>
      )}
        </>
      )}
    </div>,
    mount,
  );
}

/** Renders the in-progress equation-being-authored section (the selSpherePoles session).
 *  A `port ∈ torus` authoring capture (rb.pendingPort/rb.pendingTorus) is INDEPENDENT of
 *  the node/node pending term above — if either side is picked, render the port∈torus
 *  in-progress form instead of the node/node builder preview. */
function renderBuilder(rb: RuleBuilderState) {
  if (rb.pendingPort != null || rb.pendingTorus != null) {
    return renderPortTorusBuilder(rb);
  }
  // Left term = the first completed term, or (when none completed yet) the pending
  // half-term itself — "show the handhold being selected" before any node is picked.
  const leftTerm = rb.terms[0] ?? null;
  const rightTerm = rb.terms[1] ?? null;
  // The pending half-term slots in wherever a term is still missing: after the left term
  // (awaiting the second handhold) or as the left term itself (nothing completed yet).
  const pendingSlot: "left" | "right" | null =
    rb.pending == null ? null : leftTerm == null ? "left" : rightTerm == null ? "right" : null;

  // The clear button is armed only when there is an in-progress equation to discard (a
  // pending half-term or at least one completed term). Go owns the state; the button just
  // sends the bare clear command (fire-and-forget).
  const hasInProgress = rb.pending != null || rb.terms.length > 0;

  return (
    <>
      <div className="rule-eq-equation">
        {renderTerm(leftTerm, pendingSlot === "left" ? rb.pending!.code : null)}
        {(rightTerm != null || pendingSlot === "right") && (
          <>
            <span className="rule-eq-op"> = </span>
            {renderTerm(rightTerm, pendingSlot === "right" ? rb.pending!.code : null)}
          </>
        )}
      </div>
      <button
        className="rule-eq-clear"
        disabled={!hasInProgress}
        title="Clear the equation being built"
        onClick={() => postGoRecord(encodeClearRule())}
      >
        Clear
      </button>
    </>
  );
}

/** Renders the in-progress `port ∈ torus` authoring capture: whichever side has been
 *  picked (port or torus) shows its label; the other side shows the same `_` placeholder
 *  style as the node/node pending-term preview (renderTerm's awaiting slot). Mirrors
 *  renderPortTorus's committed syntax so the preview reads identically once it commits. */
function renderPortTorusBuilder(rb: RuleBuilderState) {
  const portSide = rb.pendingPort ? (rb.pendingPort.isInput ? "in" : "out") : null;
  const hasInProgress = rb.pendingPort != null || rb.pendingTorus != null;
  return (
    <>
      <div className="rule-eq-equation">
        <span className="rule-eq-term rule-eq-term--pending">
          (
          <span className="rule-eq-node">{rb.pendingPort ? rb.pendingPort.nodeLabel : "_"}</span>
          ,
          <span className="rule-eq-angle">{rb.pendingPort ? `${portSide}:${rb.pendingPort.label}` : "_"}</span>
          ) ∈ ◯
          <span className="rule-eq-node">{rb.pendingTorus ? rb.pendingTorus.label : "_"}</span>
        </span>
      </div>
      <button
        className="rule-eq-clear"
        disabled={!hasInProgress}
        title="Clear the equation being built"
        onClick={() => postGoRecord(encodeClearRule())}
      >
        Clear
      </button>
    </>
  );
}

/** Renders one committed polar-equation lock row: activate/deactivate checkbox + the
 *  symbolic equation. Clicking the row (not the checkbox) focuses it (edit-update
 *  lock/selected); the checkbox toggles active (edit-update lock/active). */
function renderLockRow(eq: PolarLockEntry, selected: boolean) {
  const cls = ["rule-eq-row", selected ? "rule-eq-row--selected" : "", eq.active ? "" : "rule-eq-row--inactive"]
    .filter(Boolean)
    .join(" ");
  return (
    <div
      key={eq.index}
      className={cls}
      onClick={() => postGoRecord(encodeLockSelect(eq.index))}
    >
      <input
        type="checkbox"
        checked={eq.active}
        onClick={(e) => e.stopPropagation()}
        onChange={() => postGoRecord(encodeLockToggleActive(eq.index))}
      />
      <span className="rule-eq-equation">
        {eq.kind === POLAR_LOCK_KIND_PORT_TORUS
          ? renderPortTorus(eq)
          : (
            <>
              {renderTerm({ row: eq.a.row, label: eq.a.label, code: eq.a.code }, null)}
              <span className="rule-eq-op"> = </span>
              {renderTerm({ row: eq.b.row, label: eq.b.label, code: eq.b.code }, null)}
            </>
          )}
      </span>
    </div>
  );
}

/** Renders a `port ∈ torus` membership lock: (nodeLabel,side) ∈ ◯torusLabel. Rendered
 *  distinctly from the (node,comp)=(node,comp) tuple form above — there is no equals sign,
 *  this is a membership relation, not an equation between two terms. STAGE 1 display only
 *  (no geometric effect). */
function renderPortTorus(eq: PolarLockEntry) {
  const side = eq.portIsInput ? "in" : "out";
  return (
    <span className="rule-eq-term">
      (<span className="rule-eq-node">{eq.portNodeLabel || "?"}</span>,{side}:{eq.portLabel || "?"}) ∈ ◯
      <span className="rule-eq-node">{eq.torusLabel || "?"}</span>
    </span>
  );
}

/** Renders one term slot: a completed term (filled), a pending half-term (node slot
 *  empty, angle chip highlighted — "show the handhold being selected"), or nothing. */
function renderTerm(term: RuleBuilderTerm | null, pendingCode: number | null) {
  if (term != null) {
    return (
      <span className="rule-eq-term">
        (<span className="rule-eq-node">{term.label}</span>,
        <span className="rule-eq-angle">{angleChip(term.code)}</span>)
      </span>
    );
  }
  if (pendingCode != null) {
    return (
      <span className="rule-eq-term rule-eq-term--pending">
        (<span className="rule-eq-node rule-eq-node--awaiting">_</span>,
        <span className="rule-eq-angle rule-eq-angle--pending">{angleChip(pendingCode)}</span>)
      </span>
    );
  }
  return null;
}
