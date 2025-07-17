// src/plugins/globalPaste.ts
import { PluginObject } from 'vue'

const GlobalPaste: PluginObject<undefined> = {
  install() {
    document.addEventListener(
      'keydown',
      async (e: KeyboardEvent) => {
        if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'v') {
          e.preventDefault()
          try {
            const text = await navigator.clipboard.readText()
            await fetch('http://localhost:10001/computer/paste', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ text }),
            })
          } catch (err) {
            console.error('paste proxy failed', err)
          }
        }
      },
      { capture: true }
    )
  },
}

export default GlobalPaste
