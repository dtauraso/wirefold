# Model Revised Draft — Diagrams

Illustrates the substrate model described in `MODEL-revised-draft.md`. All files are additive; `MODEL.md` and existing diagrams are unchanged.

---

**07-q2-firing-rule-and-slot-ownership.svg** — Resolution diagram for Q2. Four destination nodes (ReadGate with 3 slots, AndGate, StreakDetector, EdgeNode/XOR — each with 2 slots) shown with their incoming wires arriving at the node boundary, each wire labeled `→s_k` to indicate its construction-time-bound slot. Slots are drawn as passive cells inside the node, and each node's header carries its rule (e.g. "fire when s0 ∧ s1 ∧ s2"). A resolution panel notes that the original "how does the firing rule subscribe?" framing dissolves under the corrected model: slots don't notify anyone; the node receives the wire's arrival, sees the bound slot id, writes that slot, then re-evaluates its rule. Replaces and merges the previous 07 (A vs B subscription comparison) and a transient 15 (multi-node example). Directly corresponds to `MODEL-revised-draft.md § Open questions → Q2`.
