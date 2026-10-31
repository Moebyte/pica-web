/// Bridges the browser back button / back gesture to the Flutter navigator.
///
/// Installed only on web. The bridge keeps a single sentinel browser-history
/// entry on top of the app entry:
///
/// * install: `replaceState(current)` + `pushState(sentinel)`
/// * `popstate` with a poppable root navigator: pop it and immediately push
///   the sentinel back, so the app history depth is unchanged.
/// * `popstate` with a poppable nested navigator (main tabs): pop that.
/// * nothing poppable: leave the browser alone, so the next back exits the app
///   (back on the root page = exit, which is the expected browser behaviour).
library;

export 'web_history_bridge_io.dart'
    if (dart.library.js_interop) 'web_history_bridge_web.dart';
