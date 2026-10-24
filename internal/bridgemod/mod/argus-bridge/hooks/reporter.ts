// Event building, bounded buffering and delivery to the argus daemon. Pure of
// the engine: everything it touches comes through `Env`, so it is unit-tested
// with fakes. Never throws: a dead or confused daemon must never break (or
// even slow) the Claude session, so failures drop or buffer events.

export type WireEvent = {
  type: string
  seq: number
  boot: string
  ts: number
  session_id?: string
  [field: string]: unknown
}

export interface Env {
  /** ARGUS_TASK_ID; undefined outside an argus task. */
  taskId(): Promise<string | undefined>
  home(): Promise<string | undefined>
  /** File text, or undefined when unreadable. */
  readFile(path: string): Promise<string | undefined>
  /** The response, or undefined on any connection failure or timeout. */
  fetch(url: string, init: { method: string; headers: Record<string, string>; body: string }): Promise<{ status: number; ok: boolean } | undefined>
  now(): Promise<number>
}

export type PostResult = 'sent' | 'drop' | 'retry'

export const MAX_BUFFER = 200
const BATCH = 50

export class Reporter {
  readonly boot: string
  private seq = 0
  private queue: WireEvent[] = []
  private inflight: Promise<void> | undefined
  private token: string | undefined
  private port: string | undefined
  private task: string | undefined | null = null // null = not resolved yet
  private lastSession: string | undefined

  constructor(private readonly env: Env, boot: string, private readonly maxBuffer = MAX_BUFFER) {
    this.boot = boot
  }

  /** The task id, or undefined when this session is not an argus task (mod inert). */
  async taskId(): Promise<string | undefined> {
    if (this.task === null) this.task = (await this.env.taskId()) || undefined
    return this.task
  }

  get sessionId(): string | undefined {
    return this.lastSession
  }

  setSession(id: string | undefined): void {
    this.lastSession = id
  }

  get pending(): number {
    return this.queue.length
  }

  /** Stamps and queues an event, then tries to deliver. Resolves, never rejects. */
  async emit(type: string, fields: Record<string, unknown> = {}): Promise<void> {
    try {
      if (!(await this.taskId())) return
      const ev: WireEvent = {
        type,
        seq: ++this.seq,
        boot: this.boot,
        ts: await this.env.now(),
        ...(this.lastSession ? { session_id: this.lastSession } : {}),
        ...fields,
      }
      this.queue.push(ev)
      if (this.queue.length > this.maxBuffer) this.queue.splice(0, this.queue.length - this.maxBuffer)
      await this.flush()
    } catch {
      // fail open
    }
  }

  /** Single-flight delivery of everything queued. */
  flush(): Promise<void> {
    if (!this.inflight) {
      this.inflight = this.drain()
        .catch(() => undefined)
        .finally(() => {
          this.inflight = undefined
        })
    }
    return this.inflight
  }

  private async drain(): Promise<void> {
    while (this.queue.length > 0) {
      const batch = this.queue.slice(0, BATCH)
      const result = await this.post(batch)
      if (result === 'retry') return
      // Events may have been trimmed from the front while the post was in
      // flight; remove by identity, not by position.
      const sent = new Set(batch)
      this.queue = this.queue.filter(e => !sent.has(e))
    }
  }

  private async post(batch: WireEvent[]): Promise<PostResult> {
    const task = await this.taskId()
    const home = await this.env.home()
    if (!task || !home) return 'retry'
    for (let attempt = 0; attempt < 2; attempt++) {
      if (this.token === undefined) this.token = (await this.env.readFile(`${home}/.argus/api-token`))?.trim() || undefined
      if (this.port === undefined) this.port = (await this.env.readFile(`${home}/.argus/api-port`))?.trim() || undefined
      if (!this.token || !this.port) {
        // Files missing (daemon never ran or is mid-restart): forget and retry later.
        this.token = this.port = undefined
        return 'retry'
      }
      const res = await this.env.fetch(`http://127.0.0.1:${this.port}/api/tasks/${encodeURIComponent(task)}/bridge/events`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${this.token}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ events: batch }),
      })
      if (!res) {
        // The daemon may have restarted on another port: re-read it and go again.
        this.port = undefined
        continue
      }
      if (res.ok) return 'sent'
      if (res.status === 401) {
        this.token = undefined
        continue
      }
      if (res.status >= 500) return 'retry'
      return 'drop' // 400/404/...: resending the same batch cannot help
    }
    return 'retry'
  }
}
