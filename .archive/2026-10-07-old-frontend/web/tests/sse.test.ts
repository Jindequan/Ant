/**
 * R15 SSE lifecycle tests (audit probes + rework additions), using a stub
 * EventSource — no real network. The two probe cases come from
 * docs/evidence/CODEX-T1-AUDIT/codex-audit.test.ts.txt:
 *   1. an absolute endpoint must survive a configured VITE_API_BASE;
 *   2. an already-aborted signal must leave no active connection.
 * Additional cases pin the rework contract: close() removes the abort
 * listener, relative endpoints join correctly, initialLastSequence travels in
 * the URL, sequence dedup, and malformed deliveries are ignored + counted.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  resolveEventsUrl,
  subscribeOperationEvents,
  type OperationEvent,
} from '../src/api/events';

type DataHandler = (message: MessageEvent<string>) => void;

/** Minimal EventSource double recording construction and handlers. */
class FakeEventSource {
  static instances: FakeEventSource[] = [];

  readonly url: string;
  closed = false;
  onmessage: DataHandler | null = null;
  onerror: ((error: unknown) => void) | null = null;
  private readonly typedHandlers = new Map<string, DataHandler[]>();

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, handler: DataHandler): void {
    const list = this.typedHandlers.get(type) ?? [];
    list.push(handler);
    this.typedHandlers.set(type, list);
  }

  close(): void {
    this.closed = true;
  }

  /** Deliver a named event (or the default `message`) with JSON/string data. */
  emit(type: string, data: unknown, lastEventId = ''): void {
    const message = new MessageEvent(type, {
      data: typeof data === 'string' ? data : JSON.stringify(data),
      lastEventId,
    }) as MessageEvent<string>;
    if (type === 'message') {
      this.onmessage?.(message);
      return;
    }
    for (const handler of this.typedHandlers.get(type) ?? []) handler(message);
  }

  static activeCount(): number {
    return FakeEventSource.instances.filter((instance) => !instance.closed).length;
  }

  static reset(): void {
    FakeEventSource.instances = [];
  }
}

function stubEventSource(): void {
  vi.stubGlobal('EventSource', FakeEventSource);
}

function opEvent(sequence: number): OperationEvent {
  return { operationId: 'op-1', sequence, type: 'output_stored', createdAt: 1, payload: {} };
}

/** AbortSignal test double that records listeners (for removal assertions). */
function recordingSignal(): {
  signal: AbortSignal;
  listeners: Array<() => void>;
  abort(): void;
} {
  const listeners: Array<() => void> = [];
  const signal = {
    get aborted(): boolean {
      return false;
    },
    addEventListener(_type: 'abort', listener: () => void): void {
      listeners.push(listener);
    },
    removeEventListener(_type: 'abort', listener: () => void): void {
      const index = listeners.indexOf(listener);
      if (index >= 0) listeners.splice(index, 1);
    },
  } as unknown as AbortSignal;
  return {
    signal,
    listeners,
    abort: () => {
      for (const listener of [...listeners]) listener();
    },
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  FakeEventSource.reset();
});

describe('resolveEventsUrl', () => {
  it('uses an absolute http(s) endpoint verbatim, never joined with the base', () => {
    expect(resolveEventsUrl('https://api.example.com', 'https://events.example.com/v1/operations/op-1/events')).toBe(
      'https://events.example.com/v1/operations/op-1/events',
    );
  });

  it('joins relative endpoints with the base, handling slashes on both sides', () => {
    expect(resolveEventsUrl('https://api.example.com', '/v1/operations/op-1/events')).toBe(
      'https://api.example.com/v1/operations/op-1/events',
    );
    expect(resolveEventsUrl('https://api.example.com/', 'v1/operations/op-1/events')).toBe(
      'https://api.example.com/v1/operations/op-1/events',
    );
    expect(resolveEventsUrl('https://api.example.com//', '/v1/operations/op-1/events')).toBe(
      'https://api.example.com/v1/operations/op-1/events',
    );
  });

  it('keeps relative endpoints same-origin when no base is configured', () => {
    expect(resolveEventsUrl('', '/v1/operations/op-1/events')).toBe('/v1/operations/op-1/events');
  });

  it('rejects non-http(s) schemes on either side and protocol-relative endpoints', () => {
    expect(() => resolveEventsUrl('', 'javascript:alert(1)')).toThrow(/http\/https/);
    expect(() => resolveEventsUrl('', 'ws://events.example.com/v1/x')).toThrow(/http\/https/);
    expect(() => resolveEventsUrl('ftp://files.example.com', '/v1/x')).toThrow(/http\/https/);
    expect(() => resolveEventsUrl('', '//events.example.com/v1/x')).toThrow(/protocol-relative/);
  });
});

describe('Codex audit SSE lifecycle probes', () => {
  it('preserves an absolute endpoint when API base is configured', () => {
    vi.stubEnv('VITE_API_BASE', 'https://api.fixture.invalid');
    stubEventSource();
    const seen: OperationEvent[] = [];
    const sub = subscribeOperationEvents({
      url: 'https://api.fixture.invalid/v1/operations/op-1/events',
      onEvent: seen.push.bind(seen),
    });
    expect(FakeEventSource.instances).toHaveLength(1);
    expect(FakeEventSource.instances[0]?.url).toBe('https://api.fixture.invalid/v1/operations/op-1/events');
    sub.close();
    expect(FakeEventSource.activeCount()).toBe(0);
    expect(seen).toEqual([]);
  });

  it('honors an already aborted signal — no active connection is left behind', () => {
    stubEventSource();
    const controller = new AbortController();
    controller.abort();
    const sub = subscribeOperationEvents({
      url: '/v1/operations/op-1/events',
      signal: controller.signal,
      onEvent: () => {},
    });
    // Nothing is constructed at all: zero instances, zero active connections.
    expect(FakeEventSource.instances).toHaveLength(0);
    expect(FakeEventSource.activeCount()).toBe(0);
    // Closing an never-opened subscription is safe and idempotent.
    expect(() => {
      sub.close();
      sub.close();
    }).not.toThrow();
    expect(sub.getLastSequence()).toBe(0);
    expect(sub.getMalformedCount()).toBe(0);
  });
});

describe('subscription lifecycle', () => {
  it('closes the live connection when the signal aborts', () => {
    stubEventSource();
    const controller = new AbortController();
    const sub = subscribeOperationEvents({
      url: '/v1/operations/op-1/events',
      signal: controller.signal,
      onEvent: () => {},
    });
    expect(FakeEventSource.instances).toHaveLength(1);
    expect(FakeEventSource.activeCount()).toBe(1);
    controller.abort();
    expect(FakeEventSource.activeCount()).toBe(0);
    sub.close(); // idempotent after abort
  });

  it('close removes the abort listener, so a late abort has no effect', () => {
    stubEventSource();
    const { signal, listeners, abort } = recordingSignal();
    const sub = subscribeOperationEvents({ url: '/v1/operations/op-1/events', signal, onEvent: () => {} });
    expect(listeners).toHaveLength(1);
    const instance = FakeEventSource.instances[0];
    expect(instance?.closed).toBe(false);

    sub.close();
    expect(listeners).toHaveLength(0); // R15: no listener leak
    expect(instance?.closed).toBe(true);

    abort(); // late abort after close: nothing left to invoke
    expect(instance?.closed).toBe(true);
  });
});

describe('first-resume cursor', () => {
  it('sends initialLastSequence as afterSequence in the subscription URL', () => {
    stubEventSource();
    const sub = subscribeOperationEvents({
      url: '/v1/operations/op-1/events',
      initialLastSequence: 7,
      onEvent: () => {},
    });
    expect(FakeEventSource.instances[0]?.url).toBe('/v1/operations/op-1/events?afterSequence=7');
    expect(sub.getLastSequence()).toBe(7); // the cursor also seeds the dedup floor
    sub.close();
  });

  it('appends the cursor with & when the endpoint already has a query', () => {
    stubEventSource();
    const sub = subscribeOperationEvents({
      url: 'https://api.fixture.invalid/v1/operations/op-1/events?trace=1',
      initialLastSequence: 3,
      onEvent: () => {},
    });
    expect(FakeEventSource.instances[0]?.url).toBe(
      'https://api.fixture.invalid/v1/operations/op-1/events?trace=1&afterSequence=3',
    );
    sub.close();
  });

  it('omits the cursor parameter when no initial position is given', () => {
    stubEventSource();
    const sub = subscribeOperationEvents({ url: '/v1/operations/op-1/events', onEvent: () => {} });
    expect(FakeEventSource.instances[0]?.url).toBe('/v1/operations/op-1/events');
    sub.close();
  });
});

describe('delivery semantics', () => {
  it('dedups by sequence, lets the id line win, and ignores + counts malformed deliveries', () => {
    stubEventSource();
    const seen: number[] = [];
    const sub = subscribeOperationEvents({ url: '/v1/operations/op-1/events', onEvent: (e) => seen.push(e.sequence) });
    const instance = FakeEventSource.instances[0];
    expect(instance).toBeDefined();

    instance?.emit('message', opEvent(3));
    expect(seen).toEqual([3]);

    instance?.emit('message', opEvent(3)); // replay: dropped
    instance?.emit('message', opEvent(2)); // out of order: dropped
    expect(seen).toEqual([3]);

    // id: line is authoritative over the envelope sequence.
    instance?.emit('accepted', { ...opEvent(4), sequence: 100 }, '5');
    expect(seen).toEqual([3, 100]);
    expect(sub.getLastSequence()).toBe(5);
    instance?.emit('message', opEvent(5)); // <= cursor 5: dropped even with fresh id
    expect(seen).toEqual([3, 100]);

    // Malformed: non-JSON data, and JSON without a usable sequence/id.
    instance?.emit('message', 'not-json');
    instance?.emit('message', { operationId: 'op-1', type: 'output_stored', createdAt: 1, payload: {} });
    expect(sub.getMalformedCount()).toBe(2);
    expect(seen).toEqual([3, 100]);
    expect(sub.getLastSequence()).toBe(5);

    instance?.emit('message', opEvent(6));
    expect(seen).toEqual([3, 100, 6]);
    expect(sub.getLastSequence()).toBe(6);
    sub.close();
  });
});
