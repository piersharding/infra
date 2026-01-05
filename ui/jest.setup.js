import '@testing-library/jest-dom/extend-expect'

// Disable UI transitions/animations in tests (consumed by our wrapper at
// `components/ui/transition.js`) to avoid flaky timer/RAF behavior and noisy act warnings.
process.env.DISABLE_HEADLESSUI_TRANSITIONS = '1'

// jsdom (Jest's default browser-like environment) doesn't provide ResizeObserver.
// Some UI libs (e.g. @headlessui/react) rely on it.
// This lightweight polyfill is sufficient for unit tests that don't depend on actual layout measurements.
if (typeof global.ResizeObserver === 'undefined') {
  class ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  }

  global.ResizeObserver = ResizeObserver
}
