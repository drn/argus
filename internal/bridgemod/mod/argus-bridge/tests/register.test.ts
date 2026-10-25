import { expect, mock, test } from 'claude-code/testing'
import type { On } from 'claude-code'

type Posted = { type: string; seq: number; boot: string; session_id?: string; [k: string]: unknown }

function world(on: On, opts: { task?: string; sessionId?: () => string; denyFetch?: boolean; hangFetch?: boolean; deliver?: boolean } = {}) {
  const posted: Posted[] = []
  const urls: string[] = []
  const reads: string[] = []
  let fetches = 0
  let offered = false
  const submitted: string[] = []
  const clock = mock.clock(on, { now: 1_000_000 })
  mock.env(on, { HOME: '/h', ARGUS_BRIDGE_GENERATION: 'launch', ...(opts.task === undefined ? {} : { ARGUS_TASK_ID: opts.task }) })
  on('fs.read', (_, e) => {
    reads.push(e.path)
    return { value: e.path.endsWith('api-token') ? 'tok' : '7743' }
  })
  on('http.fetch', (_, e) => {
    fetches++
    if (opts.denyFetch) return { deny: 'network blocked' }
    if (opts.hangFetch) return new Promise(() => {}) as never
    urls.push(e.url)
    if (opts.deliver && e.url.endsWith('/deliveries')) {
      const delivery = offered ? null : { token: 'c1', text: 'INTEGRATION_NOTICE [argus-delivery:hera:1]' }
      offered = true
      return { value: { status: 200, ok: true, headers: {}, text: JSON.stringify({ delivery }) } }
    }
    if (opts.deliver && e.url.endsWith('/authorize')) return { value: { status: 200, ok: true, headers: {}, text: '{"authorized":true}' } }
    if (opts.deliver && e.url.endsWith('/ack')) return { value: { status: 200, ok: true, headers: {}, text: '{"acked":true}' } }
    posted.push(...(JSON.parse(e.init?.body ?? '{"events":[]}').events ?? []))
    return { value: { status: 200, ok: true, headers: {}, text: '{}' } }
  })
  on('session.id', () => ({ value: opts.sessionId ? opts.sessionId() : 'sess-1' }))
  on('session.model', () => ({ value: 'opus' }))
  on('session.cwd', () => ({ value: '/work' }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('command.register', (_, e) => ({ value: { command: e.name } }))
  on('turn.start', (_, e) => ({ turnId: e.turnId }))
  on('turn.complete', (_, e) => ({ text: e.answer }))
  on('session.measure', (_, e) => ({ changed: e.changed }))
  on('prompt.submit', (_, e) => { submitted.push(e.text); return { text: e.text } })
  // Delivery is background work: let it run before looking at what was posted.
  const delivered = async () => {
    await clock.settle()
    await clock.settle()
  }
  return { posted, urls, reads, submitted, fetches: () => fetches, clock, delivered, types: () => posted.map(p => p.type) }
}

const START = { cwd: '/work', surface: 'terminal', isInteractive: true } as const

test('real registration polls after a completed turn and never during a busy turn', async ($, on) => {
  const w = world(on, { task: 'T1', deliver: true })
  await $.session.start(START)
  await $.turn.start({ text: 'working', turnId: 't1' })
  await w.clock.advance(5_000)
  await w.delivered()
  expect(w.submitted).toEqual([])
  expect(w.urls.filter(u => u.endsWith('/deliveries'))).toEqual([])
  await $.turn.complete({ turnId: 't1', reason: 'answer', durationMs: 1, isAborted: false, answer: 'done' })
  await w.clock.advance(5_000)
  await w.delivered()
  expect(w.submitted).toEqual(['INTEGRATION_NOTICE [argus-delivery:hera:1]'])
  expect(w.urls.some(u => u.endsWith('/deliveries/ack'))).toBe(true)
})

test('heartbeats reconstruct a busy session after a daemon bounce', async ($, on) => {
 const w=world(on,{task:'T1'})
 await $.session.start(START)
 await $.turn.start({text:'work',turnId:'t1'})
 await w.clock.advance(15_000)
 await w.delivered()
 expect(w.posted.find(e=>e.type==='heartbeat')).toMatchObject({busy:true, needs_input:false, generation:'launch'})
})

test('inert without ARGUS_TASK_ID: no reads, no posts, no fetches, no heartbeat', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  await $.turn.start({ text: 'hi', turnId: 't1' })
  await w.clock.advance(60_000)
  await w.delivered()
  expect(w.posted.length).toBe(0)
  expect(w.fetches()).toBe(0)
  // Not even the token/port files: an unrelated Claude session must read nothing.
  expect(w.reads.length).toBe(0)
})

test('session.start reports id, model and cwd; turns report busy then idle in order', async ($, on) => {
  const w = world(on, { task: 'T1' })
  await $.session.start(START)
  await $.turn.start({ text: 'hi', turnId: 't1' })
  await $.turn.complete({ turnId: 't1', reason: 'answer', durationMs: 1, isAborted: false, answer: 'ok' })
  await w.delivered()
  expect(w.types()).toEqual(['session.start', 'turn.start', 'turn.complete'])
  expect(w.posted[0]).toMatchObject({ session_id: 'sess-1', model: 'opus', cwd: '/work' })
	expect(w.posted[0]).toMatchObject({ generation: 'launch' })
  expect(w.posted[1]).toMatchObject({ turn_id: 't1' })
  expect(w.posted[2]).toMatchObject({ turn_id: 't1', reason: 'answer' })
  expect(w.posted.map(p => p.seq)).toEqual([1, 2, 3])
  expect(new Set(w.posted.map(p => p.boot)).size).toBe(1)
  expect(w.urls[0]).toBe('http://127.0.0.1:7743/api/tasks/T1/bridge/events')
})

test('subagent turn start/complete are ignored for busy/idle', async ($, on) => {
  const w = world(on, { task: 'T1' })
  await $.session.start(START)
  await $.turn.start({ text: 'x', turnId: 'sub', agentId: 'agent-9' })
  await $.turn.complete({ turnId: 'sub', reason: 'answer', durationMs: 1, isAborted: false, answer: '', agentId: 'agent-9' })
  await w.delivered()
  expect(w.types()).toEqual(['session.start'])
})

test('a /clear (new session id, no session.start) is reported before the next turn.start', async ($, on) => {
  let id = 'old'
  const w = world(on, { task: 'T1', sessionId: () => id })
  await $.session.start(START)
  await $.turn.start({ text: 'a', turnId: 't1' })
  await $.turn.complete({ turnId: 't1', reason: 'answer', durationMs: 1, isAborted: false, answer: '' })
  id = 'new'
  await $.turn.start({ text: 'b', turnId: 't2' })
  await w.delivered()
  expect(w.types()).toEqual(['session.start', 'turn.start', 'turn.complete', 'session.start', 'turn.start'])
  expect(w.posted[3]).toMatchObject({ session_id: 'new' })
  expect(w.posted[4]).toMatchObject({ session_id: 'new' })
})

test('the heartbeat repeats about every 15 seconds', async ($, on) => {
  const w = world(on, { task: 'T1' })
  await $.session.start(START)
  await w.clock.advance(15_000)
  await w.clock.advance(15_000)
  await w.delivered()
  expect(w.types().filter(t => t === 'heartbeat').length).toBe(2)
})

test('session.measure maps usage, throttled to the latest on turn end', async ($, on) => {
  const w = world(on, { task: 'T1' })
  await $.session.start(START)
  const m = (tokens: number) => ({
    context: { tokens, percent: 1, window: 200000 },
    rateLimits: [{ kind: 'five_hour', percentUsed: 10, resetsAt: 'soon' }],
    cost: { usd: 0.1 },
    changed: ['context' as const],
  })
  await $.session.measure(m(100))
  await $.session.measure(m(200))
  await $.session.measure(m(300))
  await $.turn.complete({ turnId: 't', reason: 'answer', durationMs: 1, isAborted: false, answer: '' })
  await w.delivered()
  const measures = w.posted.filter(p => p.type === 'session.measure')
  expect(measures.length).toBe(2)
  expect(measures[0]).toMatchObject({ context: { tokens: 100 }, cost_usd: 0.1, rate_limits: [{ kind: 'five_hour', percent_used: 10, resets_at: 'soon' }] })
  expect(measures[1]).toMatchObject({ context: { tokens: 300 } })
})

test('AskUserQuestion reports an exact needs_input while pending and cleared when it ends', async ($, on) => {
  const w = world(on, { task: 'T1' })
  let release = () => {}
  on('tool.call', async () => {
    await new Promise<void>(r => {
      release = r
    })
    return { result: {} }
  })
  await $.session.start(START)
  const call = $.tool.call({ tool: 'AskUserQuestion', questions: [] } as never)
  await w.clock.settle()
  await w.delivered()
  expect(w.posted.find(p => p.type === 'needs_input')).toMatchObject({ kind: 'question', confidence: 'exact', tool: 'AskUserQuestion' })
  expect(w.types()).not.toContain('cleared')
  release()
  await call
  await w.delivered()
  expect(w.types()).toContain('cleared')
})

test('pending question gates delivery even without a running main turn', async ($, on) => {
  const w = world(on, { task: 'T1', deliver: true })
  let release = () => {}
  on('tool.call', async () => {
    await new Promise<void>(resolve => { release = resolve })
    return { result: {} }
  })
  await $.session.start(START)
  const call = $.tool.call({ tool: 'AskUserQuestion', questions: [] } as never)
  await w.clock.settle()
  await w.clock.advance(5_000)
  await w.delivered()
  expect(w.submitted).toEqual([])
  release()
  await call
  await w.clock.advance(5_000)
  await w.delivered()
  expect(w.submitted).toEqual(['INTEGRATION_NOTICE [argus-delivery:hera:1]'])
})

test('a hook failure inside the daemon path never breaks the session', async ($, on) => {
  world(on, { task: 'T1', denyFetch: true })
  await $.session.start(START)
  const r = await $.turn.start({ text: 'x', turnId: 't' })
  expect(r.turnId).toBe('t')
})

test('a daemon that never answers does not delay any hook', async ($, on) => {
  const w = world(on, { task: 'T1', hangFetch: true })
  // Every hook here awaits only the engine's own calls: were delivery awaited,
  // these would sit on the hung fetch (until its 2s timeout, twice per attempt).
  await $.session.start(START)
  await $.turn.start({ text: 'x', turnId: 't1' })
  await $.turn.complete({ turnId: 't1', reason: 'answer', durationMs: 1, isAborted: false, answer: '' })
  expect(w.fetches()).toBeGreaterThan(0)
  expect(w.clock.now()).toBe(1_000_000) // no virtual time had to pass
})

// The permission-ask path (tool.check "ask" + a call still pending after the
// grace) is covered at the unit level in tracker.test.ts. It cannot be driven
// here: the harness mints a different tool_use_id for the plugin's tool.call
// than a test can name on tool.check, so the two never meet. It is verified
// live (see context/knowledge/gotchas/bridge.md).
