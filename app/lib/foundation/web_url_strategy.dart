/// Disables Flutter's built-in browser history integration so
/// [WebHistoryBridge] can own the back button behaviour.
///
/// Flutter's web engine installs its own "single entry" browser history that
/// pushes a sentinel on every framework route change and also handles
/// `popstate` by sending `popRoute` to the root navigator. That conflicts with
/// the app-level bridge (double pops / growing history and, importantly, it
/// cannot pop the nested navigator used by comic/search pages). Turning the
/// engine integration off leaves exactly one browser history entry for the
/// app plus the sentinel managed by [WebHistoryBridge].
library;

export 'web_url_strategy_io.dart'
    if (dart.library.js_interop) 'web_url_strategy_web.dart';
