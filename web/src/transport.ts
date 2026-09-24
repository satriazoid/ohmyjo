import type { ClientMessage, ServerMessage } from "./protocol";

export type ConnectionState = "connecting" | "open" | "closed";

type MessageHandler = (msg: ServerMessage) => void;
type StateHandler = (state: ConnectionState) => void;

/**
 * Single WebSocket to the Go backend.
 *
 * The backend owns the shells, so a dropped socket never kills a process: on
 * reconnect the UI re-attaches to each session and replays its scrollback.
 * Reconnection uses capped exponential backoff because the WebView can load
 * before the loopback server is listening.
 */
export class Transport {
  private ws: WebSocket | null = null;
  private messageHandlers = new Set<MessageHandler>();
  private stateHandlers = new Set<StateHandler>();
  private state: ConnectionState = "connecting";
  private attempt = 0;
  private timer: number | null = null;
  private queue: ClientMessage[] = [];

  constructor(private url: string) {
    this.open();
  }

  onMessage(fn: MessageHandler): () => void {
    this.messageHandlers.add(fn);
    return () => this.messageHandlers.delete(fn);
  }

  onState(fn: StateHandler): () => void {
    this.stateHandlers.add(fn);
    fn(this.state);
    return () => this.stateHandlers.delete(fn);
  }

  send(msg: ClientMessage): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
      return;
    }
    // Keystrokes typed before the socket is up are worthless, but control
    // frames (hello, create, attach, close) must not be dropped.
    if (msg.type === "input" || msg.type === "resize") return;
    this.queue.push(msg);
  }

  close(): void {
    if (this.timer !== null) {
      window.clearTimeout(this.timer);
      this.timer = null;
    }
    this.ws?.close();
    this.ws = null;
    this.setState("closed");
  }

  private setState(next: ConnectionState): void {
    if (this.state === next) return;
    this.state = next;
    for (const fn of this.stateHandlers) fn(next);
  }

  private open(): void {
    this.setState("connecting");
    let socket: WebSocket;
    try {
      socket = new WebSocket(this.url);
    } catch {
      this.scheduleReconnect();
      return;
    }
    this.ws = socket;

    socket.onopen = () => {
      this.attempt = 0;
      this.setState("open");
      const pending = this.queue;
      this.queue = [];
      for (const msg of pending) this.send(msg);
    };

    socket.onmessage = (ev) => {
      if (typeof ev.data !== "string") return;
      let msg: ServerMessage;
      try {
        msg = JSON.parse(ev.data) as ServerMessage;
      } catch {
        return;
      }
      if (!msg || typeof msg.type !== "string") return;
      for (const fn of this.messageHandlers) fn(msg);
    };

    socket.onclose = () => {
      this.ws = null;
      this.setState("closed");
      this.scheduleReconnect();
    };

    socket.onerror = () => {
      // onclose always follows; nothing to do but avoid an unhandled error.
    };
  }

  private scheduleReconnect(): void {
    if (this.timer !== null) return;
    const delay = Math.min(500 * 2 ** this.attempt, 5000);
    this.attempt += 1;
    this.timer = window.setTimeout(() => {
      this.timer = null;
      this.open();
    }, delay);
  }
}
