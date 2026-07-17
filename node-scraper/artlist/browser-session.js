// Browser session lifecycle: open -> createPage -> close.
//
// Flat-path facade so consumers can import the helpers from
// `artlist/browser-session.js` without reaching into `src/driver/`.
// Implementation lives in `src/driver/browser.js`. The two pure
// helpers (`makeTempBrowserDir`, `resolveChromeProfile`) are safe to
// unit-test; the three browser-bound helpers (`openBrowser`,
// `createBrowserPage`, `closeBrowserHandle`) require Puppeteer.
//
// Exports:
//
//   makeTempBrowserDir() -> string                             (pure)
//   resolveChromeProfile(profileDir) -> string                 (pure)
//   openBrowser(profileDir) -> Promise<{browser, connected}>   (puppeteer)
//   createBrowserPage(profileDir) -> Promise<handle>          (puppeteer)
//   closeBrowserHandle(handle) -> Promise<void>               (puppeteer)

export {
  makeTempBrowserDir,
  resolveChromeProfile,
  openBrowser,
  createBrowserPage,
  closeBrowserHandle,
} from '../src/driver/browser.js';
