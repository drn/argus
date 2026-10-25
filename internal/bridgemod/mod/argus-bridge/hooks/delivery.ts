// Pure single-flight protocol. Transport retries may repeat pull/ack, never
// prompt submission. The daemon resolves a lost-submit outcome by transcript.
export interface DeliveryEnv {
  idle(): boolean
  request(path: string, token?: string): Promise<Record<string, unknown> | undefined>
  submit(text: string): Promise<boolean>
}

export class Delivery {
  private running = false
  private ack: string | undefined
  private attempted = new Set<string>()

  constructor(private readonly env: DeliveryEnv) {}

  async poll(): Promise<void> {
    if (this.running) return
    this.running = true
    try {
      if (this.ack) {
        const answer = await this.env.request('deliveries/ack', this.ack)
        // Explicit false means the daemon no longer owns this claim (already
        // acknowledged, cancelled, expired or restarted). Only a transport
        // failure should hold up later deliveries.
        if (typeof answer?.acked !== 'boolean') return
        this.ack = undefined
      }
      if (!this.env.idle()) return
      const answer = await this.env.request('deliveries')
      const claim = answer?.delivery as { token?: unknown; text?: unknown } | null | undefined
      if (!claim || typeof claim.token !== 'string' || typeof claim.text !== 'string' || this.attempted.has(claim.token) || !this.env.idle()) return
      const authorized = await this.env.request('deliveries/authorize', claim.token)
      if (!authorized?.authorized || !this.env.idle()) return
      this.attempted.add(claim.token)
      if (this.attempted.size > 1000) this.attempted.delete(this.attempted.values().next().value as string)
      if (!(await this.env.submit(claim.text))) return
      this.ack = claim.token
      const acked = await this.env.request('deliveries/ack', claim.token)
      if (typeof acked?.acked === 'boolean') this.ack = undefined
    } catch {
      // An unknown submit result is never retried. The daemon checks evidence.
    } finally {
      this.running = false
    }
  }
}
