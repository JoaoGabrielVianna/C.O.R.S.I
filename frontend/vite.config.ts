import { defineConfig, type PluginOption } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

/** Where the dev server forwards API calls. */
const BACKEND_TARGET = process.env.CORSI_BACKEND_TARGET ?? 'http://localhost:8080'

/**
 * Every path prefix the Go API owns.
 *
 * ── Why this is one list and not seven proxy entries ───────────────────
 * Because the hand-maintained version drifted, exactly as its own comment
 * predicted it would. `/job-radar` was never added, so in `npm run dev`
 * every Job Radar API call fell through to the SPA fallback and received
 * `index.html` where it expected JSON. The module looked broken while the
 * database and the backend were both correct — the second time that
 * failure has happened here, after Releases.
 *
 * A missing prefix does not fail loudly on its own, which is what makes
 * the drift expensive. Keeping the list in one place does not prevent
 * someone forgetting to add a new context, but it puts the whole answer to
 * "what does the API own?" on one screen, and it is what the identity
 * probe below reads to check it can reach one.
 */
const API_PREFIXES = [
  '/finance',
  // Streams Server-Sent Events. http-proxy forwards the body as it
  // arrives, so no extra buffering config is needed.
  '/chat',
  '/releases',
  '/integrations',
  // Added after the drift above. The tool namespace is `job_radar`; the
  // URL uses a hyphen, like every other route in the product.
  '/job-radar',
  // The Palace read surface. Missing it reproduced the same failure a
  // third time: the Library rendered "não foi possível carregar" while
  // the backend served the routes correctly.
  '/palace',
  '/health',
  '/metrics',
  '/openapi.yaml',
  '/swagger',
] as const

function apiProxy() {
  return Object.fromEntries(
    API_PREFIXES.map((prefix) => [prefix, { target: BACKEND_TARGET, changeOrigin: false }]),
  )
}

/**
 * Asks the proxy target who it is, once, when the dev server starts.
 *
 * ── Why a port is not an identity ──────────────────────────────────────
 * Another project's API was found holding `localhost:8080`. It answered
 * `200 OK` on `/health/live` and `404` on every C.O.R.S.I. route, so the
 * product read as broken rather than absent. And the real state was worse
 * than a stolen port: `localhost` resolves to both `127.0.0.1` and `::1`,
 * and two different products held the port simultaneously, one per family,
 * neither failing to bind.
 *
 * So this checks BOTH families when the target is a loopback name. The
 * browser, Node and curl do not have to agree on which one they pick, and
 * a check that only looked at one would pass while the app talked to the
 * other.
 *
 * It warns rather than refusing to start: a dev server that would not boot
 * because the backend is not up yet would be an obstacle, not a guard. The
 * app itself blocks on a positive mismatch — see src/main.tsx.
 */
function backendIdentityCheck(): PluginOption {
  return {
    name: 'corsi-backend-identity',
    apply: 'serve',
    configureServer(server) {
      void (async () => {
        const url = new URL(BACKEND_TARGET)
        const hosts =
          url.hostname === 'localhost' ? ['127.0.0.1', '::1'] : [url.hostname]

        for (const host of hosts) {
          const authority = host.includes(':') ? `[${host}]` : host
          const probe = `${url.protocol}//${authority}:${url.port || '80'}/health/live`
          let application: string
          try {
            const res = await fetch(probe, { signal: AbortSignal.timeout(1500) })
            application = ((await res.json()) as { application?: string }).application ?? ''
          } catch {
            // Nothing there, or it did not answer JSON. Both are normal
            // before `make dev` finishes, and neither is worth a warning.
            continue
          }
          if (application === 'corsi') {
            server.config.logger.info(
              `  \x1b[32m➜\x1b[0m  backend: C.O.R.S.I. at ${probe}`,
            )
            continue
          }
          server.config.logger.error(
            `\n\x1b[41m\x1b[97m BACKEND ERRADO \x1b[0m\n` +
              `  ${probe}\n` +
              `  respondeu, e NÃO é o C.O.R.S.I. (application=${application || 'ausente'}).\n` +
              `  Requests do proxy vão para essa API. Os 404 dela parecerão bugs deste produto.\n`,
          )
        }
      })()
    },
  }
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss(), backendIdentityCheck()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    // ── Why one host and not `true` ────────────────────────────────────
    // Vite refuses requests whose Host header it does not recognise, which
    // is what stops a public tunnel from being pointed at a developer's
    // machine by anyone who learns the URL. Setting `allowedHosts: true`
    // would turn that off entirely and for every host, forever.
    //
    // The Threads OAuth flow needs exactly one public origin — Meta will
    // only redirect to the URI registered in its console — so exactly one
    // host is listed. `localhost` and `127.0.0.1` are permitted by Vite by
    // default and are deliberately not repeated here.
    //
    // This is the SAME origin the browser must use for the whole flow: the
    // card derives its redirect_uri from `window.location`, so opening the
    // app on localhost while Meta redirects to the tunnel would send two
    // different `redirect_uri` values and Meta would refuse the exchange.
    // The host itself is deployment-specific and is read from the
    // environment, so no operator's tunnel URL is committed here.
    allowedHosts: process.env.VITE_DEV_ALLOWED_HOST
      ? [process.env.VITE_DEV_ALLOWED_HOST]
      : [],
    // Dev-only — forward backend calls so we don't need CORS on the Go
    // server. Production deploys put both behind the same upstream.
    //
    // The tunnel exposes THIS server, not the API: `/integrations/*` keeps
    // going through this proxy to :8080, so no second tunnel exists and the
    // backend stays off the public internet.
    proxy: apiProxy(),
  },
})
