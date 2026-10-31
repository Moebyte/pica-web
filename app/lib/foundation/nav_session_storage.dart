/// Synchronous sessionStorage facade used by [NavSnapshot].
///
/// The browser implementation stores the navigation snapshot in
/// `window.sessionStorage`, which survives reloads in the same tab but is not
/// shared with other tabs. Native builds are no-ops.
library;

export 'nav_session_storage_io.dart'
    if (dart.library.js_interop) 'nav_session_storage_web.dart';
