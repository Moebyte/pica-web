import 'dart:typed_data';

bool get canCopyImageToClipboard => false;

Future<bool> copyImageToClipboard(Uint8List bytes, String mimeType) async {
  return false;
}

void downloadImageBytes(Uint8List bytes, String filename, String mimeType) {}
