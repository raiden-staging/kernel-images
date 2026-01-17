/**
 * Ghost DOM Sync Types
 *
 * TypeScript interfaces for the ghost DOM overlay system that mirrors
 * interactive elements from the remote browser.
 */

export interface GhostElement {
  /** Unique identifier for the element (e.g., "g0", "g1") */
  id: string
  /** HTML tag name (input, button, a, select, textarea, etc.) */
  tag: string
  /** Bounding rectangle in viewport coordinates */
  rect: {
    x: number
    y: number
    w: number
    h: number
  }
  /** CSS z-index value */
  z: number
}

export interface GhostViewport {
  /** Viewport width in pixels */
  w: number
  /** Viewport height in pixels */
  h: number
  /** Horizontal scroll position (scrollX) */
  sx: number
  /** Vertical scroll position (scrollY) */
  sy: number
}

export interface GhostSyncPayload {
  /** Monotonically increasing sequence number */
  seq: number
  /** Timestamp when the sync was generated (ms since epoch) */
  ts: number
  /** Array of interactive elements with their bounding boxes */
  elements: GhostElement[]
  /** Current viewport dimensions and scroll position */
  viewport: GhostViewport
  /** Current page URL */
  url: string
}

export interface GhostWebSocketMessage {
  event: 'ghost/sync' | 'ghost/update' | 'ghost/start' | 'ghost/stop'
  data?: GhostSyncPayload
}
