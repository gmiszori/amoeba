import type { ClientMessage, ServerMessage } from "./protocol";

interface Handlers {
  open(): void;
  message(msg: ServerMessage): void;
  status(connected: boolean): void;
}

/**
 * A WebSocket that reconnects on its own. Phones drop connections all the
 * time (screen lock, switching apps, flaky lecture-hall Wi-Fi), so a lost
 * connection is normal and silently retried.
 */
export class Connection {
  private ws: WebSocket | null = null;
  private retryDelay = 500;
  private retryTimer: number | undefined;
  private stopped = false;

  constructor(
    private readonly url: string,
    private readonly on: Handlers,
  ) {
    document.addEventListener("visibilitychange", () => {
      if (document.visibilityState === "visible") this.reconnectNow();
    });
    window.addEventListener("online", () => this.reconnectNow());
    this.connect();
  }

  get connected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
  }

  send(msg: ClientMessage): boolean {
    if (!this.connected) return false;
    this.ws!.send(JSON.stringify(msg));
    return true;
  }

  /** Stops reconnecting for good, e.g. after the game was opened elsewhere. */
  stop(): void {
    this.stopped = true;
    clearTimeout(this.retryTimer);
    this.ws?.close();
  }

  private connect(): void {
    if (this.stopped) return;
    const ws = new WebSocket(this.url);
    this.ws = ws;
    ws.onopen = () => {
      this.retryDelay = 500;
      this.on.status(true);
      this.on.open();
    };
    ws.onmessage = (e) => {
      try {
        this.on.message(JSON.parse(e.data));
      } catch (err) {
        console.error("bad message from server", err);
      }
    };
    ws.onclose = () => {
      if (this.ws !== ws || this.stopped) return;
      this.ws = null;
      this.on.status(false);
      this.scheduleRetry();
    };
  }

  private scheduleRetry(): void {
    if (this.stopped) return;
    clearTimeout(this.retryTimer);
    this.retryTimer = window.setTimeout(() => this.connect(), this.retryDelay);
    this.retryDelay = Math.min(this.retryDelay * 2, 5000);
  }

  private reconnectNow(): void {
    if (this.stopped || this.ws) return;
    clearTimeout(this.retryTimer);
    this.retryDelay = 500;
    this.connect();
  }
}
