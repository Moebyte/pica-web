/// Small browser-only runtime helpers used by the reader and navigation code:
///
/// * Screen Wake Lock (keep the screen on while reading)
/// * Fullscreen (immersive reading on mobile browsers)
/// * Page lifecycle notifications (`visibilitychange` / `pagehide`)
/// * Mobile browser detection
///
/// Native platforms get no-op implementations so the same code can be shared
/// without conditional imports at every call site.
library;

export 'web_runtime_io.dart'
    if (dart.library.js_interop) 'web_runtime_web.dart';
