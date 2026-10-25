import { expect, test } from 'claude-code/testing'
import { MEASURE_MIN_INTERVAL_MS, MeasureThrottle, measureFields } from '../hooks/measure'

test('measureFields maps context, cost and every rate limit window', () => {
  expect(
    measureFields({
      context: { tokens: 1200, percent: 3, window: 200000 },
      cost: { usd: 0.5 },
      rateLimits: [{ kind: 'five_hour', percentUsed: 41, resetsAt: '2026-10-24T20:00:00Z' }, { kind: 'seven_day', percentUsed: 9 }],
    }),
  ).toEqual({
    context: { tokens: 1200, percent: 3, window: 200000 },
    cost_usd: 0.5,
    rate_limits: [
      { kind: 'five_hour', percent_used: 41, resets_at: '2026-10-24T20:00:00Z' },
      { kind: 'seven_day', percent_used: 9, resets_at: undefined },
    ],
  })
})

test('measureFields omits what the engine does not have', () => {
  expect(measureFields({})).toEqual({ rate_limits: [] })
})

test('the throttle sends the first measurement, holds the rest, and flush releases the latest', () => {
  const th = new MeasureThrottle()
  expect(th.offer({ n: 1 }, 0)).toEqual({ n: 1 })
  expect(th.offer({ n: 2 }, 1000)).toBe(undefined)
  expect(th.offer({ n: 3 }, 2000)).toBe(undefined)
  expect(th.flush(3000)).toEqual({ n: 3 })
  expect(th.flush(4000)).toBe(undefined)
})

test('the throttle releases again once the interval has passed', () => {
  const th = new MeasureThrottle()
  th.offer({ n: 1 }, 0)
  expect(th.offer({ n: 2 }, MEASURE_MIN_INTERVAL_MS)).toEqual({ n: 2 })
})

test('an unchanged measurement is not resent', () => {
  const th = new MeasureThrottle()
  th.offer({ n: 1 }, 0)
  expect(th.offer({ n: 1 }, MEASURE_MIN_INTERVAL_MS * 2)).toBe(undefined)
})
