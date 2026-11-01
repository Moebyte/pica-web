import 'dart:js_interop';
import 'dart:typed_data';

import 'package:web/web.dart' as web;

bool get canCopyImageToClipboard => true;

/// Writes an image to the system clipboard via the Async Clipboard API.
///
/// Browsers only mandate PNG support, so callers must pass PNG bytes.
Future<bool> copyImageToClipboard(Uint8List bytes, String mimeType) async {
  try {
    final blob =
        web.Blob([bytes.toJS].toJS, web.BlobPropertyBag(type: mimeType));
    final item = web.ClipboardItem({'image/png': blob}.jsify()! as JSObject);
    await web.window.navigator.clipboard.write([item].toJS).toDart;
    return true;
  } catch (_) {
    return false;
  }
}

void downloadImageBytes(Uint8List bytes, String filename, String mimeType) {
  final blob = web.Blob([bytes.toJS].toJS, web.BlobPropertyBag(type: mimeType));
  final url = web.URL.createObjectURL(blob);
  final anchor = web.HTMLAnchorElement()
    ..href = url
    ..download = filename
    ..style.display = 'none';
  web.document.body?.appendChild(anchor);
  anchor.click();
  anchor.remove();
  web.URL.revokeObjectURL(url);
}
