import { expect, mock, test } from 'claude-code/testing'
import type { On } from 'claude-code'
import type { Snapshot } from '../types'
import { bandText } from '../hooks/ui'

const START = { cwd: '/work', surface: 'terminal', isInteractive: true } as const
const BAND = { plugin: 'argus-bridge', component: 'AbovePrompt', props: { hasSurvey: false, isWorking: false, maxRows: 8, bodyColumns: 100, scroll: { offset: 0, bodyRows: 8 }, view: {} } } as const
const COMMAND = { args: '', origin: { kind: 'composer' } as const, presentation: { isFullscreen: false, columns: 100 } }

function world(on: On, opts: { engineBeneath?: boolean } = {}) {
  const clock = mock.clock(on, { now: 1_000_000 })
  mock.env(on, { HOME: '/h', ARGUS_TASK_ID: 'T1', ARGUS_BRIDGE_GENERATION: 'launch' })
  let snapshot = { settings: { enabled: true, delivery: true, band: true, toasts: true }, live: true, context: { tokens: 250000, percent: 25, window: 1000000 }, bindings: [{ binding_id: 9, role: 'coord', kind: 'coordinator', orchestrator: 'team', status: 'working', unread: 0, budget: 300000 }] as { binding_id: number; role: string; kind: string; orchestrator: string; status: string; unread: number; budget: number; workers?: { working: number; blocked: number } }[], unread: 0, messages: [] as { id: string; sender: string; tldr: string; sent_at: string }[] }
  const toasts: string[] = []
  const submits: string[] = []
  const urls: string[] = []
  const registered: string[] = []
  let unavailable = false
  on('fs.read', (_, e) => ({ value: e.path.endsWith('api-token') ? 'tok' : '7743' }))
  on('http.fetch', (_, e) => { urls.push(e.url); return { value: { status: unavailable ? 503 : 200, ok: !unavailable, headers: {}, text: JSON.stringify(e.url.endsWith('/ui') ? snapshot : {}) } } })
  on('session.id', () => ({ value: 's1' }))
  on('session.model', () => ({ value: 'opus' }))
  on('session.cwd', () => ({ value: '/work' }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('turn.start', (_, e) => ({ turnId: e.turnId }))
  on('turn.complete', (_, e) => ({ text: e.answer }))
  on('session.measure', (_, e) => ({ changed: e.changed }))
  on('command.register', (_, e) => { registered.push(e.name); return { value: { command: e.name } } })
  on('ui.toast', (_, e) => { toasts.push(e.text); return { value: undefined } })
  on('prompt.submit', (_, e) => { submits.push(e.text); return { text: e.text } })
  on('ui.render', ($, e) => { if (opts.engineBeneath) return { type: 'engine', ref: 0 } as const; const { Text } = $.ui.resolve(e); return <Text>BENEATH</Text> })
  const refresh = async () => { await clock.advance(15000); await clock.settle(); await clock.settle() }
  return { clock, refresh, snapshot, toasts, submits, urls, registered, fail: () => { unavailable = true }, recover: () => { unavailable = false } }
}

test('band composes and live flips on/off on every surface', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  await w.refresh()
  for (const surface of ['terminal', 'desktop', 'vscode', 'mobile'] as const) {
    const ui = await $.ui.mount({ ...BAND, surface })
    expect((await ui.find({ type: 'Text', text: /argus:/ }))?.text).toBe('argus: 0 unread • budget 83% • working')
    expect((await ui.find({ type: 'Text', text: /BENEATH/ }))?.text).toBe('BENEATH')
    w.snapshot.settings.band = false
    await w.refresh()
    expect(await ui.find({ type: 'Text', text: /argus:/ })).toBeUndefined()
    expect((await ui.find({ type: 'Text', text: /BENEATH/ }))?.text).toBe('BENEATH')
    w.snapshot.settings.band = true
    await w.refresh()
    expect((await ui.find({ type: 'Text', text: /argus:/ }))?.text).toContain('argus: 0 unread')
    await ui.unmount()
  }
})

test('unbound, disabled and unavailable band yields beneath; render never fetches', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  await w.refresh()
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  const bindings = w.snapshot.bindings
  w.snapshot.bindings = []
  await w.refresh()
  expect(await ui.find({ type: 'Text', text: /argus:/ })).toBeUndefined()
  w.snapshot.bindings = bindings
  await w.refresh()
  expect((await ui.find({ type: 'Text', text: /argus:/ }))?.text).toContain('argus: 0 unread')
  w.snapshot.settings.enabled = false
  await w.refresh()
  expect(await ui.find({ type: 'Text', text: /argus:/ })).toBeUndefined()
  expect((await ui.find({ type: 'Text', text: /BENEATH/ }))?.text).toBe('BENEATH')
  w.snapshot.settings.enabled = true
  await w.refresh()
  expect((await ui.find({ type: 'Text', text: /argus:/ }))?.text).toContain('argus: 0 unread')
  w.fail()
  await w.refresh()
  expect((await ui.find({ type: 'Text', text: /argus:/ }))?.text).toBe('argus: 0 unread • budget 83% • working • ⚠ bridge offline')
  w.recover()
  await w.refresh()
  expect((await ui.find({ type: 'Text', text: /argus:/ }))?.text).toBe('argus: 0 unread • budget 83% • working')
  const n = w.urls.length
  await ui.find({ type: 'Text', text: /BENEATH/ })
  expect(w.urls.length).toBe(n)
  await ui.unmount()
})

test('toasts baseline old mail, deduplicate arrivals and honor live switches', async ($, on) => {
  const w = world(on)
  const mail = (id: string) => ({ id, sender: 'worker', tldr: 'Ready for review', sent_at: '1970-01-01T00:15:00Z' })
  w.snapshot.messages = [mail('hera:old')]
  await $.session.start(START)
  await w.refresh()
  expect(w.toasts).toEqual([])
  await $.turn.start({ text: 'busy', turnId: 't1' })
  w.snapshot.messages.push(mail('hera:1'))
  await w.refresh()
  expect(w.toasts.length).toBe(1)
  expect(w.toasts[0]).toContain('worker')
  expect(w.toasts[0]).toContain('Ready for review')
  await w.refresh()
  expect(w.toasts.length).toBe(1)
  w.snapshot.settings.toasts = false
  w.snapshot.messages.push(mail('task:2'))
  await w.refresh()
  expect(w.toasts.length).toBe(1)
  w.snapshot.settings.toasts = true
  await w.refresh()
  expect(w.toasts.length).toBe(1)
  w.snapshot.messages.push(mail('task:3'))
  await w.refresh()
  expect(w.toasts.length).toBe(2)
  w.snapshot.settings.enabled = false
  w.snapshot.messages.push(mail('hera:4'))
  await w.refresh()
  expect(w.toasts.length).toBe(2)
  expect(w.submits).toEqual([])
})

test('local commands list unread mail and status without a model prompt or write', async ($, on) => {
  const w = world(on)
  w.snapshot.messages = [{ id: 'hera:1', sender: 'worker', tldr: 'Review me', sent_at: '1970-01-01T00:15:00Z' }]
  w.snapshot.unread = 1
  await $.session.start(START)
  expect(w.registered).toEqual(['hera-inbox', 'hera-status'])
  for (let i = 0; i < 2; i++) {
    const reply = await $.command.run({ ...COMMAND, command: 'hera-inbox' })
    expect(reply.text).toContain('worker')
    expect(reply.text).toContain('Review me')
    expect(reply.text).toContain('1m ago')
  }
  const reply = await $.command.run({ ...COMMAND, command: 'hera-status' })
  for (const text of ['coord', 'coordinator', 'team', 'working', '9', '1 unread from worker', 'context 25%', 'budget 83%', 'live']) expect(reply.text).toContain(text)
  expect(w.submits).toEqual([])
  expect(w.urls.every(u => u.endsWith('/events') || u.endsWith('/ui'))).toBe(true)
  w.fail()
  expect((await $.command.run({ ...COMMAND, command: 'hera-status' })).text).toContain('Daemon unreachable: showing the last snapshot.')
})

test('narrow band stays one truncate-end Text and yields to surveys', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  await w.refresh()
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal', props: { ...BAND.props, bodyColumns: 12 } })
  // One row: the outer Text wraps; its styled segments nest inside it.
  const rows = (await ui.findAll({ type: 'Text', text: /argus:/ })).filter(t => t.props.wrap !== undefined)
  expect(rows.length).toBe(1)
  expect(rows[0]?.props.wrap).toBe('truncate-end')
  expect(rows[0]?.text).toBe('argus: 0 unread • budget 83% • working')
  await ui.redraw({ ...BAND.props, bodyColumns: 12, hasSurvey: true })
  expect(await ui.find({ type: 'Text', text: /argus:/ })).toBeUndefined()
  expect((await ui.find({ type: 'Text', text: /BENEATH/ }))?.text).toBe('BENEATH')
  await ui.unmount()
})

test('turn completion and measurements refresh a mounted band before the slow timer', async ($, on) => {
  const w = world(on)
  await $.session.start(START)
  await w.clock.advance(0)
  await w.clock.settle()
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  w.snapshot.unread = 4
  await $.turn.complete({ turnId: 't1', reason: 'answer', durationMs: 1, isAborted: false, answer: '' })
  await w.clock.advance(0)
  await w.clock.settle()
  expect((await ui.find({ type: 'Text', text: /argus:/ }))?.text).toContain('4 unread')
  w.snapshot.context.tokens = 285000
  await $.session.measure({ context: { tokens: 285000, percent: 28.5, window: 1000000 }, rateLimits: [], changed: ['context'] })
  await w.clock.advance(0)
  await w.clock.settle()
  expect((await ui.find({ type: 'Text', text: /argus:/ }))?.text).toContain('budget 95%')
  await ui.unmount()
})

test('band omits the engine own empty component so no blank row sits above the prompt box', async ($, on) => {
  const w = world(on, { engineBeneath: true })
  await $.session.start(START)
  await w.refresh()
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect((await ui.find({ type: 'Text', text: /argus:/ }))?.text).toBe('argus: 0 unread • budget 83% • working')
  expect(await ui.find({ type: 'engine' })).toBeUndefined()
  await ui.redraw({ ...BAND.props, hasSurvey: true })
  expect(await ui.find({ type: 'Text', text: /argus:/ })).toBeUndefined()
  await ui.unmount()
})

test('a daemon unreachable before any snapshot shows no band', async ($, on) => {
  const w = world(on)
  w.fail()
  await $.session.start(START)
  await w.refresh()
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await ui.find({ type: 'Text', text: /argus:/ })).toBeUndefined()
  expect((await ui.find({ type: 'Text', text: /BENEATH/ }))?.text).toBe('BENEATH')
  await ui.unmount()
})

test('band styles its segments with theme tokens', async ($, on) => {
  const w = world(on)
  w.snapshot.unread = 2
  w.snapshot.messages = [
    { id: 'hera:1', sender: 'older', tldr: 'first', sent_at: '1970-01-01T00:10:00Z' },
    { id: 'hera:2', sender: 'newest', tldr: 'second', sent_at: '1970-01-01T00:15:00Z' },
  ]
  w.snapshot.context.tokens = 270000
  w.snapshot.bindings[0]!.status = 'blocked'
  w.snapshot.bindings[0]!.workers = { working: 2, blocked: 1 }
  await $.session.start(START)
  await w.refresh()
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  const line = await ui.find({ type: 'Text', text: /argus:/ })
  expect(line?.text).toBe('argus: 2 unread from newest • budget 90% • blocked • workers 2 working, 1 blocked')
  expect(line?.props.color).toBe('subtle')
  const part = async (text: string) => (await ui.findAll({ type: 'Text' })).find(t => t.text === text)?.props
  expect((await part('argus:'))?.color).toBe('ide')
  expect((await part(' 2 unread from newest'))?.color).toBe('warning')
  expect((await part(' 2 unread from newest'))?.weight).toBe('bold')
  expect((await part('budget 90%'))?.color).toBe('claude')
  expect((await part('blocked'))?.color).toBe('claude')
  expect((await part('1 blocked'))?.color).toBe('claude')
  w.snapshot.unread = 0
  w.snapshot.messages = []
  w.snapshot.context.tokens = 225000
  w.snapshot.bindings[0]!.status = 'done'
  w.snapshot.bindings[0]!.workers = { working: 0, blocked: 0 }
  await w.refresh()
  expect((await ui.find({ type: 'Text', text: /argus:/ }))?.text).toBe('argus: 0 unread • budget 75% • done')
  expect(await part(' 0 unread')).toBeUndefined()
  expect((await part('budget 75%'))?.color).toBe('warning')
  expect((await part('done'))?.color).toBe('success')
  w.snapshot.context.tokens = 222000
  await w.refresh()
  expect((await ui.find({ type: 'Text', text: /argus:/ }))?.text).toContain('budget 74%')
  expect(await part('budget 74%')).toBeUndefined()
  await ui.unmount()
})

test('band text omits zero worker counts, ignores non-coordinator roll-ups and joins statuses', async () => {
  const base: Snapshot = { settings: { enabled: true, delivery: true, band: true, toasts: true }, live: true, context: null, bindings: [], unread: 0, messages: [] }
  const coord = { binding_id: 1, role: 'c', kind: 'coordinator', orchestrator: 'o', status: 'working', unread: 0, budget: 300000 }
  expect(bandText({ ...base, bindings: [{ ...coord, workers: { working: 0, blocked: 3 } }] })).toBe('argus: 0 unread • budget ? • working • workers 3 blocked')
  expect(bandText({ ...base, bindings: [{ ...coord, workers: { working: 4, blocked: 0 } }] })).toBe('argus: 0 unread • budget ? • working • workers 4 working')
  expect(bandText({ ...base, bindings: [{ ...coord, budget: 0, workers: { working: 1, blocked: 0 } }, { ...coord, binding_id: 2, kind: 'worker', status: 'blocked', budget: 0, workers: { working: 9, blocked: 9 } }] })).toBe('argus: 0 unread • budget ? • working/blocked • workers 1 working')
  expect(bandText({ ...base, unread: 1, messages: [] })).toBe('argus: 1 unread')
})
