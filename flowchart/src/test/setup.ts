import '@testing-library/jest-dom/vitest'

class ResizeObserverStub {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver
}

if (typeof globalThis.DOMMatrixReadOnly === 'undefined') {
  class DOMMatrixReadOnlyStub {
    m22 = 1
    constructor(_init?: string) {}
  }
  globalThis.DOMMatrixReadOnly =
    DOMMatrixReadOnlyStub as unknown as typeof DOMMatrixReadOnly
}
