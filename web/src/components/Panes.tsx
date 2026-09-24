import { useCallback, useRef } from "react";
import type { Node } from "../layout";
import { useLocatedPane } from "../selectors";
import { useApp } from "../store";
import { refitAll } from "../terminals";
import { TerminalView } from "./Terminal";

/**
 * Renders a split tree. Ratios become flex-grow values, so a drag only touches
 * two inline styles; the terminals reflow through their ResizeObserver and the
 * backend receives the final geometry once per animation frame.
 *
 * `maximize` names a pane that fills the stage. It is done purely in CSS (the
 * stage flattens the split nodes with `display: contents` and hides the other
 * panes) rather than by re-rendering the tree: unmounting a pane would dispose
 * its terminal, and re-parenting would resize every PTY twice for a visual-only
 * change. A hidden pane measures zero, so its terminal skips the refit.
 */
export function PaneTree({
  node,
  activePane,
  paneCount,
  maximize = "",
}: {
  node: Node;
  activePane: string;
  paneCount: number;
  maximize?: string;
}) {
  if (node.kind === "leaf") {
    return (
      <PaneSlot
        paneId={node.pane}
        activePane={activePane}
        paneCount={paneCount}
        maximized={node.pane === maximize}
      />
    );
  }
  return <SplitView node={node} activePane={activePane} paneCount={paneCount} maximize={maximize} />;
}

function PaneSlot({
  paneId,
  activePane,
  paneCount,
  maximized,
}: {
  paneId: string;
  activePane: string;
  paneCount: number;
  maximized: boolean;
}) {
  const located = useLocatedPane(paneId);
  const profileName = useApp((s) => s.profiles.find((p) => p.id === located?.pane.profile)?.name);
  const restartPane = useApp((s) => s.restartPane);
  const maximizePane = useApp((s) => s.maximizePane);

  if (!located) return null;
  const { pane } = located;
  const active = paneId === activePane;
  // A maximized stage hides every other pane; they stay mounted so their
  // terminals keep their scrollback and their shells keep running.
  const hidden = maximizePane !== "" && !maximized;

  return (
    <div
      className={`pane${active ? " pane-active" : ""}${paneCount > 1 ? " pane-split" : ""}${maximized ? " pane-maximized" : ""}${hidden ? " pane-hidden" : ""}`}
    >
      {pane.error ? (
        <div className="pane-error">
          <strong>Could not start {profileName ?? pane.profile}</strong>
          <p>{pane.error}</p>
          <button type="button" onClick={() => restartPane(paneId)}>
            Try again
          </button>
        </div>
      ) : (
        <TerminalView pane={pane} active={active} />
      )}
      {/* No pane header: the shell name is already on the tab, and a per-pane
          title bar made the app look like a widget toolkit. The only state the
          header carried that is not visible elsewhere is a dead session, so a
          non-interactive chip reports it without stealing terminal clicks. */}
      {pane.status !== "running" && !pane.error && (
        <div className={`pane-status status-${pane.status}`}>
          {pane.status === "exited"
            ? `exited${pane.exitCode ? ` (${pane.exitCode})` : ""}`
            : "starting…"}
        </div>
      )}
    </div>
  );
}

function SplitView({
  node,
  activePane,
  paneCount,
  maximize,
}: {
  node: Extract<Node, { kind: "split" }>;
  activePane: string;
  paneCount: number;
  maximize: string;
}) {
  const setRatio = useApp((s) => s.setRatio);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const draggingRef = useRef(false);

  const onPointerDown = useCallback((ev: React.PointerEvent<HTMLDivElement>) => {
    // No preventDefault here: it would suppress the compatibility mouse events
    // and with them the double-click that resets the split. CSS handles the
    // text selection and drag ghosting instead.
    draggingRef.current = true;
    ev.currentTarget.setPointerCapture(ev.pointerId);
  }, []);

  const onPointerMove = useCallback(
    (ev: React.PointerEvent<HTMLDivElement>) => {
      if (!draggingRef.current) return;
      const box = containerRef.current?.getBoundingClientRect();
      if (!box) return;
      const ratio =
        node.dir === "row"
          ? (ev.clientX - box.left) / Math.max(1, box.width)
          : (ev.clientY - box.top) / Math.max(1, box.height);
      setRatio(node.id, ratio);
    },
    [node.dir, node.id, setRatio],
  );

  const stopDrag = useCallback((ev: React.PointerEvent<HTMLDivElement>) => {
    if (!draggingRef.current) return;
    draggingRef.current = false;
    ev.currentTarget.releasePointerCapture(ev.pointerId);
    // Terminals only relayout on their own observer callback; nudging here
    // removes a visible one-frame lag when a drag stops.
    refitAll();
  }, []);

  // flex-grow keeps the two children filling the box exactly, so rounding can
  // never leave a seam or an overflow.
  const firstStyle = { flexGrow: node.ratio, flexBasis: 0 };
  const secondStyle = { flexGrow: 1 - node.ratio, flexBasis: 0 };

  return (
    <div className={`split split-${node.dir}${maximize ? " split-maximizing" : ""}`} ref={containerRef}>
      <div className="split-child" style={firstStyle}>
        <PaneTree node={node.first} activePane={activePane} paneCount={paneCount} maximize={maximize} />
      </div>
      <div
        className={`splitter splitter-${node.dir}`}
        role="separator"
        aria-orientation={node.dir === "row" ? "vertical" : "horizontal"}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={stopDrag}
        onPointerCancel={stopDrag}
        onDoubleClick={() => setRatio(node.id, 0.5)}
      />
      <div className="split-child" style={secondStyle}>
        <PaneTree node={node.second} activePane={activePane} paneCount={paneCount} maximize={maximize} />
      </div>
    </div>
  );
}
