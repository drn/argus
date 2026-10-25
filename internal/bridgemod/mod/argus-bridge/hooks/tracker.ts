// Pure needs-input derivation from tool-call lifecycle, with no engine access
// so it is unit-testable. Time is always passed in.
//
// A user dialog is pending when:
//   - AskUserQuestion / ExitPlanMode is between tool.call pre and post (the
//     call itself IS the dialog), or
//   - a tool.check verdict of "ask" was seen for a call that is STILL pending
//     after a grace period. "ask" alone does not mean a dialog appeared: a
//     settings PermissionRequest hook can auto-approve it within about a second.
// classic.PermissionRequest / classic.Notification are deliberately not used:
// they are skipped when a managed plugin runs first.

export type Kind = 'question' | 'plan' | 'permission'

/**
 * How sure a needs_input is. A question/plan dialog is bracketed exactly by its
 * tool call (`exact`). A permission ask stays pending through the tool's whole
 * EXECUTION after approval, because the engine raises no event between
 * approval and execution (`pending-or-running`): consumers must corroborate it.
 */
export type Confidence = 'exact' | 'pending-or-running'

export type Transition =
  | { type: 'needs_input'; kind: Kind; confidence: Confidence; tool: string; toolUseId: string }
  | { type: 'cleared'; toolUseId: string }

type Call = { tool: string; direct: boolean; askAt?: number; qualified: boolean }

export const GRACE_MS = 1500

const DIRECT: Record<string, Kind> = { AskUserQuestion: 'question', ExitPlanMode: 'plan' }

export class NeedsInputTracker {
  private calls = new Map<string, Call>()
  // Ask verdicts seen before their tool.call pre (the order is not guaranteed).
  private earlyAsk = new Map<string, number>()
  private lastId = ''

  constructor(private readonly graceMs = GRACE_MS) {}

  get active(): boolean {
    for (const c of this.calls.values()) if (c.qualified) return true
    return false
  }

  /** A tool.call began. */
  callStart(id: string, tool: string): Transition | null {
    const direct = tool in DIRECT
    const askAt = this.earlyAsk.get(id)
    this.earlyAsk.delete(id)
    const wasActive = this.active
    this.calls.set(id, { tool, direct, askAt, qualified: direct })
    return direct && !wasActive ? this.enter(id, tool, DIRECT[tool] as Kind) : null
  }

  /** A tool.check verdict of "ask" was returned for the call. */
  markAsk(id: string, now: number): void {
    const call = this.calls.get(id)
    if (call) call.askAt = now
    else this.earlyAsk.set(id, now)
  }

  /** Qualifies ask-pending calls whose grace has elapsed. */
  poll(now: number): Transition | null {
    const wasActive = this.active
    let first: [string, Call] | undefined
    for (const [id, c] of this.calls) {
      if (!c.qualified && c.askAt !== undefined && now - c.askAt >= this.graceMs) {
        c.qualified = true
        first ??= [id, c]
      }
    }
    return first && !wasActive ? this.enter(first[0], first[1].tool, 'permission') : null
  }

  /** The tool.call finished (post), however it ended. */
  callEnd(id: string): Transition | null {
    this.earlyAsk.delete(id)
    const wasActive = this.active
    this.calls.delete(id)
    return wasActive && !this.active ? { type: 'cleared', toolUseId: id } : null
  }

  /** The turn ended: nothing can still be pending. */
  reset(): Transition | null {
    const wasActive = this.active
    this.calls.clear()
    this.earlyAsk.clear()
    return wasActive ? { type: 'cleared', toolUseId: this.lastId } : null
  }

  /** Earliest time a still-unqualified ask-pending call becomes due, if any. */
  nextDue(): number | undefined {
    let due: number | undefined
    for (const c of this.calls.values()) {
      if (!c.qualified && c.askAt !== undefined) {
        const t = c.askAt + this.graceMs
        due = due === undefined ? t : Math.min(due, t)
      }
    }
    return due
  }

  private enter(id: string, tool: string, kind: Kind): Transition {
    this.lastId = id
    return { type: 'needs_input', kind, confidence: kind === 'permission' ? 'pending-or-running' : 'exact', tool, toolUseId: id }
  }
}
