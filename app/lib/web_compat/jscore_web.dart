/// No-op QuickJS implementation for web.
///
/// Nothing is evaluated and no JavaScript runtime is started. Calls are
/// accepted so that code paths shared with native platforms stay simple, but
/// every evaluation returns a stub function (or null for simple values).
library;

class WebPort {
  void close() {}
}

/// Stub counterpart of `package:flutter_qjs`'s `JSInvokable`.
abstract class JSInvokable {
  dynamic invoke(List args, [dynamic thisVal]);

  dynamic call(List args) => invoke(args);

  void free();
}

class _StubFunction extends JSInvokable {
  @override
  dynamic invoke(List args, [dynamic thisVal]) => null;

  @override
  void free() {}
}

/// No-op QuickJS engine.
class FlutterQjs {
  FlutterQjs({
    Object? moduleHandler,
    int? stackSize,
    int? timeout,
    int? memoryLimit,
    Object? hostPromiseRejectionHandler,
  });

  final WebPort port = WebPort();

  void dispatch() {}

  /// Returns a stub JS function. The engine discards the result.
  dynamic evaluate(String source, {String? name}) => _StubFunction();

  void close() {}
}
