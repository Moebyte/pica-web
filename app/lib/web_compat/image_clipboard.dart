/// Clipboard/download helpers for images.
///
/// Native builds cannot copy an image to the system clipboard through a shared
/// API, so they expose no-op implementations; the Web build uses the Async
/// Clipboard API and a Blob download.
library;

export 'image_clipboard_io.dart'
    if (dart.library.js_interop) 'image_clipboard_web.dart';
