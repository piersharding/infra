import { Transition as HeadlessTransition } from '@headlessui/react'

// In tests we want to avoid animation-related async updates (timers, RAF, etc.)
// that cause flaky behavior and noisy "act(...)" warnings.
// Set `DISABLE_HEADLESSUI_TRANSITIONS=1` in the Jest environment to enable.
const transitionsDisabled =
  typeof process !== 'undefined' &&
  process.env &&
  (process.env.DISABLE_HEADLESSUI_TRANSITIONS === '1' ||
    process.env.DISABLE_HEADLESSUI_TRANSITIONS === 'true')

/**
 * `Transition` wrapper that behaves like `@headlessui/react` Transition, but can
 * render children immediately (no animations) when transitions are disabled.
 *
 * Intended usage:
 *   import Transition from '@/components/ui/transition'
 *   // then: <Transition> / <Transition.Root> / <Transition.Child>
 */
function Transition(props) {
  if (!transitionsDisabled) return <HeadlessTransition {...props} />

  const { show = true, children } = props

  // For non-root uses, HeadlessUI's `Transition` supports `show`.
  // When disabled, only render when show is truthy.
  return show ? <>{children}</> : null
}

Transition.Child = function TransitionChild(props) {
  if (!transitionsDisabled) return <HeadlessTransition.Child {...props} />

  const { show = true, children } = props
  // `Transition.Child` doesn't normally take `show`, but some call sites may pass it.
  return show ? <>{children}</> : null
}

Transition.Root = function TransitionRoot(props) {
  if (!transitionsDisabled) return <HeadlessTransition.Root {...props} />

  // IMPORTANT: preserve `show` gating so components like <Dialog ... onClose=...>
  // are not rendered when closed (Headless UI will throw otherwise).
  const { show, children } = props
  return show ? <>{children}</> : null
}

export default Transition
