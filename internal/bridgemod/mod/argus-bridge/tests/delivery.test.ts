import { expect, test } from 'claude-code/testing'
import { Delivery, type DeliveryEnv } from '../hooks/delivery'

function world() {
  const calls: string[] = [], submitted: string[] = []
  let idle = true
  const env: DeliveryEnv = {
    idle: () => idle,
    request: async path => {
      calls.push(path)
      if (path === 'deliveries') return { delivery: { token: 'c1', text: 'notice [argus-delivery:hera:1]' } }
      if (path === 'deliveries/authorize') return { authorized: true }
      return { acked: true }
    },
    submit: async text => { submitted.push(text); return true },
  }
  return { env, calls, submitted, busy: () => { idle = false } }
}

test('idle delivery submits once and retries only ack', async () => {
  const w = world(), d = new Delivery(w.env)
  await d.poll()
  await d.poll()
  expect(w.submitted.length).toBe(1)
  expect(w.calls).toEqual(['deliveries','deliveries/authorize','deliveries/ack','deliveries'])
})

test('busy or pending dialog does not pull', async () => {
  const w = world(), d = new Delivery(w.env)
  w.busy()
  await d.poll()
  expect(w.calls.length).toBe(0)
})

test('becoming busy during authorization never submits', async () => {
  const w = world(), request = w.env.request
  w.env.request = async (path, token) => {
    const value = await request(path, token)
    if (path === 'deliveries/authorize') w.busy()
    return value
  }
  await new Delivery(w.env).poll()
  expect(w.submitted.length).toBe(0)
})

test('failed ack is retried without resubmitting', async () => {
  const w = world(), request = w.env.request
  let acks = 0
  w.env.request = async (path, token) => path === 'deliveries/ack' && acks++ === 0 ? undefined : request(path, token)
  const d = new Delivery(w.env)
  await d.poll()
  await d.poll()
  expect(w.submitted.length).toBe(1)
})

test('a completed or cancelled ack does not block later delivery', async () => {
  const w = world(), request = w.env.request
  let claims = 0, acks = 0
  w.env.request = async (path, token) => {
    if (path === 'deliveries') return { delivery: { token: `c${++claims}`, text: `notice${claims}` } }
    if (path === 'deliveries/ack') return ++acks === 1 ? undefined : { acked: false }
    return request(path, token)
  }
  const d = new Delivery(w.env)
  await d.poll()
  await d.poll()
  expect(w.submitted).toEqual(['notice1', 'notice2'])
})

test('single flight stops concurrent polling from submitting twice', async () => {
  const w = world(), d = new Delivery(w.env)
  await Promise.all([d.poll(), d.poll(), d.poll()])
  expect(w.submitted.length).toBe(1)
})

test('denied authorization and submission refusal do not acknowledge', async () => {
  for (const stage of ['authorization', 'submit']) {
    const w = world(), request = w.env.request
    if (stage === 'authorization') w.env.request = async (path, token) => path === 'deliveries/authorize' ? { authorized: false } : request(path, token)
    else w.env.submit = async () => false
    await new Delivery(w.env).poll()
    expect(w.calls.includes('deliveries/ack')).toBe(false)
  }
})
