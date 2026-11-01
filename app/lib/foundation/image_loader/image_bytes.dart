import 'dart:collection';
import 'dart:typed_data';

/// Keeps the encoded bytes of recently loaded images so a long-press action
/// can copy/save the image without downloading it again.
///
/// Only the most recent images are retained (bounded by both entry count and
/// total bytes); the reader's decoded-image cache is the primary memory user,
/// this is a small side buffer for explicit user actions.
class ImageByteCache {
  static const int _maxEntries = 32;
  static const int _maxBytes = 48 * 1024 * 1024;

  static final LinkedHashMap<String, Uint8List> _entries = LinkedHashMap();

  static Uint8List? get(String key) {
    final bytes = _entries.remove(key);
    if (bytes != null) {
      _entries[key] = bytes;
    }
    return bytes;
  }

  static void put(String key, Uint8List bytes) {
    if (key.isEmpty || bytes.isEmpty) return;
    _entries.remove(key);
    _entries[key] = bytes;
    while (_entries.length > _maxEntries ||
        _entries.values.fold<int>(0, (sum, e) => sum + e.length) > _maxBytes) {
      if (_entries.isEmpty) break;
      _entries.remove(_entries.keys.first);
    }
  }

  static void clear() => _entries.clear();
}
