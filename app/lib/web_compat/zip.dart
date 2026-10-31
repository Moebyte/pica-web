/// Zip archive facade.
///
/// Native builds re-export `package:zip_flutter` (dart:ffi).
/// Web builds use a no-op stub: exporting/downloading comics as zip archives
/// is disabled on web.
library;

export 'package:zip_flutter/zip_flutter.dart'
    if (dart.library.js_interop) 'zip_web.dart';
