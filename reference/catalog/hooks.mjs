// Node module resolve hook: maps the web flasher's absolute imports ("/lib/x.js", "/js/x.js") onto the checkout in
// reference/.flasher, so js/catalog.js and js/flash.js run exactly as committed at 45a5257, with no edits.
let root = null;

export async function initialize(data) {
  root = data.root.replace(/\/$/, '');
}

export async function resolve(specifier, context, next) {
  if (/^\/(lib|js)\//.test(specifier)) {
    return { url: 'file://' + root + specifier, shortCircuit: true, format: 'module' };
  }
  return next(specifier, context);
}
