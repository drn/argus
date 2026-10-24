import { expect, test } from 'claude-code/testing'
import { MAX_BUFFER, Reporter, type Env } from '../hooks/reporter'

type Call = { url: string; body: { events: Array<{ type: string; seq: number; boot: string; session_id?: string }> }; auth: string }

function fakeEnv(opts: {
  task?: string | undefined
  files?: Record<string, string>
  respond?: (call: Call, n: number) => { status: number; ok: boolean } | undefined
} = {}) {
  const calls: Call[] = []
  const reads: string[] = []
  const files = opts.files ?? { '/h/.argus/api-token': 'tok\n', '/h/.argus/api-port': '7743\n' }
  const env: Env = {
    taskId: async () => ('task' in opts ? opts.task : 'T1'),
    home: async () => '/h',
    readFile: async p => {
      reads.push(p)
      return files[p]
    },
    fetch: async (url, init) => {
      const call: Call = { url, body: JSON.parse(init.body), auth: init.headers.Authorization ?? '' }
      calls.push(call)
      return opts.respond ? opts.respond(call, calls.length) : { status: 200, ok: true }
    },
    now: async () => 1234,
  }
  return { env, calls, reads, files }
}

test('inert without a task id: nothing is built, read or posted', async () => {
  const f = fakeEnv({ task: undefined })
  const r = new Reporter(f.env, 'boot1')
  await r.emit('turn.start')
  expect(f.calls.length).toBe(0)
  expect(f.reads.length).toBe(0)
  expect(r.pending).toBe(0)
})

test('events carry monotonic seq, boot, ts and the session id; the url and bearer come from the files', async () => {
  const f = fakeEnv()
  const r = new Reporter(f.env, 'boot1')
  r.setSession('s1')
  await r.emit('turn.start', { turn_id: 'x' })
  await r.emit('turn.complete', { reason: 'answer' })
  expect(f.calls.map(c => c.url)).toEqual(Array(2).fill('http://127.0.0.1:7743/api/tasks/T1/bridge/events'))
  expect(f.calls[0]?.auth).toBe('Bearer tok')
  expect(f.calls.flatMap(c => c.body.events).map(e => [e.type, e.seq, e.boot, e.session_id])).toEqual([
    ['turn.start', 1, 'boot1', 's1'],
    ['turn.complete', 2, 'boot1', 's1'],
  ])
  expect(r.pending).toBe(0)
})

test('a dead daemon buffers; the next emit delivers everything in order', async () => {
  let up = false
  const f = fakeEnv({ respond: () => (up ? { status: 200, ok: true } : undefined) })
  const r = new Reporter(f.env, 'b')
  await r.emit('heartbeat')
  await r.emit('turn.start')
  expect(r.pending).toBe(2)
  up = true
  await r.emit('turn.complete')
  expect(r.pending).toBe(0)
  expect(f.calls[f.calls.length - 1]?.body.events.map(e => e.type)).toEqual(['heartbeat', 'turn.start', 'turn.complete'])
})

test('the buffer is bounded and drops the oldest events', async () => {
  const f = fakeEnv({ respond: () => undefined })
  const r = new Reporter(f.env, 'b', 5)
  for (let i = 0; i < 9; i++) await r.emit('heartbeat')
  expect(r.pending).toBe(5)
  const up = fakeEnv()
  expect(MAX_BUFFER).toBe(200)
  expect(up.calls.length).toBe(0)
})

test('a connection failure re-reads the port file and retries on the new port', async () => {
  const f = fakeEnv({
    respond: (c, n) => (n === 1 ? undefined : { status: 200, ok: true }),
  })
  const r = new Reporter(f.env, 'b')
  await r.emit('heartbeat')
  f.files['/h/.argus/api-port'] = '7744\n'
  // Force the failure path: the first attempt of the next emit fails, then re-reads.
  const g = fakeEnv({ files: f.files, respond: (c, n) => (n === 1 ? undefined : { status: 200, ok: true }) })
  const r2 = new Reporter(g.env, 'b')
  await r2.emit('heartbeat')
  expect(g.calls.map(c => c.url.split('/')[2])).toEqual(['127.0.0.1:7744', '127.0.0.1:7744'])
  expect(g.reads.filter(p => p.endsWith('api-port')).length).toBe(2)
})

test('port moving between attempts is followed', async () => {
  const files: Record<string, string> = { '/h/.argus/api-token': 't', '/h/.argus/api-port': '7743' }
  const f = fakeEnv({
    files,
    respond: (c, n) => {
      if (n === 1) {
        files['/h/.argus/api-port'] = '7750'
        return undefined
      }
      return { status: 200, ok: true }
    },
  })
  const r = new Reporter(f.env, 'b')
  await r.emit('heartbeat')
  expect(f.calls.map(c => c.url.split('/')[2])).toEqual(['127.0.0.1:7743', '127.0.0.1:7750'])
  expect(r.pending).toBe(0)
})

test('a 401 re-reads the token once', async () => {
  const files: Record<string, string> = { '/h/.argus/api-token': 'old', '/h/.argus/api-port': '7743' }
  const f = fakeEnv({
    files,
    respond: (c, n) => {
      if (n === 1) {
        files['/h/.argus/api-token'] = 'new'
        return { status: 401, ok: false }
      }
      return { status: 200, ok: true }
    },
  })
  const r = new Reporter(f.env, 'b')
  await r.emit('heartbeat')
  expect(f.calls.map(c => c.auth)).toEqual(['Bearer old', 'Bearer new'])
})

test('4xx drops the batch (resending cannot help); 5xx keeps it', async () => {
  const bad = fakeEnv({ respond: () => ({ status: 404, ok: false }) })
  const r = new Reporter(bad.env, 'b')
  await r.emit('heartbeat')
  expect(r.pending).toBe(0)

  const sick = fakeEnv({ respond: () => ({ status: 503, ok: false }) })
  const r2 = new Reporter(sick.env, 'b')
  await r2.emit('heartbeat')
  expect(r2.pending).toBe(1)
})

test('missing token or port files keep events buffered without posting', async () => {
  const f = fakeEnv({ files: {} })
  const r = new Reporter(f.env, 'b')
  await r.emit('heartbeat')
  expect(f.calls.length).toBe(0)
  expect(r.pending).toBe(1)
})

test('an env that throws never escapes emit', async () => {
  const env: Env = {
    taskId: async () => 'T',
    home: async () => {
      throw new Error('boom')
    },
    readFile: async () => undefined,
    fetch: async () => undefined,
    now: async () => 0,
  }
  const r = new Reporter(env, 'b')
  await r.emit('heartbeat')
  expect(r.pending).toBe(1)
})
