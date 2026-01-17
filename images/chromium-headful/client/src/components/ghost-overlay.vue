<template>
  <svg
    v-if="enabled && hasElements"
    class="ghost-overlay"
    :viewBox="viewBox"
    preserveAspectRatio="none"
  >
    <rect
      v-for="el in elements"
      :key="el.id"
      :x="el.rect.x"
      :y="el.rect.y"
      :width="el.rect.w"
      :height="el.rect.h"
      :fill="getColor(el.tag)"
      fill-opacity="0.12"
      :stroke="getColor(el.tag)"
      stroke-opacity="0.35"
      stroke-width="1"
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
import { Component, Vue } from 'vue-property-decorator'
import { GhostElement } from '~/neko/ghost-types'

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
  // Default - gray
  default: '#6b7280',
}

@Component({
  name: 'ghost-overlay',
})
export default class extends Vue {
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

  get viewBox(): string {
    // ViewBox matches the remote viewport dimensions
    return `0 0 ${this.viewport.w} ${this.viewport.h}`
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
