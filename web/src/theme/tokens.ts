// Design tokens for the agenda console.
//
// Concept: a "control surface / machine surface" duality. The app chrome you
// configure things in (nav, forms, tables) is a calm, warm, light surface —
// this is a tool used for hours at a time, not a dashboard to glance at, so
// it avoids the reflexive all-dark admin-panel look. Anywhere raw machine
// output actually appears — pipeline steps, logs, route/instance identifiers
// — switches to a dark monospace "terminal" surface. That switch is the
// throughline: it's how a user tells "what I configured" from "what the
// machine just told me" at a glance.
export const color = {
  ink: '#14151A', // machine-surface background (terminal/log/pipeline panels)
  inkRaised: '#1D1F26', // machine-surface card/step background
  inkBorder: '#2B2E38',
  paper: '#F4F2ED', // control-surface app background
  paperRaised: '#FFFFFF', // control-surface cards
  paperBorder: '#E4E0D6',
  ink900: '#1B1C21', // primary text on paper
  ink500: '#6B6E7A', // secondary text on paper
  signal: '#FF8A3D', // amber — building / attention / primary accent
  signalDark: '#DB6E27',
  verified: '#2FB673', // teal-green — live / verified / success
  fail: '#E5484D', // red — failed
  wire: '#3B82C4', // blue — informational / links / instance pins
} as const

export const font = {
  display: '"Space Grotesk", "Inter", sans-serif',
  body: '"Inter", -apple-system, BlinkMacSystemFont, sans-serif',
  mono: '"IBM Plex Mono", "SFMono-Regular", Consolas, monospace',
} as const

export const radius = {
  sm: 4,
  md: 6,
  lg: 10,
} as const
