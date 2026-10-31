/// QuickJS engine facade.
///
/// Native builds re-export `package:flutter_qjs` (dart:ffi + dart:io).
/// Web builds use a no-op implementation so the app compiles and starts.
/// JavaScript comic sources are not available on web.
library;

export 'package:flutter_qjs/flutter_qjs.dart'
    if (dart.library.js_interop) 'jscore_web.dart';
