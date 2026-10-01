import "@testing-library/jest-dom/vitest";

class LocalStorageMock {
  constructor() {
    this.values = new Map();
  }

  clear() {
    this.values.clear();
  }

  getItem(key) {
    return this.values.has(String(key)) ? this.values.get(String(key)) : null;
  }

  removeItem(key) {
    this.values.delete(String(key));
  }

  setItem(key, value) {
    this.values.set(String(key), String(value));
  }
}

// Node 26 exposes an incomplete localStorage unless --localstorage-file is set.
// Keep browser-oriented tests deterministic across supported Node runtimes.
Object.defineProperty(globalThis, "localStorage", {
  configurable: true,
  value: new LocalStorageMock(),
});

class ResizeObserverMock {
  observe() {}
  unobserve() {}
  disconnect() {}
}

if (!globalThis.ResizeObserver) {
  globalThis.ResizeObserver = ResizeObserverMock;
}

if (!globalThis.PointerEvent) {
  globalThis.PointerEvent = MouseEvent;
}

if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function scrollIntoView() {};
}

const originalGetComputedStyle = window.getComputedStyle;
window.getComputedStyle = (element, pseudoElement) => {
  if (pseudoElement) return originalGetComputedStyle(element);
  return originalGetComputedStyle(element);
};

const originalConsoleError = console.error;

console.error = (...args) => {
  const message = String(args[0] || "");
  if (message.includes("not wrapped in act")) {
    return;
  }
  originalConsoleError(...args);
};
