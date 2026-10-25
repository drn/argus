// session.measure → wire fields, plus the throttle that keeps a chatty event
// (it fires after every tool step) from flooding the daemon.

export type MeasureInput = {
  context?: { tokens?: number; percent?: number; window?: number }
  rateLimits?: ReadonlyArray<{ kind: string; percentUsed: number; resetsAt?: string }>
  cost?: { usd: number }
}

export function measureFields(m: MeasureInput): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  if (m.context) {
    out.context = { tokens: m.context.tokens, percent: m.context.percent, window: m.context.window }
  }
  if (m.cost) out.cost_usd = m.cost.usd
  out.rate_limits = (m.rateLimits ?? []).map(r => ({
    kind: r.kind,
    percent_used: r.percentUsed,
    resets_at: r.resetsAt,
  }))
  return out
}

export const MEASURE_MIN_INTERVAL_MS = 5000

/** Holds the latest measurement; releases it at most once per interval, or on demand. */
export class MeasureThrottle {
  private latest: Record<string, unknown> | undefined
  private lastSentAt = -Infinity
  private lastKey = ''

  /** Records a measurement; returns it when it should be sent now. */
  offer(fields: Record<string, unknown>, now: number): Record<string, unknown> | undefined {
    this.latest = fields
    return now - this.lastSentAt >= MEASURE_MIN_INTERVAL_MS ? this.take(now) : undefined
  }

  /** Releases the held measurement if it differs from what was last sent. */
  flush(now: number): Record<string, unknown> | undefined {
    return this.latest ? this.take(now) : undefined
  }

  private take(now: number): Record<string, unknown> | undefined {
    const fields = this.latest
    this.latest = undefined
    if (!fields) return undefined
    const key = JSON.stringify(fields)
    if (key === this.lastKey) return undefined
    this.lastKey = key
    this.lastSentAt = now
    return fields
  }
}
