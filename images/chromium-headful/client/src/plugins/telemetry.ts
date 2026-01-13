// Telemetry Vue Plugin - Integrates telemetry with Vue app lifecycle

import { PluginObject } from 'vue'
import {
  TelemetryConfig,
  DEFAULT_TELEMETRY_CONFIG,
  initTelemetry,
  getTelemetry,
  installErrorCollector,
  installPerformanceCollector,
  installConnectionCollector,
  getConnectionCollector,
  TelemetryService,
} from '~/telemetry'
import { NekoClient } from '~/neko'

// Runtime configuration interface
interface RuntimeTelemetryConfig {
  enabled?: boolean
  endpoint?: string
  debug?: boolean
  capture?: string // comma-separated event types or 'all'
}

// Global window augmentation for runtime config
declare global {
  interface Window {
    __NEKO_TELEMETRY_CONFIG__?: RuntimeTelemetryConfig
    $telemetry: TelemetryService
  }
}

declare module 'vue/types/vue' {
  interface Vue {
    $telemetry: TelemetryService
  }
}

/**
 * Parse boolean from string/undefined
 */
function parseBoolean(value: string | undefined | null, defaultValue: boolean): boolean {
  if (value === undefined || value === null || value === '') {
    return defaultValue
  }
  return value.toLowerCase() === 'true' || value === '1'
}

/**
 * Get telemetry configuration from multiple sources:
 * 1. Build-time environment variables (VUE_APP_TELEMETRY_*)
 * 2. Runtime window config (window.__NEKO_TELEMETRY_CONFIG__)
 * 3. Query parameters (?telemetry=true&telemetry_endpoint=...&telemetry_capture=...)
 *
 * Priority: Query params > Window config > Env vars > Defaults
 *
 * TELEMETRY_CAPTURE format (comma-separated):
 *   - 'all' (default): Capture all events
 *   - 'none': Capture no events
 *   - 'error_js,perf_fps,stream_*': Specific events or patterns
 *   - 'all,-perf_*': All except matching patterns
 */
function getTelemetryConfig(): Partial<TelemetryConfig> {
  const config: Partial<TelemetryConfig> = {}

  // Track capture from each source (last non-empty wins)
  let captureSpec: string | undefined

  // 1. Build-time environment variables
  const envEnabled = process.env.VUE_APP_TELEMETRY_ENABLED
  const envEndpoint = process.env.VUE_APP_TELEMETRY_ENDPOINT
  const envDebug = process.env.VUE_APP_TELEMETRY_DEBUG
  const envCapture = process.env.VUE_APP_TELEMETRY_CAPTURE

  if (envEnabled !== undefined) {
    config.enabled = parseBoolean(envEnabled, DEFAULT_TELEMETRY_CONFIG.enabled)
  }
  if (envEndpoint) {
    config.endpoint = envEndpoint
  }
  if (envDebug !== undefined) {
    config.debug = parseBoolean(envDebug, DEFAULT_TELEMETRY_CONFIG.debug)
  }
  if (envCapture) {
    captureSpec = envCapture
  }

  // 2. Runtime window configuration
  const windowConfig = window.__NEKO_TELEMETRY_CONFIG__
  if (windowConfig) {
    if (windowConfig.enabled !== undefined) {
      config.enabled = windowConfig.enabled
    }
    if (windowConfig.endpoint) {
      config.endpoint = windowConfig.endpoint
    }
    if (windowConfig.debug !== undefined) {
      config.debug = windowConfig.debug
    }
    if (windowConfig.capture) {
      captureSpec = windowConfig.capture
    }
  }

  // 3. Query parameters (highest priority)
  const urlParams = new URLSearchParams(window.location.search)

  const paramEnabled = urlParams.get('telemetry') ?? urlParams.get('telemetry_enabled')
  const paramEndpoint = urlParams.get('telemetry_endpoint')
  const paramDebug = urlParams.get('telemetry_debug')
  const paramCapture = urlParams.get('telemetry_capture')

  if (paramEnabled !== null) {
    config.enabled = parseBoolean(paramEnabled, DEFAULT_TELEMETRY_CONFIG.enabled)
  }
  if (paramEndpoint) {
    config.endpoint = paramEndpoint
  }
  if (paramDebug !== null) {
    config.debug = parseBoolean(paramDebug, DEFAULT_TELEMETRY_CONFIG.debug)
  }
  if (paramCapture) {
    captureSpec = paramCapture
  }

  // Set capture spec (defaults to 'all' if not specified)
  if (captureSpec) {
    config.capture = captureSpec
  }

  return config
}

const plugin: PluginObject<undefined> = {
  install(Vue) {
    // Get merged configuration
    const config = getTelemetryConfig()

    // Log configuration for debugging
    if (config.debug || process.env.NODE_ENV === 'development') {
      console.log('[Telemetry] Configuration:', {
        enabled: config.enabled ?? DEFAULT_TELEMETRY_CONFIG.enabled,
        endpoint: config.endpoint ?? DEFAULT_TELEMETRY_CONFIG.endpoint,
        debug: config.debug ?? DEFAULT_TELEMETRY_CONFIG.debug,
        source: {
          env: {
            VUE_APP_TELEMETRY_ENABLED: process.env.VUE_APP_TELEMETRY_ENABLED,
            VUE_APP_TELEMETRY_ENDPOINT: process.env.VUE_APP_TELEMETRY_ENDPOINT,
          },
          window: window.__NEKO_TELEMETRY_CONFIG__,
          queryParams: window.location.search,
        },
      })
    }

    // Initialize telemetry service
    const telemetry = initTelemetry(config)

    // Make telemetry available globally
    window.$telemetry = telemetry
    Vue.prototype.$telemetry = telemetry

    // Only install collectors if telemetry is enabled
    if (telemetry.getConfig().enabled) {
      // Install error collector (captures JS errors, Vue errors, network errors)
      installErrorCollector()

      // Install performance collector (Core Web Vitals, FPS, memory)
      installPerformanceCollector()

      // Track app ready when Vue is mounted
      let appReadyTracked = false
      Vue.mixin({
        mounted() {
          // Only track once for the first mount
          if (!appReadyTracked) {
            appReadyTracked = true
            getTelemetry().track('app_ready', 'info', {
              mountTime: Date.now(),
            })
          }
        },
      })
    }
  },
}

/**
 * Install connection collector after neko client is initialized.
 * Call this in the app after $client is available.
 */
export function installTelemetryConnectionCollector(client: NekoClient): void {
  const telemetry = getTelemetry()
  if (!telemetry.getConfig().enabled) {
    return
  }

  // Cast to any to work around strict type checking on EventEmitter
  installConnectionCollector(client as unknown as Parameters<typeof installConnectionCollector>[0])

  // Track login/logout actions
  const originalLogin = client.login.bind(client)
  const originalLogout = client.logout.bind(client)

  client.login = function (password: string, displayname: string) {
    getConnectionCollector().trackLogin(displayname)
    return originalLogin(password, displayname)
  }

  client.logout = function () {
    getConnectionCollector().trackLogout()
    return originalLogout()
  }
}

/**
 * Get the current telemetry configuration that was used.
 */
export function getTelemetryConfigUsed(): TelemetryConfig {
  return getTelemetry().getConfig()
}

export default plugin
