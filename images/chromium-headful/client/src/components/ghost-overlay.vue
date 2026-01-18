<template>
  <svg
    v-if="enabled && hasElements"
    class="ghost-overlay"
    :viewBox="viewBox"
    preserveAspectRatio="none"
  >
    <rect
      v-for="el in transformedElements"
      :key="el.id"
      :x="el.screenX"
      :y="el.screenY"
      :width="el.rect.w"
      :height="el.rect.h"
      :fill="getColor(el.tag)"
      fill-opacity="0.15"
      :stroke="getColor(el.tag)"
      stroke-opacity="0.5"
      stroke-width="2"
    />
  </svg>
</template>

<style lang="scss" scoped>
.ghost-overlay {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  pointer-events: none;
  z-index: 50;
}
</style>

<script lang="ts">
import { Component, Vue, Prop } from 'vue-property-decorator'
import { GhostElement, GhostWindowBounds } from '~/neko/ghost-types'

// Color mapping for different element types
const TAG_COLORS: Record<string, string> = {
  // Input fields - blue
  input: '#3b82f6',
  textarea: '#3b82f6',
  // Buttons - green
  button: '#22c55e',
  // Links - purple
  a: '#8b5cf6',
  // Select/dropdowns - amber
  select: '#f59e0b',
  // Address bar - orange/red
  addressbar: '#ef4444',
  // Default - gray
  default: '#6b7280',
}

interface TransformedElement extends GhostElement {
  screenX: number
  screenY: number
}

@Component({
  name: 'ghost-overlay',
})
export default class extends Vue {
  // Screen dimensions from video resolution (passed from parent or use defaults)
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

  get viewport() {
    return this.$accessor.ghost.viewport
  }

  get windowBounds(): GhostWindowBounds {
    return this.$accessor.ghost.windowBounds
  }

  get viewBox(): string {
    // ViewBox matches the screen/video dimensions
    // This is what the video stream shows (the full X11 display)
    return `0 0 ${this.screenWidth} ${this.screenHeight}`
  }

  get transformedElements(): TransformedElement[] {
    // Transform element positions from viewport coordinates to screen coordinates
    // The video shows the full screen, elements are positioned relative to viewport
    // Screen position = window position + chrome offset + element viewport position
    const bounds = this.windowBounds
    const offsetX = bounds.x + bounds.chromeLeft
    const offsetY = bounds.y + bounds.chromeTop

    return this.elements.map((el) => ({
      ...el,
      screenX: offsetX + el.rect.x,
      screenY: offsetY + el.rect.y,
    }))
  }

  getColor(tag: string): string {
    // Check for exact match first
    if (tag in TAG_COLORS) {
      return TAG_COLORS[tag]
    }
    // Default color
    return TAG_COLORS.default
  }
}
</script>
