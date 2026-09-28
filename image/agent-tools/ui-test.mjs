import { build } from "esbuild";
import { JSDOM, VirtualConsole } from "jsdom";
import { resolve, join } from "node:path";

/** Mount the actual React entry component using the app's React dependencies.
 * Bundles in memory; never writes a build into the live app or modifies its deps.
 * This is a simulated DOM check, not CSS/layout or a real-browser assertion. */
export async function mountApp({ root = process.cwd(), entry = "src/App.tsx", url = "http://localhost/" } = {}) {
  root = resolve(root);
  const result = await build({
    absWorkingDir: root, stdin: {
      contents: `import React from 'react'; import {createRoot} from 'react-dom/client';
        import App from ${JSON.stringify("./" + entry)};
        const root = createRoot(document.getElementById('root'));
        window.__unmountApp = () => root.unmount(); root.render(React.createElement(App));`,
      resolveDir: root, sourcefile: "agent-ui-check.tsx", loader: "tsx",
    },
    bundle: true, write: false, format: "iife", platform: "browser", jsx: "automatic",
    alias: { "@": join(root, "src") },
    define: { "process.env.NODE_ENV": '"test"' },
    loader: { ".css": "empty", ".svg": "dataurl", ".png": "dataurl", ".jpg": "dataurl", ".webp": "dataurl", ".woff2": "dataurl" },
    logLevel: "silent",
  });
  const errors = [];
  const virtualConsole = new VirtualConsole();
  virtualConsole.on("jsdomError", error => errors.push(error.message));
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url, runScripts: "outside-only", pretendToBeVisual: true, virtualConsole,
  });
  dom.window.addEventListener("error", event => errors.push(event.message));
  try {
    dom.window.eval(result.outputFiles[0].text);
    await new Promise(resolve => setTimeout(resolve, 30));
    if (errors.length) throw new Error(errors.join("\n"));
  } catch (error) { dom.window.close(); throw error; }
  return {
    window: dom.window, document: dom.window.document, errors,
    async waitFor(assertion, timeoutMs = 2000) {
      const deadline = Date.now() + timeoutMs;
      for (;;) {
        if (errors.length) throw new Error(errors.join("\n"));
        try { await assertion(); return; } catch (error) {
          if (Date.now() >= deadline) throw error;
          await new Promise(resolve => setTimeout(resolve, 20));
        }
      }
    },
    close() { dom.window.__unmountApp?.(); dom.window.close(); },
  };
}
