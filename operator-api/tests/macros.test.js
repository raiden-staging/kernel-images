import { j } from './utils.js'

describe('macros', () => {
  it('create/list/delete macro (run skipped due to xdotool requirement)', async () => {
    const create = await j('/macros/create', {
      method: 'POST',
      body: JSON.stringify({
        name: 'type-hello',
        steps: [{ action: 'sleep', ms: 10 }, { action: 'keyboard.type', text: 'hello' }]
      })
    })
    expect(create.status).toBe(200)
    const macro_id = create.body.macro_id

    const list = await j('/macros/list')
    expect(list.status).toBe(200)
    expect(list.body.items.find(i => i.macro_id === macro_id)).toBeTruthy()

    const del = await j(`/macros/${macro_id}`, { method: 'DELETE' })
    expect(del.status).toBe(200)
  })
})
