// Copied unchanged from the meshflash research prototype (proto-esp32/hooks.mjs).
// Node module resolve hook: maps the web flasher's absolute imports ("/lib/x.js", "/js/x.js")
// onto the local checkout, so js/flash.js can be imported unmodified.
let root = null;
export async function initialize(data) { root = data.root.replace(/\/$/, ''); }
export async function resolve(specifier, context, next) {
  if (/^\/(lib|js)\//.test(specifier)) {
    return { url: 'file://' + root + specifier, shortCircuit: true, format: 'module' };
  }
  return next(specifier, context);
}
