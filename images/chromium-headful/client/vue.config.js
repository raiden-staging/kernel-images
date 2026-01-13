const path = require('path')
const webpack = require('webpack')

module.exports = {
  productionSourceMap: false,
  css: {
    loaderOptions: {
      sass: {
        additionalData: `
          @import "@/assets/styles/_variables.scss";
        `,
      },
    },
  },
  publicPath: './',
  assetsDir: './',
  configureWebpack: {
    resolve: {
      alias: {
        vue$: 'vue/dist/vue.esm.js',
        '~': path.resolve(__dirname, 'src/'),
      },
    },
    plugins: [
      // Define environment variables for telemetry configuration
      // These can be set at build time via:
      //   VUE_APP_TELEMETRY_ENABLED=true npm run build
      //   VUE_APP_TELEMETRY_ENDPOINT=https://example.com/telemetry npm run build
      //   VUE_APP_TELEMETRY_DEBUG=true npm run build
      //   VUE_APP_TELEMETRY_CAPTURE='error_*,perf_fps,connection_*' npm run build
      //   VUE_APP_TELEMETRY_CAPTURE='all,-perf_memory,-perf_fps' npm run build
      //   VUE_APP_TELEMETRY_CAPTURE='all' npm run build (default)
      new webpack.DefinePlugin({
        'process.env.VUE_APP_TELEMETRY_ENABLED': JSON.stringify(process.env.VUE_APP_TELEMETRY_ENABLED || 'true'),
        'process.env.VUE_APP_TELEMETRY_ENDPOINT': JSON.stringify(process.env.VUE_APP_TELEMETRY_ENDPOINT || ''),
        'process.env.VUE_APP_TELEMETRY_DEBUG': JSON.stringify(process.env.VUE_APP_TELEMETRY_DEBUG || 'false'),
        'process.env.VUE_APP_TELEMETRY_CAPTURE': JSON.stringify(process.env.VUE_APP_TELEMETRY_CAPTURE || 'all'),
      }),
    ],
  },
  devServer: {
    allowedHosts: 'all',
  },
}
