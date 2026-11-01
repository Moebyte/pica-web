import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:pica_comic/foundation/image_loader/image_bytes.dart';

void main() {
  test('keeps only the most recent entries', () {
    ImageByteCache.clear();
    for (var i = 0; i < 40; i++) {
      ImageByteCache.put("k$i", Uint8List(1024));
    }
    expect(ImageByteCache.get("k0"), isNull);
    expect(ImageByteCache.get("k7"), isNull);
    expect(ImageByteCache.get("k8"), isNotNull);
    expect(ImageByteCache.get("k39"), isNotNull);
  });

  test('drops an image that alone exceeds the byte budget', () {
    ImageByteCache.clear();
    ImageByteCache.put("huge", Uint8List(49 * 1024 * 1024));
    expect(ImageByteCache.get("huge"), isNull);
  });

  test('ignores empty keys and data', () {
    ImageByteCache.clear();
    ImageByteCache.put("", Uint8List(1));
    ImageByteCache.put("empty", Uint8List(0));
    expect(ImageByteCache.get(""), isNull);
    expect(ImageByteCache.get("empty"), isNull);
  });
}
