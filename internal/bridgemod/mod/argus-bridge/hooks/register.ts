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
import { Delivery } from './delivery'
import { GRACE_MS, NeedsInputTracker, type Transition } from './tracker'

/** The engine interface a hook receives. */
type Engine = Parameters<Hook<'session.start'>>[0]

const HEARTBEAT_MS = 15_000
/** A hung daemon must not hang a hook: a post is abandoned after this. */
const POST_TIMEOUT_MS = 2_000

/** Everything one load of the mod keeps; a hot reload makes a fresh one. */
type Bridge = {
	busy: boolean
	needsKind: string
  readonly boot: string
  reporter: Reporter | undefined
  readonly tracker: NeedsInputTracker
  readonly measure: MeasureThrottle
  lastModel: string | undefined
  heartbeat: { cancel: () => void } | undefined
  deliveryTimer: { cancel: () => void } | undefined
  delivery: Delivery | undefined
}

function newBridge(): Bridge {
  return {
	busy: false,
	needsKind: '',
    // `boot` distinguishes this load from an earlier one whose seq restarts.
    boot: Math.random().toString(36).slice(2, 10) + Math.random().toString(36).slice(2, 6),
    reporter: undefined,
    tracker: new NeedsInputTracker(),
    measure: new MeasureThrottle(),
    lastModel: undefined,
    heartbeat: undefined,
    deliveryTimer: undefined,
    delivery: undefined,
  }
}

function envFor($: Engine): Env {
  return {
    taskId: () => $.env.get('ARGUS_TASK_ID'),
	launchGeneration: () => $.env.get('ARGUS_BRIDGE_GENERATION'),
    home: () => $.env.get('HOME'),
    dataDir: () => $.env.get('ARGUS_BRIDGE_DIR'),
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
	b.needsKind = t.type === 'needs_input' ? t.kind : ''
  if (t.type === 'needs_input') {
    await rep($, b).emit('needs_input', { kind: t.kind, confidence: t.confidence, tool: t.tool, tool_use_id: t.toolUseId })
  }
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
	await syncSession($, b, false)
  await pollNeedsInput($, b)
  await sendMeasure($, b, b.measure.flush(await $.clock.now()))
  await rep($, b).emit('heartbeat', { busy: b.busy, needs_input: b.tracker.active, kind: b.needsKind, confidence: b.needsKind === 'permission' ? 'pending-or-running' : 'exact' })
  await rep($, b).flush()
}

// Read discovery on every request: sessions survive daemon port/token changes.
async function bridgeRequest($: Engine, b: Bridge, path: string, token?: string): Promise<Record<string, unknown> | undefined> {
  const task = await rep($, b).taskId()
  if (!task) return undefined
  const dir = (await $.env.get('ARGUS_BRIDGE_DIR')) || `${await $.env.get('HOME')}/.argus`
  const port = (await $.fs.read(`${dir}/api-port`)).trim()
  const auth = (await $.fs.read(`${dir}/api-token`)).trim()
  if (!/^\d+$/.test(port) || !auth) return undefined
  const body = JSON.stringify({ generation: await $.env.get('ARGUS_BRIDGE_GENERATION'), boot: b.boot, token })
  const response = await Promise.race([
    $.http.fetch(`http://127.0.0.1:${port}/api/tasks/${encodeURIComponent(task)}/bridge/${path}`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${auth}`, 'Content-Type': 'application/json' },
      body,
    }),
    $.clock.sleep(POST_TIMEOUT_MS).then(() => undefined),
  ])
  if (!response?.ok) return undefined
  return JSON.parse(response.text) as Record<string, unknown>
}

function deliveryFor($: Engine, b: Bridge): Delivery {
  b.delivery ??= new Delivery({
    idle: () => !b.busy && !b.tracker.active,
    request: (path, token) => bridgeRequest($, b, path, token),
    submit: async text => {
      if (b.busy || b.tracker.active) return false
      const result = await $.prompt.submit({ text })
      return !result.drop
    },
  })
  return b.delivery
}

async function pollDelivery($: Engine, b: Bridge): Promise<void> {
  // Flush turn/identity reports before requesting a route decision.
  await rep($, b).flush()
  await deliveryFor($, b).poll()
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
      b.deliveryTimer?.cancel()
      b.deliveryTimer = $.clock.every(5_000, () => { void safe(() => pollDelivery($, b)) })
      $.clock.after(0, () => { void safe(() => pollDelivery($, b)) })
    })
    return next(e)
  })

  on('turn.start', async ($, e, next) => {
    if (e.agentId === undefined) {
      b.busy = true
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
		b.busy = false
        await sendMeasure($, b, b.measure.flush(await $.clock.now()))
        await rep($, b).emit('turn.complete', { turn_id: e.turnId, reason: e.reason })
        $.clock.after(0, () => { void safe(() => pollDelivery($, b)) })
      })
    }
    return next(e)
  })

  on('tool.check', async ($, e, next) => {
    const result = await next(e)
    // Not filtered on agentId: an ask inside a subagent blocks the user just the same.
    if (e.tool_use_id && result.decision === 'ask') {
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
    // Not filtered on agentId (see tool.check): subagent dialogs block the user.
    const id = e.tool_use_id
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
