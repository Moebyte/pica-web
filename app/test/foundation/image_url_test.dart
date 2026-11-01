import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:pica_comic/foundation/image_url.dart';

String signed(String raw) {
  final payload = base64Url.encode(utf8.encode(jsonEncode({
    'url': raw,
    'profile': 'jm',
  })));
  return '/api/img/$payload.signature';
}

void main() {
  test('keeps raw upstream urls untouched', () {
    const url = 'https://cdn.example.com/media/albums/1_3x4.jpg';
    expect(stableImageUrl(url), url);
  });

  test('unwraps a relative signed image url', () {
    const raw = 'https://cdn.example.com/media/albums/1_3x4.jpg';
    expect(stableImageUrl(signed(raw)), raw);
  });

  test('unwraps an absolute same-origin signed image url', () {
    const raw = 'https://cdn.example.com/media/albums/1_3x4.jpg';
    expect(stableImageUrl('https://picacg.example.com${signed(raw)}'), raw);
  });

  test('keeps malformed tokens instead of dropping the cover', () {
    expect(
        stableImageUrl('/api/img/not-a-token.sig'), '/api/img/not-a-token.sig');
    expect(stableImageUrl('/api/img/'), '/api/img/');
    expect(stableImageUrl(''), '');
  });

  test('does not unwrap non-http payloads', () {
    final payload =
        base64Url.encode(utf8.encode(jsonEncode({'url': 'data:x'})));
    final url = '/api/img/$payload.sig';
    expect(stableImageUrl(url), url);
  });
}
