// The timers module Postman's sandbox exposes, backed by the script-scoped
// timer queue Go installs as the setTimeout/setImmediate globals. Delays are
// ordering only: no wall-clock time passes between callbacks.
module.exports = {
  setTimeout: function (fn, ms) { return globalThis.setTimeout(fn, ms); },
  setInterval: function (fn, ms) { return globalThis.setInterval(fn, ms); },
  setImmediate: function (fn) { return globalThis.setImmediate(fn); },
  clearTimeout: function (id) { return globalThis.clearTimeout(id); },
  clearInterval: function (id) { return globalThis.clearInterval(id); },
  clearImmediate: function (id) { return globalThis.clearImmediate(id); }
};
