// argus-bridge: reports this Claude session's state to the argus daemon as
// structured events (phase 1: report-only; argus compares them with its own
// screen-reading and acts on nothing). Inert unless ARGUS_TASK_ID is set.
//
// Every hook passes through to `next` and never throws: a dead daemon, a
// missing token or any bug here must not change how the session behaves.
//
// `$` is only ever handed to function declarations at the top of this file
// (`claude plugin validate` follows it nowhere else), so the helpers below take
// `$` and the per-load `Bridge` state explicitly instead of closing over them.
import type { Hook, Register } from 'claude-code'
import { measureFields, MeasureThrottle } from './measure'
import { Reporter, type Env } from './reporter'
import { GRACE_MS, NeedsInputTracker, type Transition } from './tracker'

/** The engine interface a hook receives. */
type Engine = Parameters<Hook<'session.start'>>[0]

const HEARTBEAT_MS = 15_000
/** A hung daemon must not hang a hook: a post is abandoned after this. */
const POST_TIMEOUT_MS = 2_000

/** Everything one load of the mod keeps; a hot reload makes a fresh one. */
type Bridge = {
  readonly boot: string
  reporter: Reporter | undefined
  readonly tracker: NeedsInputTracker
  readonly measure: MeasureThrottle
  lastModel: string | undefined
  heartbeat: { cancel: () => void } | undefined
}

function newBridge(): Bridge {
  return {
    // `boot` distinguishes this load from an earlier one whose seq restarts.
    boot: Math.random().toString(36).slice(2, 10) + Math.random().toString(36).slice(2, 6),
    reporter: undefined,
    tracker: new NeedsInputTracker(),
    measure: new MeasureThrottle(),
    lastModel: undefined,
    heartbeat: undefined,
  }
}

function envFor($: Engine): Env {
  return {
    taskId: () => $.env.get('ARGUS_TASK_ID'),
    home: () => $.env.get('HOME'),
    readFile: async path => {
      try {
        return await $.fs.read(path)
      } catch {
        return undefined
      }
    },
    fetch: async (url, init) => {
      try {
        const res = await Promise.race([
          $.http.fetch(url, init),
          $.clock.sleep(POST_TIMEOUT_MS).then(() => undefined),
        ])
        return res ? { status: res.status, ok: res.ok } : undefined
      } catch {
        return undefined
      }
    },
    now: () => $.clock.now(),
  }
}

function rep($: Engine, b: Bridge): Reporter {
  b.reporter ??= new Reporter(envFor($), b.boot)
  return b.reporter
}

async function safe(work: () => Promise<unknown>): Promise<void> {
  try {
    await work()
  } catch {
    // fail open
  }
}

async function emitTransition($: Engine, b: Bridge, t: Transition | null): Promise<void> {
  if (!t) return
  if (t.type === 'needs_input') await rep($, b).emit('needs_input', { kind: t.kind, tool: t.tool, tool_use_id: t.toolUseId })
  else await rep($, b).emit('cleared', { tool_use_id: t.toolUseId })
}

async function sendMeasure($: Engine, b: Bridge, fields: Record<string, unknown> | undefined): Promise<void> {
  if (fields) await rep($, b).emit('session.measure', fields)
}

// Reports the session id/model/cwd when any differs from what was last
// reported. /clear does NOT re-fire session.start, so this also runs on every
// turn.start.
async function syncSession($: Engine, b: Bridge, force: boolean): Promise<void> {
  const r = rep($, b)
  const id = await $.session.id()
  const model = await $.session.model()
  if (!force && id === r.sessionId && model === b.lastModel) return
  r.setSession(id)
  b.lastModel = model
  await r.emit('session.start', { model, cwd: await $.session.cwd() })
}

async function pollNeedsInput($: Engine, b: Bridge): Promise<void> {
  await emitTransition($, b, b.tracker.poll(await $.clock.now()))
}

async function beat($: Engine, b: Bridge): Promise<void> {
  await pollNeedsInput($, b)
  await sendMeasure($, b, b.measure.flush(await $.clock.now()))
  await rep($, b).emit('heartbeat')
  await rep($, b).flush()
}

export const register: Register = on => {
  const b = newBridge()

  on('session.start', async ($, e, next) => {
    await safe(async () => {
      if (!(await rep($, b).taskId())) return
      await syncSession($, b, true)
      b.heartbeat?.cancel()
      b.heartbeat = $.clock.every(HEARTBEAT_MS, () => {
        void safe(() => beat($, b))
      })
    })
    return next(e)
  })

  on('turn.start', async ($, e, next) => {
    if (e.agentId === undefined) {
      await safe(async () => {
        if (!(await rep($, b).taskId())) return
        await syncSession($, b, false)
        await rep($, b).emit('turn.start', { turn_id: e.turnId })
      })
    }
    return next(e)
  })

  on('turn.complete', async ($, e, next) => {
    if (e.agentId === undefined) {
      await safe(async () => {
        if (!(await rep($, b).taskId())) return
        await emitTransition($, b, b.tracker.reset())
        await sendMeasure($, b, b.measure.flush(await $.clock.now()))
        await rep($, b).emit('turn.complete', { reason: e.reason })
      })
    }
    return next(e)
  })

  on('tool.check', async ($, e, next) => {
    const result = await next(e)
    if (e.agentId === undefined && e.tool_use_id && result.decision === 'ask') {
      await safe(async () => {
        if (!(await rep($, b).taskId())) return
        b.tracker.markAsk(e.tool_use_id as string, await $.clock.now())
        // Qualify it once the grace has passed if the call is still pending;
        // the heartbeat poll is the backstop should this timer not fire.
        $.clock.after(GRACE_MS + 100, () => {
          void safe(() => pollNeedsInput($, b))
        })
      })
    }
    return result
  }).catch(($, e, next) => next(e))

  on('tool.call', async ($, e, next) => {
    const id = e.agentId === undefined ? e.tool_use_id : undefined
    if (id) {
      await safe(async () => {
        if (!(await rep($, b).taskId())) return
        await emitTransition($, b, b.tracker.callStart(id, e.tool))
      })
    }
    try {
      return await next(e)
    } finally {
      if (id) {
        await safe(async () => {
          if (!(await rep($, b).taskId())) return
          await emitTransition($, b, b.tracker.callEnd(id))
        })
      }
    }
  }).catch(($, e, next) => next(e))

  on('session.measure', async ($, e, next) => {
    await safe(async () => {
      if (!(await rep($, b).taskId())) return
      await sendMeasure($, b, b.measure.offer(measureFields(e), await $.clock.now()))
    })
    return next(e)
  })
}
