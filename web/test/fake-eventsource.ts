// A fake EventSource for tests: you push events and errors by hand.
type Listener = (e: MessageEvent) => void;

export class FakeEventSource {
  static instances: FakeEventSource[] = [];
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  url: string;
  readyState = 0;
  onopen: ((e: Event) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;
  onmessage: Listener | null = null;
  private listeners = new Map<string, Listener[]>();

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, l: Listener) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), l]);
  }
  close() {
    this.readyState = 2;
  }
  // ---- test controls ----
  open() {
    this.readyState = 1;
    this.onopen?.(new Event("open"));
  }
  emit(type: string, data: unknown, id = "") {
    const e = new MessageEvent(type, { data: JSON.stringify(data), lastEventId: id });
    for (const l of this.listeners.get(type) ?? []) l(e);
  }
  /** A drop the browser will retry by itself (readyState CONNECTING). */
  drop() {
    this.readyState = 0;
    this.onerror?.(new Event("error"));
  }
  /** A drop the browser gives up on (readyState CLOSED). */
  fail() {
    this.readyState = 2;
    this.onerror?.(new Event("error"));
  }
  static reset() {
    FakeEventSource.instances = [];
  }
  static last() {
    return FakeEventSource.instances[FakeEventSource.instances.length - 1];
  }
}
