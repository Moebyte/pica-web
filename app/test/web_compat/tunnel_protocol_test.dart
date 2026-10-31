import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:pica_comic/web_compat/tunnel_protocol.dart';

void main() {
  group('normalizeHeaderMap', () {
    test('skips null/empty values and joins multiple values', () {
      final result = normalizeHeaderMap({
        'User-Agent': 'Pica/1.0',
        'Referer': 'https://example.com/',
        'Cookie': null,
        'Accept': ['text/html', 'application/json'],
        'X-Number': 42,
        'X-Empty': '',
        '  ': 'ignored',
      });
      expect(result['User-Agent'], 'Pica/1.0');
      expect(result['Referer'], 'https://example.com/');
      expect(result.containsKey('Cookie'), isFalse);
      expect(result['Accept'], 'text/html, application/json');
      expect(result['X-Number'], '42');
      expect(result.containsKey('X-Empty'), isFalse);
      expect(result.length, 4);
    });

    test('strips CR/LF to avoid header injection', () {
      final result = normalizeHeaderMap({'X-Test': 'a\r\nb'});
      expect(result['X-Test'], 'a  b');
    });
  });

  group('request header encoding', () {
    test('is base64url without padding and decodes back', () {
      final encoded = encodeRequestHeaders({
        'User-Agent': 'Pica/1.0',
        'Referer': 'https://example.com/',
        'X-List': ['a', 'b'],
      });
      expect(encoded.contains('='), isFalse);
      expect(encoded.contains('+'), isFalse);
      expect(encoded.contains('/'), isFalse);

      final decoded = jsonDecode(decodeBase64UrlText(encoded)!)
          as Map<String, dynamic>;
      expect(decoded['User-Agent'], 'Pica/1.0');
      expect(decoded['Referer'], 'https://example.com/');
      expect(decoded['X-List'], 'a, b');
    });

    test('decodeBase64UrlText accepts padded input', () {
      final raw = base64Url.encode(utf8.encode('{"a":"b"}'));
      expect(decodeBase64UrlText(raw), '{"a":"b"}');
      expect(decodeBase64UrlText(''), isNull);
      expect(decodeBase64UrlText(null), isNull);
    });
  });

  group('decodeTunnelMeta', () {
    String encodeMeta(Map<String, dynamic> meta) =>
        base64Url.encode(utf8.encode(jsonEncode(meta))).replaceAll('=', '');

    test('parses status, headers, setCookies and finalUrl', () {
      final value = encodeMeta({
        'status': 302,
        'headers': {
          'Content-Type': ['text/html'],
          'Location': 'https://example.com/next',
        },
        'setCookies': ['sid=abc; Path=/; HttpOnly'],
        'finalUrl': 'https://example.com/next',
      });

      final meta = decodeTunnelMeta(value)!;
      expect(meta.status, 302);
      expect(meta.headers['Content-Type'], ['text/html']);
      expect(meta.setCookies, ['sid=abc; Path=/; HttpOnly']);
      expect(meta.finalUrl, 'https://example.com/next');
    });

    test('accepts string header values and padded base64', () {
      final raw = base64Url.encode(utf8.encode(jsonEncode({
        'status': 200,
        'headers': {'X-Test': 'yes'},
      })));
      final meta = decodeTunnelMeta(raw)!;
      expect(meta.status, 200);
      expect(meta.headers['X-Test'], ['yes']);
      expect(meta.setCookies, isEmpty);
      expect(meta.finalUrl, isNull);
    });

    test('returns null for malformed input', () {
      expect(decodeTunnelMeta(null), isNull);
      expect(decodeTunnelMeta(''), isNull);
      expect(decodeTunnelMeta('!!!not-base64!!!'), isNull);
      expect(decodeTunnelMeta(encodeMeta({'status': 'nope', 'headers': 'no'})),
          isNotNull);
    });
  });

  group('buildResponseHeaders', () {
    test('lowercases names and appends set-cookie', () {
      const meta = PicaTunnelMeta(
        status: 200,
        headers: {
          'Content-Type': ['image/png'],
          'Content-Length': ['3'],
        },
        setCookies: ['sid=1', 'theme=dark'],
        finalUrl: null,
      );
      final headers = buildResponseHeaders(meta);
      expect(headers['content-type'], ['image/png']);
      expect(headers['content-length'], ['3']);
      expect(headers['set-cookie'], ['sid=1', 'theme=dark']);
      expect(headers.containsKey('Content-Type'), isFalse);
    });
  });

  group('shouldTunnel', () {
    final gateway = Uri.parse('http://127.0.0.1:18081/api/tunnel');

    test('bypasses same-origin requests', () {
      expect(shouldTunnel(Uri.parse('http://127.0.0.1:18081/api/auth/status'),
          gatewayUri: gateway), isFalse);
      expect(shouldTunnel(Uri.parse('http://127.0.0.1:18081/assets/main.js'),
          gatewayUri: gateway), isFalse);
    });

    test('tunnels cross-origin http(s) requests', () {
      expect(shouldTunnel(Uri.parse('https://nhentai.net/api/galleries/search'),
          gatewayUri: gateway), isTrue);
      expect(shouldTunnel(Uri.parse('http://127.0.0.1:9000/image.png'),
          gatewayUri: gateway), isTrue);
    });

    test('ignores non-http schemes', () {
      expect(shouldTunnel(Uri.parse('data:image/png;base64,AAAA'),
          gatewayUri: gateway), isFalse);
      expect(shouldTunnel(Uri.parse('blob:https://example.com/id'),
          gatewayUri: gateway), isFalse);
    });
  });

  group('gatewayErrorMessage', () {
    test('explains 401 clearly', () {
      final message = gatewayErrorMessage(401, 'unauthorized', '{"error":"unauthorized"}');
      expect(message, contains('authentication required'));
      expect(message, contains('401'));
    });

    test('includes code and body snippet otherwise', () {
      final message = gatewayErrorMessage(502, 'upstream_error', 'dial failed');
      expect(message, contains('502'));
      expect(message, contains('upstream_error'));
      expect(message, contains('dial failed'));
    });
  });

  group('resolveTunnelUri', () {
    test('resolves against the page URI', () {
      final uri = resolveTunnelUri(Uri.parse('http://localhost:18081/#/page'));
      expect(uri.toString(), 'http://localhost:18081/api/tunnel');
    });
  });
}
