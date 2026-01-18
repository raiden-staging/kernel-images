<template>
  <div v-if="enabled && hasElements" class="ghost-overlay">
    <div
      v-for="el in transformedElements"
      :key="el.id"
      class="ghost-input"
      :style="getElementStyle(el)"
      @touchstart.prevent="onInputTap"
      @click.prevent="onInputTap"
    />
  </div>
</template>

<style lang="scss" scoped>
.ghost-overlay {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  pointer-events: none;
  z-index: 110; // Above the neko textarea overlay (z-index: 100)
}

.ghost-input {
  position: absolute;
  pointer-events: auto;
  cursor: text;
  // Subtle visual indicator for input fields
  background: rgba(59, 130, 246, 0.08);
  border: 1px solid rgba(59, 130, 246, 0.25);
  border-radius: 3px;
}
</style>

<script lang="ts">
import { Component, Vue, Prop } from 'vue-property-decorator'
import { GhostElement, GhostWindowBounds } from '~/neko/ghost-types'

interface TransformedElement extends GhostElement {
  screenX: number
  screenY: number
}

@Component({
  name: 'ghost-overlay',
})
export default class extends Vue {
  @Prop({ type: Number, default: 1920 }) screenWidth!: number
  @Prop({ type: Number, default: 1080 }) screenHeight!: number

  get enabled(): boolean {
    return this.$accessor.ghost.enabled
  }

  get elements(): GhostElement[] {
    return this.$accessor.ghost.elements
  }

  get hasElements(): boolean {
    return this.$accessor.ghost.hasElements
  }

  get windowBounds(): GhostWindowBounds {
    return this.$accessor.ghost.windowBounds
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
    // Convert from screen coordinates to percentage of video
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

  onInputTap() {
    // Find the neko overlay textarea and focus it to trigger mobile keyboard
    const overlay = document.querySelector('.player-container .overlay') as HTMLTextAreaElement
    if (overlay) {
      overlay.focus()
      // On iOS, we may need to programmatically show keyboard
      if ('virtualKeyboard' in navigator) {
        (navigator as any).virtualKeyboard.show()
      }
    }
  }
}
</script>
