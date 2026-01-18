<template>
  <div v-if="enabled && showOverlay && hasElements" ref="overlay" class="dom-overlay" :class="{ disabled: tempDisabled }">
    <div
      v-for="el in transformedElements"
      :key="el.id"
      class="dom-input"
      :style="getElementStyle(el)"
      @touchstart="onInputTap"
      @mousedown="onInputTap"
    />
  </div>
</template>

<style lang="scss" scoped>
.dom-overlay {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  pointer-events: none;
  z-index: 110;

  &.disabled {
    pointer-events: none !important;
    .dom-input {
      pointer-events: none !important;
    }
  }
}

.dom-input {
  position: absolute;
  pointer-events: auto;
  cursor: text;
  background: rgba(139, 92, 246, 0.1);
  border: 1px solid rgba(139, 92, 246, 0.3);
  border-radius: 3px;
}
</style>

<script lang="ts">
import { Component, Vue, Prop } from 'vue-property-decorator'
import { DomElement, DomWindowBounds } from '~/neko/dom-types'

interface TransformedElement extends DomElement {
  screenX: number
  screenY: number
}

@Component({
  name: 'dom-overlay',
})
export default class extends Vue {
  @Prop({ type: Number, default: 1920 }) screenWidth!: number
  @Prop({ type: Number, default: 1080 }) screenHeight!: number
  @Prop({ type: Boolean, default: true }) showOverlay!: boolean

  tempDisabled = false

  get enabled(): boolean {
    return this.$accessor.dom.enabled
  }

  get elements(): DomElement[] {
    return this.$accessor.dom.elements
  }

  get hasElements(): boolean {
    return this.$accessor.dom.hasElements
  }

  get windowBounds(): DomWindowBounds {
    return this.$accessor.dom.windowBounds
  }

  get transformedElements(): TransformedElement[] {
    const bounds = this.windowBounds
    const offsetX = bounds.x + bounds.chromeLeft
    const offsetY = bounds.y + bounds.chromeTop

    return this.elements.map((el) => ({
      ...el,
      screenX: offsetX + el.rect.x,
      screenY: offsetY + el.rect.y,
    }))
  }

  getElementStyle(el: TransformedElement): Record<string, string> {
    const xPercent = (el.screenX / this.screenWidth) * 100
    const yPercent = (el.screenY / this.screenHeight) * 100
    const wPercent = (el.rect.w / this.screenWidth) * 100
    const hPercent = (el.rect.h / this.screenHeight) * 100

    return {
      left: `${xPercent}%`,
      top: `${yPercent}%`,
      width: `${wPercent}%`,
      height: `${hPercent}%`,
    }
  }

  onInputTap(e: MouseEvent | TouchEvent) {
    e.preventDefault()
    e.stopPropagation()

    // Get click coordinates
    let clientX: number, clientY: number
    if ('touches' in e && e.touches.length > 0) {
      clientX = e.touches[0].clientX
      clientY = e.touches[0].clientY
    } else if ('clientX' in e) {
      clientX = e.clientX
      clientY = e.clientY
    } else {
      return
    }

    // Find the neko overlay textarea - this is what handles input events
    const overlay = document.querySelector('.player-container .overlay') as HTMLTextAreaElement

    // Focus the textarea to trigger mobile keyboard
    if (overlay) {
      overlay.focus()

      // Create event options
      const eventInit: MouseEventInit = {
        bubbles: true,
        cancelable: true,
        clientX,
        clientY,
        screenX: clientX,
        screenY: clientY,
        view: window,
        button: 0,
        buttons: 1,
      }

      // Temporarily enable pointer events on overlay if needed
      const oldPointerEvents = overlay.style.pointerEvents
      overlay.style.pointerEvents = 'auto'

      // Dispatch mouse events to simulate a click
      overlay.dispatchEvent(new MouseEvent('mousedown', eventInit))
      setTimeout(() => {
        overlay.dispatchEvent(new MouseEvent('mouseup', { ...eventInit, buttons: 0 }))
        // Restore pointer events
        overlay.style.pointerEvents = oldPointerEvents
      }, 20)
    }

    // Temporarily disable dom overlay for follow-up interactions
    this.tempDisabled = true
    setTimeout(() => {
      this.tempDisabled = false
    }, 500)
  }
}
</script>
