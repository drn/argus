import { expect, test } from 'claude-code/testing'
import { GRACE_MS, NeedsInputTracker } from '../hooks/tracker'

test('AskUserQuestion is needs-input from call start to call end', () => {
  const t = new NeedsInputTracker()
  expect(t.callStart('a', 'AskUserQuestion')).toEqual({ type: 'needs_input', kind: 'question', confidence: 'exact', tool: 'AskUserQuestion', toolUseId: 'a' })
  expect(t.active).toBe(true)
  expect(t.callEnd('a')).toEqual({ type: 'cleared', toolUseId: 'a' })
  expect(t.active).toBe(false)
})

test('ExitPlanMode is a plan dialog', () => {
  const t = new NeedsInputTracker()
  expect(t.callStart('p', 'ExitPlanMode')).toEqual({ type: 'needs_input', kind: 'plan', confidence: 'exact', tool: 'ExitPlanMode', toolUseId: 'p' })
})

test('an ask verdict only counts after the grace period and while still pending', () => {
  const t = new NeedsInputTracker()
  expect(t.callStart('b', 'Bash')).toBe(null)
  t.markAsk('b', 1000)
  expect(t.poll(1000 + GRACE_MS - 1)).toBe(null)
  expect(t.poll(1000 + GRACE_MS)).toEqual({ type: 'needs_input', kind: 'permission', confidence: 'pending-or-running', tool: 'Bash', toolUseId: 'b' })
  expect(t.callEnd('b')).toEqual({ type: 'cleared', toolUseId: 'b' })
})

test('an auto-approved ask (call ends inside the grace) never reports', () => {
  const t = new NeedsInputTracker()
  t.callStart('b', 'Bash')
  t.markAsk('b', 1000)
  expect(t.callEnd('b')).toBe(null)
  expect(t.poll(1000 + GRACE_MS * 2)).toBe(null)
  expect(t.active).toBe(false)
})

test('a verdict seen before the call start still qualifies it', () => {
  const t = new NeedsInputTracker()
  t.markAsk('b', 1000)
  t.callStart('b', 'mcp__srv__tool')
  expect(t.poll(1000 + GRACE_MS)?.type).toBe('needs_input')
})

test('a stray early verdict for a call that never starts is forgotten at its end', () => {
  const t = new NeedsInputTracker()
  t.markAsk('x', 1000)
  expect(t.callEnd('x')).toBe(null)
  t.callStart('x', 'Bash')
  expect(t.poll(1000 + GRACE_MS * 3)).toBe(null)
})

test('a call without an ask verdict never qualifies', () => {
  const t = new NeedsInputTracker()
  t.callStart('b', 'Bash')
  expect(t.poll(1e9)).toBe(null)
  expect(t.nextDue()).toBe(undefined)
})

test('needs-input is edge-triggered: only the first of several pending dialogs reports, clear waits for the last', () => {
  const t = new NeedsInputTracker()
  expect(t.callStart('q1', 'AskUserQuestion')?.type).toBe('needs_input')
  expect(t.callStart('q2', 'ExitPlanMode')).toBe(null)
  expect(t.callEnd('q1')).toBe(null)
  expect(t.callEnd('q2')).toEqual({ type: 'cleared', toolUseId: 'q2' })
})

test('a qualified ask is not re-reported by a later poll', () => {
  const t = new NeedsInputTracker()
  t.callStart('b', 'Bash')
  t.markAsk('b', 0)
  expect(t.poll(GRACE_MS)?.type).toBe('needs_input')
  expect(t.poll(GRACE_MS * 2)).toBe(null)
})

test('nextDue names the earliest unqualified ask', () => {
  const t = new NeedsInputTracker()
  t.callStart('a', 'Bash')
  t.callStart('b', 'Bash')
  t.markAsk('b', 500)
  t.markAsk('a', 200)
  expect(t.nextDue()).toBe(200 + GRACE_MS)
})

test('turn end resets a stuck dialog and reports the clear once', () => {
  const t = new NeedsInputTracker()
  t.callStart('q', 'AskUserQuestion')
  expect(t.reset()).toEqual({ type: 'cleared', toolUseId: 'q' })
  expect(t.reset()).toBe(null)
  expect(t.active).toBe(false)
})
