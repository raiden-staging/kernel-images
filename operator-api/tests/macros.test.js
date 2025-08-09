import { j, raw } from './utils.js'

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

  it('handles invalid macro creation requests', async () => {
    const invalidCreate = await j('/macros/create', {
      method: 'POST',
      body: JSON.stringify({
        name: '', // Missing name
        steps: []
      })
    })
    expect(invalidCreate.status).toBe(400)

    const noSteps = await j('/macros/create', {
      method: 'POST',
      body: JSON.stringify({
        name: 'test-macro',
        steps: [] // Empty steps
      })
    })
    expect(noSteps.status).toBe(400)
  })

  it('handles macro run requests', async () => {
    // Create a test macro first
    const create = await j('/macros/create', {
      method: 'POST',
      body: JSON.stringify({
        name: 'test-run-macro',
        steps: [{ action: 'sleep', ms: 5 }]
      })
    })
    expect(create.status).toBe(200)
    const macro_id = create.body.macro_id

    // Test run endpoint
    const run = await j('/macros/run', {
      method: 'POST',
      body: JSON.stringify({
        macro_id
      })
    })
    expect(run.status).toBe(200)

    // Test invalid run request
    const invalidRun = await j('/macros/run', {
      method: 'POST',
      body: JSON.stringify({
        macro_id: 'non-existent-id'
      })
    })
    expect(invalidRun.status).toBe(404)

    // Clean up
    await j(`/macros/${macro_id}`, { method: 'DELETE' })
  })

  it('handles get macro by ID', async () => {
    // Create a test macro
    const create = await j('/macros/create', {
      method: 'POST',
      body: JSON.stringify({
        name: 'get-by-id-test',
        steps: [{ action: 'sleep', ms: 10 }]
      })
    })
    expect(create.status).toBe(200)
    const macro_id = create.body.macro_id

    // Get macro by ID
    const getMacro = await j(`/macros/${macro_id}`)
    expect(getMacro.status).toBe(200)
    expect(getMacro.body.name).toBe('get-by-id-test')
    expect(Array.isArray(getMacro.body.steps)).toBe(true)

    // Test non-existent macro
    const getNonExistent = await j('/macros/non-existent-id')
    expect(getNonExistent.status).toBe(404)

    // Clean up
    await j(`/macros/${macro_id}`, { method: 'DELETE' })
  })

  it('rejects invalid requests to macro endpoints', async () => {
    // Test invalid method on create
    const invalidMethod = await raw('/macros/create', { method: 'GET' })
    expect(invalidMethod.status).toBe(405)

    // Test invalid JSON in create
    const invalidJson = await raw('/macros/create', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{invalid json'
    })
    expect(invalidJson.status).toBe(400)
  })
})
