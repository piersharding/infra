import { Component } from 'react'

export default class ErrorBoundary extends Component {
  constructor(props) {
    super(props)
    this.state = { hasError: false, error: null }
  }

  static getDerivedStateFromError(error) {
    return { hasError: true, error }
  }

  componentDidCatch(error, errorInfo) {
    console.error('[ErrorBoundary] Caught:', error.message, 'in component:', this.componentName || 'unknown')
    console.error('[ErrorBoundary] Stack:', error.stack)
    if (errorInfo && errorInfo.componentStack) {
      console.error('[ErrorBoundary] Component stack:', errorInfo.componentStack)
    }
  }

  render() {
    if (this.state.hasError) {
      return this.props.fallback || <h1>Something went wrong</h1>
    }
    return this.props.children
  }
}
