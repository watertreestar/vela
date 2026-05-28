import "@testing-library/jest-dom/vitest";

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

const originalConsoleError = console.error;

console.error = (...args) => {
  const message = String(args[0] || "");
  if (message.includes("not wrapped in act")) {
    return;
  }
  originalConsoleError(...args);
};
