import { j } from './utils.js'

describe('input', () => {
  it('gracefully errors for invalid payloads', async () => {
    const r = await j('/input/mouse/move', { method: 'POST', body: JSON.stringify({}) })
    expect(r.status).toBe(400)
  })
})

