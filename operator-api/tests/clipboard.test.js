import { j } from './utils.js'

describe('clipboard', () => {
  it('GET /clipboard responds even if tooling missing', async () => {
    const r = await j('/clipboard')
    expect(r.status).toBe(200)
    expect(['text', 'image']).toContain(r.body.type)
  })
})
