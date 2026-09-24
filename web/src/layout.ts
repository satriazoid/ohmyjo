/**
 * Pane layout model: a binary split tree per tab.
 *
 * A tree (rather than a flat grid) is what makes nested splits and
 * drag-resizable ratios expressible, and it serialises 1:1 into the config's
 * `layout` field so the workspace can be restored on the next launch.
 */

export type PaneID = string;

/** `row` places children side by side (a vertical splitter); `column` stacks them. */
export type SplitDir = "row" | "column";

export interface LeafNode {
  kind: "leaf";
  pane: PaneID;
}

export interface SplitNode {
  kind: "split";
  id: string;
  dir: SplitDir;
  /** Share of the axis given to `first`, clamped to 0.15..0.85. */
  ratio: number;
  first: Node;
  second: Node;
}

export type Node = LeafNode | SplitNode;

export interface Pane {
  id: PaneID;
  profile: string;
  cwd: string;
  title: string;
  sessionId?: string;
  status: "starting" | "running" | "exited";
  exitCode?: number;
  /** Set when the shell could not be started, for display in the pane. */
  error?: string;
}

export interface Tab {
  id: string;
  title: string;
  root: Node;
  panes: Record<PaneID, Pane>;
  /** Set once the user renames the tab, so a shell title cannot overwrite it. */
  renamed?: boolean;
}

/** A pane whose shell process is still alive. */
export function hasRunningPane(tab: Tab): boolean {
  return Object.values(tab.panes).some((p) => p.status === "running");
}

const MIN_RATIO = 0.15;
const MAX_RATIO = 0.85;

export function collectPanes(node: Node): PaneID[] {
  if (node.kind === "leaf") return [node.pane];
  return [...collectPanes(node.first), ...collectPanes(node.second)];
}

export function firstPane(node: Node): PaneID {
  let cur = node;
  while (cur.kind === "split") cur = cur.first;
  return cur.pane;
}

/** Splits `target`, inserting a new pane on the leading or trailing side. */
export function splitNode(node: Node, target: PaneID, dir: SplitDir, pane: PaneID, before: boolean, splitId: string): Node {
  if (node.kind === "leaf") {
    if (node.pane !== target) return node;
    const leaf: LeafNode = { kind: "leaf", pane };
    const existing: LeafNode = { kind: "leaf", pane: target };
    return {
      kind: "split",
      id: splitId,
      dir,
      ratio: 0.5,
      first: before ? leaf : existing,
      second: before ? existing : leaf,
    };
  }
  return {
    ...node,
    first: splitNode(node.first, target, dir, pane, before, splitId),
    second: splitNode(node.second, target, dir, pane, before, splitId),
  };
}

/**
 * Removes a leaf and collapses its parent, promoting the surviving sibling so
 * the tree never keeps a single-child split node.
 */
export function removeNode(node: Node, target: PaneID): Node | null {
  if (node.kind === "leaf") return node.pane === target ? null : node;
  const first = removeNode(node.first, target);
  const second = removeNode(node.second, target);
  if (!first) return second;
  if (!second) return first;
  return { ...node, first, second };
}

export function setRatio(node: Node, splitId: string, ratio: number): Node {
  if (node.kind === "leaf") return node;
  if (node.id === splitId) {
    return { ...node, ratio: Math.min(MAX_RATIO, Math.max(MIN_RATIO, ratio)) };
  }
  return {
    ...node,
    first: setRatio(node.first, splitId, ratio),
    second: setRatio(node.second, splitId, ratio),
  };
}

/** The chain of split ids from the root down to a pane, for focus navigation. */
export function pathToPane(node: Node, target: PaneID): SplitNode[] | null {
  if (node.kind === "leaf") return node.pane === target ? [] : null;
  const inFirst = pathToPane(node.first, target);
  if (inFirst) return [node, ...inFirst];
  const inSecond = pathToPane(node.second, target);
  if (inSecond) return [node, ...inSecond];
  return null;
}

/** Next pane in visual order, wrapping; used by the focus keybindings. */
export function siblingPane(node: Node, current: PaneID, delta: number): PaneID {
  const order = collectPanes(node);
  const idx = order.indexOf(current);
  if (idx < 0) return order[0] ?? current;
  const next = (idx + delta + order.length) % order.length;
  return order[next];
}

/** Directional focus: adjacent pane along the given axis, falling back to order. */
export function paneInDirection(node: Node, current: PaneID, dir: "left" | "right" | "up" | "down"): PaneID {
  const chain = pathToPane(node, current);
  if (!chain) return current;
  const axis: SplitDir = dir === "left" || dir === "right" ? "row" : "column";
  const wantsFirst = dir === "left" || dir === "up";
  // Walk up until we find an ancestor split on the wanted axis where we sit on
  // the opposite side, then descend into the nearest leaf there.
  for (let i = chain.length - 1; i >= 0; i--) {
    const split = chain[i];
    if (split.dir !== axis) continue;
    const inFirst = pathToPane(split.first, current) !== null;
    if (inFirst === !wantsFirst) continue;
    return firstPane(wantsFirst ? split.first : split.second);
  }
  return current;
}
